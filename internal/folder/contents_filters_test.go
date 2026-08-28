package folder

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateContentsFilters(t *testing.T) {
	t.Run("trims values", func(t *testing.T) {
		input := ContentsInput{Query: "  азбука ", Type: " pack ", Difficulty: " easy "}
		require.NoError(t, validateContentsFilters(&input))
		assert.Equal(t, "азбука", input.Query)
		assert.Equal(t, "pack", input.Type)
		assert.Equal(t, "easy", input.Difficulty)
	})

	t.Run("rejects unknown type", func(t *testing.T) {
		input := ContentsInput{Type: "student"}
		require.Error(t, validateContentsFilters(&input))
	})

	t.Run("rejects age out of range", func(t *testing.T) {
		age := 2
		input := ContentsInput{Age: &age}
		require.Error(t, validateContentsFilters(&input))
	})

	t.Run("rejects unknown difficulty", func(t *testing.T) {
		input := ContentsInput{Difficulty: "impossible"}
		require.Error(t, validateContentsFilters(&input))
	})

	t.Run("accepts empty filters", func(t *testing.T) {
		input := ContentsInput{}
		require.NoError(t, validateContentsFilters(&input))
	})
}
