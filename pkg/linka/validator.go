package linka

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

//go:embed schema.json
var schemaBytes []byte

var compiledSchema *jsonschema.Schema

const MaxConfigSize = 5 * 1024 * 1024

func init() {
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("schema.json", bytes.NewReader(schemaBytes)); err != nil {
		panic(fmt.Errorf("failed to add schema resource: %w", err))
	}

	schema, err := compiler.Compile("schema.json")
	if err != nil {
		panic(fmt.Errorf("failed to compile linka schema: %w", err))
	}
	compiledSchema = schema
}

func ValidateConfig(ctx context.Context, data json.RawMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if len(data) > MaxConfigSize {
		return errors.New("validation error: config file exceeds maximum size of 5MB")
	}

	var v interface{}
	if err := json.Unmarshal(data, &v); err != nil {
		return fmt.Errorf("invalid json format: %w", err)
	}

	if err := compiledSchema.Validate(v); err != nil {
		return fmt.Errorf("schema validation failed: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("failed to parse config structs: %w", err)
	}

	for i, block := range cfg.Blocks {
		if err := validateBlockLogic(block); err != nil {
			return fmt.Errorf("block[%d] (id: %s) logic error: %w", i, block.ID, err)
		}
	}

	return nil
}

func validateBlockLogic(b Block) error {
	switch b.Type {
	case BlockTypeSingleChoice:
		correctCount := countCorrectAnswers(b.Answers)
		if correctCount != 1 {
			return fmt.Errorf("single_choice requires exactly 1 correct answer, got %d", correctCount)
		}

	case BlockTypeMultiChoice:
		correctCount := countCorrectAnswers(b.Answers)
		if correctCount < 1 {
			return fmt.Errorf("multi_choice requires at least 1 correct answer, got %d", correctCount)
		}

	case BlockTypeSequence:
		orderMap := make(map[int]bool)
		for _, seq := range b.Sequence {
			if orderMap[seq.Order] {
				return fmt.Errorf("sequence requires unique order, duplicate found: %d", seq.Order)
			}
			orderMap[seq.Order] = true
		}
	}
	return nil
}

func countCorrectAnswers(answers []Answer) int {
	count := 0
	for _, a := range answers {
		if a.IsCorrect {
			count++
		}
	}
	return count
}
