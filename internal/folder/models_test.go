package folder

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContentItemMarshalJSONMetadataByType(t *testing.T) {
	id := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	updatedAt := time.Date(2026, time.August, 29, 1, 2, 3, 0, time.UTC)
	age, difficulty, kind := 5, "medium", KindFolder

	tests := []struct {
		name     string
		item     ContentItem
		expected string
	}{
		{
			name: "pack with metadata",
			item: ContentItem{
				Type: "pack", ID: id, Name: "Filled", Published: true,
				Age: &age, Difficulty: &difficulty, UpdatedAt: updatedAt,
			},
			expected: `{
				"type":"pack","id":"00000000-0000-0000-0000-000000000001",
				"name":"Filled","published":true,"age":5,"difficulty":"medium",
				"updated_at":"2026-08-29T01:02:03Z"
			}`,
		},
		{
			name: "pack without metadata",
			item: ContentItem{
				Type: "pack", ID: id, Name: "Empty", UpdatedAt: updatedAt,
			},
			expected: `{
				"type":"pack","id":"00000000-0000-0000-0000-000000000001",
				"name":"Empty","age":null,"difficulty":null,
				"updated_at":"2026-08-29T01:02:03Z"
			}`,
		},
		{
			name: "folder omits pack metadata",
			item: ContentItem{
				Type: "folder", ID: id, Name: "Folder", Kind: &kind,
				Age: &age, Difficulty: &difficulty, UpdatedAt: updatedAt,
			},
			expected: `{
				"type":"folder","id":"00000000-0000-0000-0000-000000000001",
				"name":"Folder","kind":"folder",
				"updated_at":"2026-08-29T01:02:03Z"
			}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual, err := json.Marshal(tt.item)
			require.NoError(t, err)
			assert.JSONEq(t, tt.expected, string(actual))
		})
	}
}
