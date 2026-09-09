package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadAppliesEnvironmentOverrides(t *testing.T) {
	t.Setenv("APP_ENV", "dev")
	t.Setenv("DB_URL", "postgres://env-user:env-pass@postgres:5432/linka")
	t.Setenv("DB_MIGRATE_URL", "postgres://env-user:env-pass@postgres:5432/linka")
	t.Setenv("REDIS_URL", "redis://redis:6379/0")
	t.Setenv("NATS_CONNECTION_URL", "nats://nats:4222")
	t.Setenv("JWT_SECRET", testCredential("env-jwt"))
	t.Setenv("MINIO_ENDPOINT", "minio:9000")
	t.Setenv("MINIO_ACCESS_KEY", testCredential("env-minio-access"))
	t.Setenv("MINIO_SECRET_KEY", testCredential("env-minio-secret"))
	t.Setenv("CRYPTO_AES_KEY", testAESKey())
	t.Setenv("CRYPTO_HMAC_KEY", testHMACKey())
	t.Setenv("SMTP_USERNAME", "env-smtp@example.test")
	t.Setenv("SMTP_PASSWORD", testCredential("env-smtp"))
	t.Setenv("SMTP_FROM_EMAIL", "env-smtp@example.test")
	t.Setenv("YANDEX_CLIENT_SECRET", testCredential("env-yandex"))
	t.Setenv("OPENAI_API_KEY", testCredential("env-openai"))

	cfg, err := Load("../../config/config.dev.yml")
	require.NoError(t, err)

	assert.Equal(t, "dev", cfg.App.Env)
	assert.Equal(t, "postgres://env-user:env-pass@postgres:5432/linka", cfg.DB.URL)
	assert.Equal(t, "redis://redis:6379/0", cfg.Redis.URL)
	assert.Equal(t, "nats://nats:4222", cfg.NATS.Connection.URL)
	assert.Equal(t, testCredential("env-jwt"), cfg.JWT.Secret)
	assert.Equal(t, "minio:9000", cfg.MinIO.Endpoint)
	assert.Equal(t, testCredential("env-minio-access"), cfg.MinIO.AccessKey)
	assert.Equal(t, testCredential("env-minio-secret"), cfg.MinIO.SecretKey)
	assert.Len(t, cfg.Crypto.AESKey, 32)
	assert.Len(t, cfg.Crypto.HMACKey, 32)
	assert.Equal(t, testCredential("env-smtp"), cfg.SMTP.Password)
	assert.Equal(t, testCredential("env-yandex"), cfg.Yandex.ClientSecret)
	assert.Equal(t, testCredential("env-openai"), cfg.OpenAI.APIKey)
}

func TestLoadMigrationDoesNotValidateRuntimeSecrets(t *testing.T) {
	t.Setenv("APP_ENV", "prod")
	t.Setenv("DB_URL", "postgres://migration:secret@postgres:5432/linka")
	t.Setenv("DB_MIGRATE_URL", "postgres://migration:secret@postgres:5432/linka")

	cfg, err := LoadMigration("../../config/config.prod.yml")
	require.NoError(t, err)
	assert.Equal(t, "prod", cfg.App.Env)
	assert.Equal(t, "postgres://migration:secret@postgres:5432/linka", cfg.DB.MigrateURL)
}

func TestLoadMigrationRejectsUnsafeProductionDatabaseURL(t *testing.T) {
	t.Setenv("APP_ENV", "prod")
	t.Setenv("DB_URL", "postgres://linka:linka@postgres:5432/linka")
	t.Setenv("DB_MIGRATE_URL", "postgres://linka:linka@postgres:5432/linka")

	_, err := LoadMigration("../../config/config.prod.yml")
	require.Error(t, err)
	assert.ErrorContains(t, err, "known development database credentials")
}

func TestLoadMigrationTreatsWhitespacePaddedProdAsProduction(t *testing.T) {
	t.Setenv("APP_ENV", "  PROD  ")
	t.Setenv("DB_URL", "postgres://linka:linka@postgres:5432/linka")
	t.Setenv("DB_MIGRATE_URL", "postgres://linka:linka@postgres:5432/linka")

	_, err := LoadMigration("../../config/config.prod.yml")
	require.Error(t, err)
	assert.ErrorContains(t, err, "known development database credentials")
}
