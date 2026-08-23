package picturebank

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewSourceLocalModeDoesNotRequireExternalConfiguration(t *testing.T) {
	cfg := testPicturesConfig()
	cfg.URL = ""

	source, err := NewSource(true, cfg, nil, LocalDependencies{
		DB:      &pgxpool.Pool{},
		Storage: staticLocalStorage{data: []byte("unused")},
	})

	require.NoError(t, err)
	assert.IsType(t, &localSource{}, source)
}

func TestNewSourceExternalModeStillRequiresExternalDependencies(t *testing.T) {
	cfg := testPicturesConfig()
	cfg.URL = "https://pictures.example.test"

	_, err := NewSource(false, cfg, nil)

	require.ErrorContains(t, err, "distributed limiter is required")
}

func TestNewSourceLocalModeRequiresOnlyLocalDependencies(t *testing.T) {
	cfg := testPicturesConfig()
	cfg.URL = "not-an-external-url"

	_, err := NewSource(true, cfg, nil)
	require.ErrorContains(t, err, "local pictures bank dependencies are required")

	_, err = NewSource(true, cfg, nil, LocalDependencies{})
	require.ErrorContains(t, err, "local pictures bank database is required")
}

type staticLocalStorage struct {
	data []byte
}

func (s staticLocalStorage) GetObject(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(string(s.data))), nil
}
