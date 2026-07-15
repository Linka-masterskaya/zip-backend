package linka

import (
	"context"
	"encoding/json"
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
		{"Valid Single Choice", "valid_single_choice.json", false, ""},
		{"Valid Sequence", "valid_sequence.json", false, ""},
		{"Invalid Single (0 correct)", "invalid_single_0.json", true, "requires exactly 1 correct answer"},
		{"Invalid Single (2 correct)", "invalid_single_2.json", true, "requires exactly 1 correct answer"},
		{"Invalid Multi (0 correct)", "invalid_multi_0.json", true, "requires at least 1 correct answer"},
		{"Invalid Sequence (Dup order)", "invalid_seq_dup.json", true, "unique order"},
		{"Invalid Type (Open Answer)", "invalid_open_answer.json", true, "schema validation failed"},
		{"Invalid Settings (Rows > 100)", "invalid_rows_101.json", true, "schema validation failed"},
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
