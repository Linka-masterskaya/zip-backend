package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadAppliesEnvironmentOverrides(t *testing.T) {
	t.Setenv("APP_ENV", "prod")
	t.Setenv("DB_URL", "postgres://env-user:env-pass@postgres:5432/linka")
	t.Setenv("REDIS_URL", "redis://redis:6379/0")
	t.Setenv("NATS_CONNECTION_URL", "nats://nats:4222")
	t.Setenv("JWT_SECRET", "environment-secret-that-is-at-least-32-bytes")
	t.Setenv("MINIO_ENDPOINT", "minio:9000")

	cfg, err := Load("../../config/config.dev.yml")
	require.NoError(t, err)

	assert.Equal(t, "prod", cfg.App.Env)
	assert.Equal(t, "postgres://env-user:env-pass@postgres:5432/linka", cfg.DB.URL)
	assert.Equal(t, "redis://redis:6379/0", cfg.Redis.URL)
	assert.Equal(t, "nats://nats:4222", cfg.NATS.Connection.URL)
	assert.Equal(t, "environment-secret-that-is-at-least-32-bytes", cfg.JWT.Secret)
	assert.Equal(t, "minio:9000", cfg.MinIO.Endpoint)
}

func TestLoadMigrationDoesNotValidateRuntimeSecrets(t *testing.T) {
	t.Setenv("APP_ENV", "prod")
	t.Setenv("DB_URL", "postgres://migration:secret@postgres:5432/linka")

	cfg, err := LoadMigration("../../config/config.prod.yml")
	require.NoError(t, err)
	assert.Equal(t, "prod", cfg.App.Env)
	assert.Equal(t, "postgres://migration:secret@postgres:5432/linka", cfg.DB.URL)
}
