package app

import (
	"testing"

	"github.com/Linka-masterskaya/zip-backend/internal/config"
	"github.com/Linka-masterskaya/zip-backend/internal/storage"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

func TestBuildPicturesSourceLocalModeDoesNotRequireExternalURLOrLimiter(t *testing.T) {
	in := &infra{
		cfg: &config.Config{
			FeatureFlags: config.FeatureFlagsConfig{LocalBank: true},
			PicturesBank: config.PicturesBankConfig{
				URL:           "",
				MaxImageBytes: 1024,
			},
		},
		db:      &pgxpool.Pool{},
		redis:   nil,
		storage: &storage.Client{},
	}

	source, err := buildPicturesSource(in)

	require.NoError(t, err)
	require.NotNil(t, source)
}
