package student

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeCreateDefaultsCardsShift(t *testing.T) {
	input := CreateInput{Email: "a@b.c", Name: "Аня"}
	normalizeCreate(&input)
	require.NotNil(t, input.CardsShift)
	assert.Equal(t, "full", *input.CardsShift)
}

// TestPrepareUpdateCardsShift: значение проверяется по белому списку,
// null превращается в значение по умолчанию, отсутствие — в «не трогать».
func TestPrepareUpdateCardsShift(t *testing.T) {
	service := &Service{}

	t.Run("допустимое значение", func(t *testing.T) {
		value := "left"
		result, err := service.prepareUpdate(UpdateInput{
			CardsShift: nullableField[string]{Set: true, Value: &value},
		})
		require.NoError(t, err)
		require.NotNil(t, result.CardsShift)
		assert.Equal(t, "left", *result.CardsShift)
	})

	t.Run("null сбрасывает к full", func(t *testing.T) {
		result, err := service.prepareUpdate(UpdateInput{
			CardsShift: nullableField[string]{Set: true},
		})
		require.NoError(t, err)
		require.NotNil(t, result.CardsShift)
		assert.Equal(t, "full", *result.CardsShift)
	})

	t.Run("недопустимое значение — 400", func(t *testing.T) {
		value := "center"
		_, err := service.prepareUpdate(UpdateInput{
			CardsShift: nullableField[string]{Set: true, Value: &value},
		})
		assertStudentStatus(t, err, 400)
	})

	t.Run("поле не передано", func(t *testing.T) {
		name := "Аня"
		result, err := service.prepareUpdate(UpdateInput{Name: &name})
		require.NoError(t, err)
		assert.Nil(t, result.CardsShift)
	})
}
