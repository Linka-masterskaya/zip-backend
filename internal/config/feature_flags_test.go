package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFeatureLocalBankIsReadOnlyFromConfig(t *testing.T) {
	t.Setenv("FEATURE_LOCAL_BANK", "true")
	t.Setenv("FEATURE_FLAGS_LOCAL_BANK", "true")
	cfg, err := Load("../../config/config.dev.yml")
	require.NoError(t, err)
	assert.False(t, cfg.FeatureFlags.LocalBank)
}
