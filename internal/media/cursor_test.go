package media

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCursorRoundTrips(t *testing.T) {
	original := mediaCursor{
		CreatedAt: time.Date(2026, 8, 1, 12, 30, 0, 123456000, time.UTC),
		ID:        uuid.New(),
	}
	decoded, err := decodeCursor(encodeCursor(original))
	require.NoError(t, err)
	assert.True(t, original.CreatedAt.Equal(decoded.CreatedAt))
	assert.Equal(t, original.ID, decoded.ID)
}

func TestDecodeCursorRejectsMalformedInput(t *testing.T) {
	for _, value := range []string{
		"not-base64!!!",
		"bm8tY29tbWE",         // base64("no-comma"), missing the "," separator
		"MjAyNi0wOC0wMSxub3Q", // base64("2026-08-01,not"), invalid uuid
	} {
		_, err := decodeCursor(value)
		assert.Error(t, err, value)
	}
}
