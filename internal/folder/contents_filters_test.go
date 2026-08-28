package folder

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateContentsFilters(t *testing.T) {
	t.Run("значения обрезаются", func(t *testing.T) {
		input := ContentsInput{Query: "  азбука ", Type: " pack ", Difficulty: " easy "}
		require.NoError(t, validateContentsFilters(&input))
		assert.Equal(t, "азбука", input.Query)
		assert.Equal(t, "pack", input.Type)
		assert.Equal(t, "easy", input.Difficulty)
	})

	t.Run("неизвестный тип", func(t *testing.T) {
		input := ContentsInput{Type: "student"}
		require.Error(t, validateContentsFilters(&input))
	})

	t.Run("возраст вне диапазона", func(t *testing.T) {
		age := 2
		input := ContentsInput{Age: &age}
		require.Error(t, validateContentsFilters(&input))
	})

	t.Run("неизвестная сложность", func(t *testing.T) {
		input := ContentsInput{Difficulty: "impossible"}
		require.Error(t, validateContentsFilters(&input))
	})

	t.Run("пустые фильтры допустимы", func(t *testing.T) {
		input := ContentsInput{}
		require.NoError(t, validateContentsFilters(&input))
	})
}
