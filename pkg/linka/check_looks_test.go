package linka

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckLooksIgnoresMissingMediaPathsButRejectsUnsupportedBlocks(t *testing.T) {
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

	for _, blockType := range []string{BlockTypeMultiChoice, BlockTypeCategories, BlockTypeSequence} {
		cfg := Config{Blocks: []Block{{ID: "b", Type: blockType}}}
		assert.ErrorIs(t, CheckLooks(cfg), ErrLooksUnsupportedBlock, blockType)
	}
}
