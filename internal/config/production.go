package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/mail"
	"net/url"
	"strings"
)

const (
	DevJWTSecret        = "dev-only-jwt-secret-do-not-use-in-production-2026" //nolint:gosec // Documented development fixture rejected in production.
	DevAESKeyBase64     = "ZGV2LW9ubHktYWVzLWtleS1ub3QtZm9yLXByb2QhISE="
	DevHMACKeyBase64    = "ZGV2LW9ubHktaG1hYy1rZXktbm90LWZvci1wcm9kISE="
	DevMinIOAccessKey   = "linka-dev-minio"
	DevMinIOSecretKey   = "linka-dev-minio-secret" //nolint:gosec // Documented development fixture rejected in production.
	DevPostgresPassword = "linka"
	DevRedisPassword    = "pass"
	DevSMTPPassword     = "dev-only-smtp-password" //nolint:gosec // Documented development fixture rejected in production.
)

var leakedCredentialFingerprints = map[string]map[string]struct{}{
	"jwt.secret": {
		"f16dd3036e8e542ced86fe8bc25d7d1f6c4b1622f0321069c17600c12b59415b": {},
	},
	"crypto.aes_key": {
		"861009ec4d599fab1f40abc76e6f89880cff5833c79c548c99f9045f191cd90b": {},
		"2cf5e6ec387461b4bf954f587ad4d957753fcbc48bf892b5e49996b90cf3b476": {},
	},
	"crypto.hmac_key": {
		"f6d527e6d01865481134f29788be2afe7fc3c702e1a55d7ceafac5f35199e8dc": {},
		"2cf5e6ec387461b4bf954f587ad4d957753fcbc48bf892b5e49996b90cf3b476": {},
	},
}

// leakedCryptoMaterialFingerprints contains SHA-256 fingerprints of the
// decoded AES/HMAC bytes. This is intentionally separate from fingerprints of
// tracked text representations: converting an old raw key to canonical base64
// changes the source string but not the compromised key material.
var leakedCryptoMaterialFingerprints = map[string]map[string]struct{}{
	"crypto.aes_key": {
		"861009ec4d599fab1f40abc76e6f89880cff5833c79c548c99f9045f191cd90b": {},
		"5f2560c1d6160f95c48ec63ef391d6993b70ceec9e2d9ad68dbab6286115bf0b": {},
		"3eb1bd439947eb762998e566ccc2e099c791118b2f40579cc4f7da2b5061b7f9": {},
	},
	"crypto.hmac_key": {
		"f6d527e6d01865481134f29788be2afe7fc3c702e1a55d7ceafac5f35199e8dc": {},
		"23d328bdaf8da8b816c41b4a70f0f178468fd6c2a66990ee2f083b2496eabf52": {},
		"3eb1bd439947eb762998e566ccc2e099c791118b2f40579cc4f7da2b5061b7f9": {},
	},
}

func isProductionEnvironment(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), "prod")
}

func validateProductionConfig(cfg *Config) error {
	checks := []struct {
		name  string
		value string
		dev   []string
	}{
		{"db.url", cfg.DB.URL, nil},
		{"redis.url", cfg.Redis.URL, nil},
		{"minio.endpoint", cfg.MinIO.Endpoint, nil},
		{"minio.access_key", cfg.MinIO.AccessKey, []string{"minioadmin", DevMinIOAccessKey}},
		{"minio.secret_key", cfg.MinIO.SecretKey, []string{"minioadmin", DevMinIOSecretKey}},
		{"jwt.secret", cfg.JWT.Secret, []string{DevJWTSecret}},
		{"smtp.host", cfg.SMTP.Host, nil},
		{"smtp.username", cfg.SMTP.Username, nil},
		{"smtp.password", cfg.SMTP.Password, []string{DevSMTPPassword}},
		{"smtp.from_email", cfg.SMTP.From, nil},
	}
	for _, check := range checks {
		if err := validateProductionCredential(check.name, check.value, check.dev...); err != nil {
			return err
		}
	}
	aesKey, hmacKey, err := ValidateProductionCryptoKeys(cfg.Crypto.AESKeyRaw, cfg.Crypto.HMACKeyRaw)
	if err != nil {
		return err
	}
	cfg.Crypto.AESKey = aesKey
	cfg.Crypto.HMACKey = hmacKey

	if err := validatePostgresProductionURL(cfg.DB.URL); err != nil {
		return err
	}
	if err := validateRedisProductionURL(cfg.Redis.URL); err != nil {
		return err
	}
	if err := validateOptionalProductionCredential("yandex.client_secret", cfg.Yandex.ClientSecret); err != nil {
		return err
	}
	if err := validateOptionalProductionCredential("openai.api_key", cfg.OpenAI.APIKey); err != nil {
		return err
	}
	if smtpRequiresMatchingSender(cfg.SMTP) && !sameMailbox(cfg.SMTP.Username, cfg.SMTP.From) {
		return fmt.Errorf("smtp.from_email must match smtp.username for provider %q", cfg.SMTP.Host)
	}
	return nil
}

// ValidateProductionCryptoKeys validates a prospective production AES/HMAC
// pair and returns decoded key bytes. It is shared by normal startup and the
// rotation utility so a rotation cannot commit data under credentials that the
// application would reject on its next start.
func ValidateProductionCryptoKeys(aesRaw, hmacRaw string) ([]byte, []byte, error) {
	if err := validateProductionCredential("crypto.aes_key", aesRaw, DevAESKeyBase64); err != nil {
		return nil, nil, err
	}
	if err := validateProductionCredential("crypto.hmac_key", hmacRaw, DevHMACKeyBase64); err != nil {
		return nil, nil, err
	}

	aesKey, err := decodeCryptoKey("crypto.aes_key", aesRaw, 32, true)
	if err != nil {
		return nil, nil, err
	}
	if err := validateLeakedCryptoMaterial("crypto.aes_key", aesKey); err != nil {
		return nil, nil, err
	}
	hmacKey, err := decodeCryptoKey("crypto.hmac_key", hmacRaw, 32, false)
	if err != nil {
		return nil, nil, err
	}
	if err := validateLeakedCryptoMaterial("crypto.hmac_key", hmacKey); err != nil {
		return nil, nil, err
	}
	if bytes.Equal(aesKey, hmacKey) {
		return nil, nil, fmt.Errorf("crypto.aes_key and crypto.hmac_key must be different in production")
	}
	return aesKey, hmacKey, nil
}

func decodeConfiguredCryptoKeys(aesRaw, hmacRaw string) ([]byte, []byte, error) {
	aesKey, err := decodeCryptoKey("crypto.aes_key", aesRaw, 32, true)
	if err != nil {
		return nil, nil, err
	}
	hmacKey, err := decodeCryptoKey("crypto.hmac_key", hmacRaw, 32, false)
	if err != nil {
		return nil, nil, err
	}
	return aesKey, hmacKey, nil
}

func decodeCryptoKey(name, raw string, minimum int, exact bool) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	decoded, err := base64.StdEncoding.Strict().DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: invalid base64", name)
	}
	if base64.StdEncoding.EncodeToString(decoded) != raw {
		return nil, fmt.Errorf("%s: must use canonical base64 encoding", name)
	}
	if exact && len(decoded) != minimum {
		return nil, fmt.Errorf("%s: must be exactly %d bytes", name, minimum)
	}
	if !exact && len(decoded) < minimum {
		return nil, fmt.Errorf("%s: must be at least %d bytes", name, minimum)
	}
	return decoded, nil
}

func validateProductionCredential(name, value string, devValues ...string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("%s is required in production", name)
	}
	if isPlaceholder(value) {
		return fmt.Errorf("%s uses a forbidden placeholder", name)
	}
	for _, devValue := range devValues {
		if value == devValue {
			return fmt.Errorf("%s uses a known development credential", name)
		}
	}
	if fingerprints, ok := leakedCredentialFingerprints[name]; ok {
		sum := sha256.Sum256([]byte(value))
		if _, found := fingerprints[hex.EncodeToString(sum[:])]; found {
			return fmt.Errorf("%s uses a previously tracked development credential", name)
		}
	}
	return nil
}

func validateLeakedCryptoMaterial(name string, key []byte) error {
	fingerprints, ok := leakedCryptoMaterialFingerprints[name]
	if !ok {
		return nil
	}
	sum := sha256.Sum256(key)
	if _, found := fingerprints[hex.EncodeToString(sum[:])]; found {
		return fmt.Errorf("%s uses previously tracked key material", name)
	}
	return nil
}

func validateOptionalProductionCredential(name, value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return validateProductionCredential(name, value)
}

func isPlaceholder(value string) bool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	placeholders := map[string]struct{}{
		"change-me": {}, "changeme": {}, "your-app-password": {},
		"replace-me": {}, "replace_with_secret": {}, "password": {},
	}
	_, found := placeholders[normalized]
	return found
}

func validatePostgresProductionURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") {
		return fmt.Errorf("db.url is invalid")
	}
	if parsed.Hostname() == "" || parsed.User == nil || strings.TrimSpace(parsed.User.Username()) == "" {
		return fmt.Errorf("db.url must contain a database host and username in production")
	}
	password, hasPassword := parsed.User.Password()
	if !hasPassword || password == "" {
		return fmt.Errorf("db.url must contain a database password in production")
	}
	database := strings.TrimPrefix(parsed.EscapedPath(), "/")
	if database == "" || strings.Contains(database, "/") {
		return fmt.Errorf("db.url must contain a database name in production")
	}
	if strings.EqualFold(password, DevPostgresPassword) {
		return fmt.Errorf("db.url uses known development database credentials")
	}
	if isPlaceholder(password) {
		return fmt.Errorf("db.url uses a forbidden placeholder password")
	}
	return nil
}

func validateRedisProductionURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "redis" && parsed.Scheme != "rediss") {
		return fmt.Errorf("redis.url is invalid")
	}
	if parsed.Hostname() == "" || parsed.User == nil {
		return fmt.Errorf("redis.url must contain a host and password in production")
	}
	password, hasPassword := parsed.User.Password()
	if !hasPassword || password == "" {
		return fmt.Errorf("redis.url must contain a password in production")
	}
	if strings.EqualFold(password, DevRedisPassword) || isPlaceholder(password) {
		return fmt.Errorf("redis.url uses a known development or placeholder password")
	}
	return nil
}

func smtpRequiresMatchingSender(cfg SMTPConfig) bool {
	if cfg.RequireFromMatch {
		return true
	}
	host := strings.ToLower(strings.TrimSpace(cfg.Host))
	return strings.Contains(host, "yandex.") || host == "smtp.gmail.com" || strings.HasSuffix(host, ".mail.ru")
}

func sameMailbox(username, from string) bool {
	usernameAddress, usernameErr := mail.ParseAddress(username)
	fromAddress, fromErr := mail.ParseAddress(from)
	if usernameErr != nil || fromErr != nil {
		return strings.EqualFold(strings.TrimSpace(username), strings.TrimSpace(from))
	}
	return strings.EqualFold(usernameAddress.Address, fromAddress.Address)
}
