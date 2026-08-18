package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setValidProductionEnv(t *testing.T) {
	t.Helper()
	t.Setenv("APP_ENV", "prod")
	t.Setenv("DB_URL", fmt.Sprintf(
		"postgres://prod-user:%s@postgres:5432/linka?sslmode=require",
		testCredential("database"),
	))
	t.Setenv("REDIS_URL", fmt.Sprintf(
		"redis://:%s@redis:6379/0",
		testCredential("redis"),
	))
	t.Setenv("MINIO_ENDPOINT", "minio.internal:9000")
	t.Setenv("MINIO_ACCESS_KEY", testCredential("minio-access"))
	t.Setenv("MINIO_SECRET_KEY", testCredential("minio-secret"))
	t.Setenv("JWT_SECRET", testCredential("jwt"))
	t.Setenv("CRYPTO_AES_KEY", testAESKey())
	t.Setenv("CRYPTO_HMAC_KEY", testHMACKey())
	t.Setenv("SMTP_HOST", "smtp.yandex.ru")
	t.Setenv("SMTP_USERNAME", "noreply@example.com")
	t.Setenv("SMTP_PASSWORD", testCredential("smtp"))
	t.Setenv("SMTP_FROM_EMAIL", "noreply@example.com")
	t.Setenv("SMTP_REQUIRE_FROM_MATCH", "true")
}

func TestProductionConfigLoadsOnlyWithSafeEnvironmentOverrides(t *testing.T) {
	setValidProductionEnv(t)

	cfg, err := Load("../../config/config.prod.yml")
	require.NoError(t, err)
	assert.Equal(t, testCredential("minio-access"), cfg.MinIO.AccessKey)
	assert.Equal(t, testCredential("jwt"), cfg.JWT.Secret)
	assert.Len(t, cfg.Crypto.AESKey, 32)
	assert.GreaterOrEqual(t, len(cfg.Crypto.HMACKey), 32)
	assert.True(t, cfg.SMTP.RequireFromMatch)
}

func TestProductionConfigRejectsUnsafeCredentials(t *testing.T) {
	trackedFixture := testCredential("tracked-jwt")
	trackedHash := sha256.Sum256([]byte(trackedFixture))
	trackedFingerprint := hex.EncodeToString(trackedHash[:])
	leakedCredentialFingerprints["jwt.secret"][trackedFingerprint] = struct{}{}
	t.Cleanup(func() {
		delete(leakedCredentialFingerprints["jwt.secret"], trackedFingerprint)
	})

	tests := []struct {
		name    string
		key     string
		value   string
		message string
	}{
		{"placeholder JWT", "JWT_SECRET", "change-me", "forbidden placeholder"},
		{"uppercase placeholder JWT", "JWT_SECRET", "CHANGE-ME", "forbidden placeholder"},
		{"tracked JWT", "JWT_SECRET", trackedFixture, "previously tracked development credential"},
		{"development JWT", "JWT_SECRET", DevJWTSecret, "known development credential"},
		{"development AES", "CRYPTO_AES_KEY", DevAESKeyBase64, "known development credential"},
		{"development HMAC", "CRYPTO_HMAC_KEY", DevHMACKeyBase64, "known development credential"},
		{"development MinIO access", "MINIO_ACCESS_KEY", "minioadmin", "known development credential"},
		{"development MinIO secret", "MINIO_SECRET_KEY", DevMinIOSecretKey, "known development credential"},
		{"development SMTP", "SMTP_PASSWORD", DevSMTPPassword, "known development credential"},
		{"reused AES as HMAC", "CRYPTO_HMAC_KEY", testAESKey(), "must be different"},
		{"empty HMAC", "CRYPTO_HMAC_KEY", "", "required in production"},
		{"database dev credentials", "DB_URL", "postgres://linka:linka@postgres:5432/linka", "known development database credentials"},
		{"database dev password with another user", "DB_URL", "postgres://prod:LINKA@postgres:5432/linka", "known development database credentials"},
		{"database without username", "DB_URL", "postgres://:safe-password@postgres:5432/linka", "host and username"},
		{"database without name", "DB_URL", "postgres://prod:safe-password@postgres:5432", "database name"},
		{"redis without password", "REDIS_URL", "redis://redis:6379/0", "password"},
		{"redis without host", "REDIS_URL", "redis://:safe-password@/0", "host and password"},
		{"SMTP sender mismatch", "SMTP_FROM_EMAIL", "other@example.com", "must match smtp.username"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setValidProductionEnv(t)
			t.Setenv(tt.key, tt.value)
			_, err := Load("../../config/config.prod.yml")
			require.Error(t, err)
			assert.ErrorContains(t, err, tt.message)
			if tt.value != "" {
				assert.NotContains(t, err.Error(), tt.value, "validation errors must not expose secret values")
			}
		})
	}
}

func testCredential(label string) string {
	return "test-only-" + label + "-" + strings.Repeat("x", 32)
}

func testAESKey() string {
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x31}, 32))
}

func testHMACKey() string {
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))
}

func TestLeakedCredentialFingerprintInventory(t *testing.T) {
	expectedSource := map[string][]string{
		"jwt.secret": {
			"32764387e4bc263f87e102ca57010c55ca8b3d9cb9fb0d809696a6f680e4384d",
			"f16dd3036e8e542ced86fe8bc25d7d1f6c4b1622f0321069c17600c12b59415b",
		},
		"crypto.aes_key": {
			"861009ec4d599fab1f40abc76e6f89880cff5833c79c548c99f9045f191cd90b",
			"cbee3ab7885dfe98049d29d2d92bcb669ce3b37fa9b0f6e46a7a832772332b25",
			"2cf5e6ec387461b4bf954f587ad4d957753fcbc48bf892b5e49996b90cf3b476",
		},
		"crypto.hmac_key": {
			"9a17bb0290ef0facc1b39174d8ebf96578a99f2d091d019eef3ecfaebf40fd6b",
			"f6d527e6d01865481134f29788be2afe7fc3c702e1a55d7ceafac5f35199e8dc",
			"2cf5e6ec387461b4bf954f587ad4d957753fcbc48bf892b5e49996b90cf3b476",
		},
	}
	for name, fingerprints := range expectedSource {
		for _, fingerprint := range fingerprints {
			_, found := leakedCredentialFingerprints[name][fingerprint]
			assert.True(t, found, "source:"+name+":"+fingerprint)
		}
	}

	expectedMaterial := map[string][]string{
		"crypto.aes_key": {
			"861009ec4d599fab1f40abc76e6f89880cff5833c79c548c99f9045f191cd90b",
			"5f2560c1d6160f95c48ec63ef391d6993b70ceec9e2d9ad68dbab6286115bf0b",
			"3eb1bd439947eb762998e566ccc2e099c791118b2f40579cc4f7da2b5061b7f9",
		},
		"crypto.hmac_key": {
			"f6d527e6d01865481134f29788be2afe7fc3c702e1a55d7ceafac5f35199e8dc",
			"23d328bdaf8da8b816c41b4a70f0f178468fd6c2a66990ee2f083b2496eabf52",
			"3eb1bd439947eb762998e566ccc2e099c791118b2f40579cc4f7da2b5061b7f9",
		},
	}
	for name, fingerprints := range expectedMaterial {
		for _, fingerprint := range fingerprints {
			_, found := leakedCryptoMaterialFingerprints[name][fingerprint]
			assert.True(t, found, "material:"+name+":"+fingerprint)
		}
	}
}

func TestProductionEnvironmentWithWhitespaceStillFailsFast(t *testing.T) {
	setValidProductionEnv(t)
	t.Setenv("APP_ENV", "  PrOd  ")
	t.Setenv("JWT_SECRET", "change-me")

	_, err := Load("../../config/config.prod.yml")
	require.Error(t, err)
	assert.ErrorContains(t, err, "forbidden placeholder")
}

func TestProductionEnvironmentIsTrimmedAfterLoading(t *testing.T) {
	setValidProductionEnv(t)
	t.Setenv("APP_ENV", "  PROD  ")

	cfg, err := Load("../../config/config.prod.yml")
	require.NoError(t, err)
	assert.Equal(t, "PROD", cfg.App.Env)
}

func TestProductionCryptoKeysRejectSameDecodedBytes(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x5a}, 32))

	_, _, err := ValidateProductionCryptoKeys(key, "  "+key+"  ")
	require.Error(t, err)
	assert.ErrorContains(t, err, "must be different")
}

func TestProductionCryptoKeysRejectTrackedFingerprint(t *testing.T) {
	trackedAES := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x6a}, 32))
	trackedHash := sha256.Sum256([]byte(trackedAES))
	trackedFingerprint := hex.EncodeToString(trackedHash[:])
	leakedCredentialFingerprints["crypto.aes_key"][trackedFingerprint] = struct{}{}
	t.Cleanup(func() {
		delete(leakedCredentialFingerprints["crypto.aes_key"], trackedFingerprint)
	})

	_, _, err := ValidateProductionCryptoKeys(trackedAES, testHMACKey())
	require.Error(t, err)
	assert.ErrorContains(t, err, "previously tracked development credential")
	assert.NotContains(t, err.Error(), trackedAES)
}

func TestProductionCryptoKeysRejectLegacySharedTrackedCredential(t *testing.T) {
	legacy := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("0123456789abcdef"), 2))

	_, _, err := ValidateProductionCryptoKeys(legacy, testHMACKey())
	require.Error(t, err)
	assert.ErrorContains(t, err, "previously tracked development credential")

	_, _, err = ValidateProductionCryptoKeys(testAESKey(), legacy)
	require.Error(t, err)
	assert.ErrorContains(t, err, "previously tracked development credential")
}

func TestProductionCryptoKeysRejectHistoricalRawKeyMaterialAfterBase64Encoding(t *testing.T) {
	legacyAESMaterial := []byte(strings.Repeat("0123456789", 3) + "01")
	legacyHMACMaterial := []byte("abcdefghijklmnopqrstuvwxyz" + "123456")
	legacyAES := base64.StdEncoding.EncodeToString(legacyAESMaterial)
	legacyHMAC := base64.StdEncoding.EncodeToString(legacyHMACMaterial)

	_, _, err := ValidateProductionCryptoKeys(legacyAES, testHMACKey())
	require.Error(t, err)
	assert.ErrorContains(t, err, "previously tracked key material")
	assert.NotContains(t, err.Error(), legacyAES)

	_, _, err = ValidateProductionCryptoKeys(testAESKey(), legacyHMAC)
	require.Error(t, err)
	assert.ErrorContains(t, err, "previously tracked key material")
	assert.NotContains(t, err.Error(), legacyHMAC)
}

func TestProductionCryptoKeysRejectNonCanonicalBase64(t *testing.T) {
	aesKey := testAESKey()
	nonCanonicalHMAC := strings.TrimRight(testHMACKey(), "=")

	_, _, err := ValidateProductionCryptoKeys(aesKey, nonCanonicalHMAC)
	require.Error(t, err)
	assert.ErrorContains(t, err, "invalid base64")
}
