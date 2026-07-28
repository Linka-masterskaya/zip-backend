package picturebank

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewSourceKeepsUnimplementedLocalModeFailClosed(t *testing.T) {
	cfg := testPicturesConfig()
	cfg.URL = "https://pictures.example.test"

	_, err := NewSource(true, cfg, nil)
	require.ErrorIs(t, err, ErrLocalBankNotImplemented)

	_, err = NewSource(false, cfg, nil)
	require.ErrorContains(t, err, "distributed limiter is required")
}
