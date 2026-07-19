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
	compiler.AssertFormat = true

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

	blockIDs := make(map[string]bool)

	for i, block := range cfg.Blocks {
		if blockIDs[block.ID] {
			return fmt.Errorf("duplicate block id found: %s", block.ID)
		}
		blockIDs[block.ID] = true

		validElementIDs := make(map[string]bool)
		for _, el := range block.Elements {
			if validElementIDs[el.ID] {
				return fmt.Errorf("block[%d] (id: %s): duplicate element id found: %s", i, block.ID, el.ID)
			}
			validElementIDs[el.ID] = true
		}

		if err := validateBlockLogic(block, validElementIDs); err != nil {
			return fmt.Errorf("block[%d] (id: %s) logic error: %w", i, block.ID, err)
		}
	}

	return nil
}

func validateBlockLogic(b Block, validElements map[string]bool) error {
	switch b.Type {
	case BlockTypeSingleChoice:
		return validateSingleChoice(b, validElements)
	case BlockTypeMultiChoice:
		return validateMultiChoice(b, validElements)
	case BlockTypeSequence:
		return validateSequence(b, validElements)
	case BlockTypeMatching:
		return validateMatching(b, validElements)
	case BlockTypeCategories:
		return validateCategories(b, validElements)
	case BlockTypeGrid:
		// Для сетки (grid)
		return nil
	}

	return nil
}

func validateSingleChoice(b Block, validElements map[string]bool) error {
	correctCount := countCorrectAnswers(b.Answers)
	if correctCount != 1 {
		return fmt.Errorf("single_choice requires exactly 1 correct answer, got %d", correctCount)
	}
	for _, ans := range b.Answers {
		if !validElements[ans.ElementID] {
			return fmt.Errorf("invalid element_id in answers: %s", ans.ElementID)
		}
	}
	return nil
}

func validateMultiChoice(b Block, validElements map[string]bool) error {
	correctCount := countCorrectAnswers(b.Answers)
	if correctCount < 1 {
		return fmt.Errorf("multi_choice requires at least 1 correct answer, got %d", correctCount)
	}
	for _, ans := range b.Answers {
		if !validElements[ans.ElementID] {
			return fmt.Errorf("invalid element_id in answers: %s", ans.ElementID)
		}
	}
	return nil
}

func validateSequence(b Block, validElements map[string]bool) error {
	orderMap := make(map[int]bool)
	for _, seq := range b.Sequence {
		if orderMap[seq.Order] {
			return fmt.Errorf("sequence requires unique order, duplicate found: %d", seq.Order)
		}
		orderMap[seq.Order] = true

		if !validElements[seq.ElementID] {
			return fmt.Errorf("invalid element_id in sequence: %s", seq.ElementID)
		}
	}
	return nil
}

func validateMatching(b Block, validElements map[string]bool) error {
	for _, pair := range b.Pairs {
		if !validElements[pair.LeftID] {
			return fmt.Errorf("invalid left_id in matching pair: %s", pair.LeftID)
		}
		if !validElements[pair.RightID] {
			return fmt.Errorf("invalid right_id in matching pair: %s", pair.RightID)
		}
	}
	return nil
}

func validateCategories(b Block, validElements map[string]bool) error {
	for _, cat := range b.Category {
		for _, itemID := range cat.Items {
			if !validElements[itemID] {
				return fmt.Errorf("invalid item_id in category items: %s", itemID)
			}
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
