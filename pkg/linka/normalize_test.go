package linka

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeConfigConvertsFlatImageCard(t *testing.T) {
	raw := json.RawMessage(`{
		"metadata": {"version": "2.0"},
		"settings": {"rows": 1, "columns": 1},
		"blocks": [{
			"id": "c1d2e3a4-1111-4a2b-9c3d-000000000001",
			"type": "grid",
			"layout": {"rows": 1, "columns": 1},
			"elements": [{
				"id": "a988de06-eacd-4a49-b32e-e4883528fcc7",
				"kind": "image",
				"media_id": "e7a7fbbf-8eee-4b0c-9a9b-4ddeea0b715e",
				"source_picture_id": "ab79954b-4f2c-4451-b334-536c4118b39d"
			}]
		}]
	}`)
	require.NoError(t, ValidateConfig(context.Background(), raw))

	normalized, err := NormalizeConfig(raw)

	require.NoError(t, err)
	require.NoError(t, ValidateConfig(context.Background(), normalized))
	var got struct {
		Blocks []struct {
			Layout   map[string]int               `json:"layout"`
			Elements []map[string]json.RawMessage `json:"elements"`
		} `json:"blocks"`
	}
	require.NoError(t, json.Unmarshal(normalized, &got))
	element := got.Blocks[0].Elements[0]
	assert.JSONEq(t, `"normal"`, string(element["kind"]))
	assert.JSONEq(t,
		`{"media_id":"e7a7fbbf-8eee-4b0c-9a9b-4ddeea0b715e","source_picture_id":"ab79954b-4f2c-4451-b334-536c4118b39d"}`,
		string(element["image"]))
	assert.NotContains(t, element, "media_id")
	assert.NotContains(t, element, "source_picture_id")
	assert.Equal(t, map[string]int{"rows": 1, "columns": 1}, got.Blocks[0].Layout)
}

func TestNormalizeConfigKeepsEmptyTaskArrays(t *testing.T) {
	raw := json.RawMessage(`{
		"metadata": {"version": "2.0"},
		"settings": {"rows": 1, "columns": 1},
		"blocks": [{"id": "b1", "type": "single_choice", "elements": [], "answers": []}]
	}`)

	normalized, err := NormalizeConfig(raw)

	require.NoError(t, err)
	assert.JSONEq(t, string(raw), string(normalized))
}
