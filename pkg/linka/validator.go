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
			if err := validateElement(el); err != nil {
				return fmt.Errorf("block[%d] (id: %s) element %s: %w", i, block.ID, el.ID, err)
			}
		}

		if err := validateLayout(block); err != nil {
			return fmt.Errorf("block[%d] (id: %s): %w", i, block.ID, err)
		}
		if err := validateBlockLogic(block, validElementIDs); err != nil {
			return fmt.Errorf("block[%d] (id: %s) logic error: %w", i, block.ID, err)
		}
	}

	return nil
}

// validateElement — пустая карточка и пробел не несут содержимого:
// если оно там есть, клиент его не покажет, а автор об этом не узнает.
func validateElement(el Element) error {
	switch el.Kind {
	case ElementKindEmpty, ElementKindSpace:
		if el.Text != "" || el.Image != nil || el.Audio != nil {
			return fmt.Errorf("kind %q must not carry content", el.Kind)
		}
	}
	return nil
}

// validateLayout проверяет сетку блока, если она задана явно. Для блоков
// без layout проверки нет: они сохранены до появления поля, и ронять их
// на сохранении нельзя. Переполнение там страхует конвертер.
//
// Сетку задаёт только grid. У остальных типов размер — это длина
// массива: вариантов у выбора и последовательности, пар у сопоставления,
// категорий и вариантов у распределения. Отдельного поля «количество»
// нет, чтобы не было двух источников правды.
func validateLayout(b Block) error {
	if b.Layout == nil {
		return nil
	}
	if b.Type != BlockTypeGrid {
		return fmt.Errorf("layout is not applicable to block type %q", b.Type)
	}
	if got, capacity := len(b.Elements), b.Layout.Capacity(); got > capacity {
		// Looks обрезает страницу до rows×columns, лишние карточки
		// пропали бы молча.
		return fmt.Errorf("%d elements exceeds layout capacity %d (%dx%d)",
			got, capacity, b.Layout.Rows, b.Layout.Columns)
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
	headers := make(map[string]string, len(b.Category))
	for _, cat := range b.Category {
		if cat.ElementID == "" && cat.Name == "" {
			return fmt.Errorf("category %s needs element_id or name", cat.ID)
		}
		if cat.ElementID != "" {
			if !validElements[cat.ElementID] {
				return fmt.Errorf("category %s header: unknown element %s", cat.ID, cat.ElementID)
			}
			if other, taken := headers[cat.ElementID]; taken {
				return fmt.Errorf("element %s is a header of more than one category (%s, %s)",
					cat.ElementID, other, cat.ID)
			}
			headers[cat.ElementID] = cat.ID
		}
		for _, itemID := range cat.Items {
			if !validElements[itemID] {
				return fmt.Errorf("invalid item_id in category items: %s", itemID)
			}
		}
	}
	// Карточка не может быть одновременно заголовком и вариантом ответа:
	// ребёнок не должен раскладывать «лес» в «лес».
	for _, cat := range b.Category {
		for _, itemID := range cat.Items {
			if _, isHeader := headers[itemID]; isHeader {
				return fmt.Errorf("category %s: header %s is also an item", cat.ID, itemID)
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
