package linka

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckLooksIgnoresMissingMediaPathsButRejectsUnrepresentableBlocks(t *testing.T) {
	mediaID := uuid.New()
	ok := Config{
		Settings: Settings{Columns: 1, Rows: 1},
		Blocks: []Block{{
			ID: "grid", Type: BlockTypeGrid,
			Elements: []Element{{ID: "e1", Kind: ElementKindNormal, Image: &ElementImage{MediaID: &mediaID}}},
		}},
	}
	require.NoError(t, CheckLooks(ok))
	assert.Empty(t, ok.Blocks[0].Elements[0].Image.MediaURL, "caller config must stay untouched")

	unknown := Config{Blocks: []Block{{ID: "b", Type: "puzzle"}}}
	assert.ErrorIs(t, CheckLooks(unknown), ErrLooksUnsupportedBlock)

	orphan := Config{Blocks: []Block{{
		ID: "b", Type: BlockTypeCategories,
		Elements: []Element{{ID: "a", Kind: ElementKindText, Text: "a"}, {ID: "x", Kind: ElementKindText, Text: "x"}},
		Category: []Category{{ID: "c", Name: "c", Items: []string{"a"}}},
	}}}
	assert.ErrorIs(t, CheckLooks(orphan), ErrLooksUnrepresentableCategories)
}
