package linka

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateConfig(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name        string
		fixtureName string
		wantErr     bool
		errMsg      string
	}{
		{"Valid Grid", "valid_grid.json", false, ""},
		{"Valid Empty Pack", "valid_empty_pack.json", false, ""},
		{"Valid Single Choice", "valid_single_choice.json", false, ""},
		{"Valid Sequence", "valid_sequence.json", false, ""},
		{"Valid Matching", "valid_matching.json", false, ""},
		{"Valid Categories", "valid_categories.json", false, ""},
		{"Invalid Single (0 correct)", "invalid_single_0.json", true, "requires exactly 1 correct answer"},
		{"Invalid Single (2 correct)", "invalid_single_2.json", true, "requires exactly 1 correct answer"},
		{"Invalid Multi (0 correct)", "invalid_multi_0.json", true, "requires at least 1 correct answer"},
		{"Invalid Sequence (Dup order)", "invalid_seq_dup.json", true, "unique order"},
		{"Invalid Type (Open Answer)", "invalid_open_answer.json", true, "schema validation failed"},
		{"Invalid Settings (Rows > 100)", "invalid_rows_101.json", true, "schema validation failed"},
		{"Invalid Empty Pack", "invalid_empty_pack.json", true, "schema validation failed"},
		{"Invalid Typo Property", "invalid_typo_property.json", true, "schema validation failed"},
		{"Invalid Broken Link", "invalid_broken_link.json", true, "invalid element_id"},
		{"Invalid Duplicate Element ID", "invalid_duplicate_element_id.json", true, "duplicate element id found"},
		{"Invalid Missing Array", "invalid_missing_array.json", true, "schema validation failed"},
		{"Invalid Empty Object", "invalid_empty_object.json", true, "schema validation failed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join("testdata", tt.fixtureName)
			data, err := os.ReadFile(path)

			if err != nil {
				t.Fatalf("failed to load fixture %s: %v", tt.fixtureName, err)
			}

			err = ValidateConfig(ctx, json.RawMessage(data))

			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateConfig() error = %v, wantErr %v", err, tt.wantErr)
			}

			if err != nil && tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("Expected error containing %q, got %q", tt.errMsg, err.Error())
			}
		})
	}
}

func TestValidateConfigAllowsBlockName(t *testing.T) {
	config := json.RawMessage(`{
		"metadata":{"version":"2.0"},
		"settings":{"columns":1,"rows":1},
		"blocks":[{
			"id":"block","type":"grid","name":"Животные",
			"elements":[{"id":"el","kind":"text","value":"кот"}]
		}]
	}`)

	if err := ValidateConfig(t.Context(), config); err != nil {
		t.Fatalf("config with block name must be valid: %v", err)
	}
}

func TestValidateConfigAllowsPicturesBankSourceID(t *testing.T) {
	config := json.RawMessage(`{
		"metadata":{"version":"2.0"},
		"settings":{"columns":1,"rows":1},
		"blocks":[{
			"id":"block","type":"grid",
			"elements":[{
				"id":"image","kind":"image",
				"media_id":"9153ae50-9c6a-4b71-a8de-df458a905d51",
				"source_picture_id":"0ca59ca4-2298-4a70-a37b-a5107e90844e"
			}]
		}]
	}`)

	if err := ValidateConfig(t.Context(), config); err != nil {
		t.Fatalf("config with source_picture_id must be valid: %v", err)
	}
}

func layoutConfig(blockType string, rows, columns int, elements int, withLayout bool) string {
	els := ""
	for i := 0; i < elements; i++ {
		if i > 0 {
			els += ","
		}
		els += fmt.Sprintf(`{"id":"e%d","kind":"text","value":"x"}`, i)
	}
	layout := ""
	if withLayout {
		layout = fmt.Sprintf(`"layout":{"rows":%d,"columns":%d},`, rows, columns)
	}
	answers := ""
	if blockType == BlockTypeSingleChoice {
		answers = `,"answers":[{"element_id":"e0","is_correct":true}]`
	}
	return fmt.Sprintf(`{
		"metadata":{"version":"2.0","title":"t"},
		"settings":{"columns":2,"rows":2},
		"blocks":[{"id":"b1","type":%q,%s"elements":[%s]%s}]
	}`, blockType, layout, els, answers)
}

// TestValidateConfigBlockLayout — сетка принадлежит блоку: у каждого
// задания в наборе своя раскладка, как в Linka Looks. Поле опционально,
// чтобы уже сохранённые наборы без него продолжали проходить.
func TestValidateConfigBlockLayout(t *testing.T) {
	tests := []struct {
		name    string
		config  string
		wantErr string
	}{
		{"grid с layout, элементы помещаются", layoutConfig(BlockTypeGrid, 2, 2, 4, true), ""},
		{"grid с layout, элементов больше сетки", layoutConfig(BlockTypeGrid, 2, 2, 5, true), "exceeds layout capacity"},
		{"grid без layout — старые наборы", layoutConfig(BlockTypeGrid, 0, 0, 1, false), ""},
		{"single_choice с layout, помещается", layoutConfig(BlockTypeSingleChoice, 1, 3, 3, true), ""},
		{"single_choice с layout, переполнение", layoutConfig(BlockTypeSingleChoice, 1, 2, 3, true), "exceeds layout capacity"},
		{"single_choice без layout", layoutConfig(BlockTypeSingleChoice, 0, 0, 3, false), ""},
		{"layout с нулевыми строками отвергается схемой", layoutConfig(BlockTypeGrid, 0, 2, 1, true), "schema validation failed"},
		{"layout больше 100 отвергается схемой", layoutConfig(BlockTypeGrid, 101, 1, 1, true), "schema validation failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateConfig(context.Background(), json.RawMessage(tt.config))
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

// TestValidateConfigRejectsLayoutOnLaneBlocks: у сопоставления и
// категорий своя геометрия, прямоугольная сетка к ним не относится.
func TestValidateConfigRejectsLayoutOnLaneBlocks(t *testing.T) {
	config := `{
		"metadata":{"version":"2.0","title":"t"},
		"settings":{"columns":2,"rows":2},
		"blocks":[{"id":"b1","type":"matching","layout":{"rows":1,"columns":2},
			"elements":[{"id":"a","kind":"text","value":"a"},{"id":"b","kind":"text","value":"b"}],
			"pairs":[{"left_id":"a","right_id":"b"}]}]
	}`
	err := ValidateConfig(context.Background(), json.RawMessage(config))
	if err == nil || !strings.Contains(err.Error(), "layout is not applicable") {
		t.Fatalf("err = %v, want layout rejection for matching", err)
	}
}
