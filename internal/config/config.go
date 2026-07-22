// Package config loads application configuration.
package config

import (
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config contains all application settings.
type Config struct {
	App          AppConfig          `mapstructure:"app"`
	DB           DBConfig           `mapstructure:"db"`
	Redis        RedisConfig        `mapstructure:"redis"`
	NATS         NATSConfig         `mapstructure:"nats"`
	MinIO        MinIOConfig        `mapstructure:"minio"`
	JWT          JWTConfig          `mapstructure:"jwt"`
	Yandex       YandexConfig       `mapstructure:"yandex"`
	SMTP         SMTPConfig         `mapstructure:"smtp"`
	Auth         AuthConfig         `mapstructure:"auth"`
	Profile      ProfileConfig      `mapstructure:"profile"`
	CORS         CORSConfig         `mapstructure:"cors"`
	OpenAI       OpenAIConfig       `mapstructure:"openai"`
	PicturesBank PicturesBankConfig `mapstructure:"pictures_bank"`
	Crypto       CryptoConfig       `mapstructure:"crypto"`
	RateLimit    RateLimitConfig    `mapstructure:"rate_limit"`
}

// CryptoConfig contains encryption and hashing settings.
type CryptoConfig struct {
	AESKeyRaw  string `mapstructure:"aes_key"`
	HMACKeyRaw string `mapstructure:"hmac_key"`

	AESKey  []byte `mapstructure:"-"`
	HMACKey []byte `mapstructure:"-"`
}

// AppConfig contains application runtime settings.
type AppConfig struct {
	Env            string   `mapstructure:"env"`
	Port           string   `mapstructure:"port"`
	PublicURL      string   `mapstructure:"public_url"`
	FrontendURL    string   `mapstructure:"frontend_url"`
	MigrationsDir  string   `mapstructure:"migrations_dir"`
	TrustedProxies []string `mapstructure:"trusted_proxies"`
}

// DBConfig contains database connection settings.
type DBConfig struct {
	URL               string        `mapstructure:"url"`
	MaxConns          int32         `mapstructure:"max_conns"`
	MinConns          int32         `mapstructure:"min_conns"`
	MaxConnLifetime   time.Duration `mapstructure:"max_conn_lifetime"`
	HealthCheckPeriod time.Duration `mapstructure:"healthcheck_period"`
}

// RedisConfig contains Redis connection settings.
type RedisConfig struct {
	URL             string        `mapstructure:"url"`
	PoolSize        int           `mapstructure:"pool_size"`
	MinIdleConns    int           `mapstructure:"min_idle_conns"`
	MaxIdleConns    int           `mapstructure:"max_idle_conns"`
	ConnMaxIdleTime time.Duration `mapstructure:"conn_max_idle_time"`
	ConnMaxLifetime time.Duration `mapstructure:"conn_max_lifetime"`
	DialTimeout     time.Duration `mapstructure:"dial_timeout"`
	ReadTimeout     time.Duration `mapstructure:"read_timeout"`
	WriteTimeout    time.Duration `mapstructure:"write_timeout"`
	MaxRetries      int           `mapstructure:"max_retries"`
	MinRetryBackoff time.Duration `mapstructure:"min_retry_backoff"`
	MaxRetryBackoff time.Duration `mapstructure:"max_retry_backoff"`
	ClientName      string        `mapstructure:"client_name"`
}

// NATSConfig contains NATS connection settings.
type NATSConfig struct {
	Connection ConnectionConfig `mapstructure:"connection"`
	Stream     StreamConfig     `mapstructure:"stream"`
	Consumers  ConsumersConfig  `mapstructure:"consumers"`
}

// ConnectionConfig contains NATS connection and reconnect settings.
type ConnectionConfig struct {
	URL                 string        `mapstructure:"url"`
	MaxReconnect        int           `mapstructure:"max_reconnect"`
	PingInterval        time.Duration `mapstructure:"ping_interval"`
	MaxPingsOutstanding int           `mapstructure:"max_pings_outstanding"`
}

// StreamConfig contains JetStream AI_JOBS stream settings.
type StreamConfig struct {
	Name        string        `mapstructure:"name"`
	InitTimeout time.Duration `mapstructure:"init_timeout"`
	MaxAge      time.Duration `mapstructure:"max_age"`
	MaxBytes    int64         `mapstructure:"max_bytes"`
	MaxMsgs     int64         `mapstructure:"max_msgs"`
	Duplicates  time.Duration `mapstructure:"duplicates"`
}

// ConsumersConfig contains settings for all AI job consumers.
type ConsumersConfig struct {
	TTS    ConsumerSettings `mapstructure:"tts"`
	ClamAV ConsumerSettings `mapstructure:"clamav"`
}

// ConsumerSettings contains durable consumer settings for a single job type.
type ConsumerSettings struct {
	Durable      string        `mapstructure:"durable"`
	AckWait      time.Duration `mapstructure:"ack_wait"`
	MaxDeliver   int           `mapstructure:"max_deliver"`
	FetchMaxWait time.Duration `mapstructure:"fetch_max_wait"`
}

// MinIOConfig contains MinIO object storage settings.
type MinIOConfig struct {
	Endpoint  string `mapstructure:"endpoint"`
	AccessKey string `mapstructure:"access_key"`
	SecretKey string `mapstructure:"secret_key"`
	Bucket    string `mapstructure:"bucket"`
	UseSSL    bool   `mapstructure:"use_ssl"`
	Timeout   string `mapstructure:"timeout"`
}

// JWTConfig contains JWT signing and expiration settings.
type JWTConfig struct {
	Secret     string        `mapstructure:"secret"`
	AccessTTL  time.Duration `mapstructure:"access_ttl"`
	RefreshTTL time.Duration `mapstructure:"refresh_ttl"`
}

type RateLimitConfig struct {
	Resend RateLimitRule `mapstructure:"resend"`
	Login  RateLimitRule `mapstructure:"login"`
	Verify RateLimitRule `mapstructure:"verify"`
}

// RateLimitRule describes one rate-limit configuration.
type RateLimitRule struct {
	Scope  string        `mapstructure:"scope"`
	Limit  int64         `mapstructure:"limit"`
	Window time.Duration `mapstructure:"window"`
}

// YandexConfig contains Yandex OAuth settings.
type YandexConfig struct {
	ClientID     string `mapstructure:"client_id"`
	ClientSecret string `mapstructure:"client_secret"`
	RedirectURL  string `mapstructure:"redirect_url"`
}

// OpenAIConfig contains Openai settings.
type OpenAIConfig struct {
	APIKey  string `mapstructure:"api_key"`
	BaseURL string `mapstructure:"base_url"`
	OrgID   string `mapstructure:"org_id"`
}

// PicturesBankConfig contains Pictures Bank settings.
type PicturesBankConfig struct {
	URL string `mapstructure:"url"`
}

// SMTPConfig contains Email settings.
type SMTPConfig struct {
	Host     string        `mapstructure:"host"`
	Port     int           `mapstructure:"port"`
	Username string        `mapstructure:"username"`
	Password string        `mapstructure:"password"`
	From     string        `mapstructure:"from_email"`
	TLS      bool          `mapstructure:"tls"`
	Timeout  time.Duration `mapstructure:"timeout"`
}

// AuthConfig contains authentication and security settings.
type AuthConfig struct {
	AccessTokenTTL           time.Duration `mapstructure:"access_token_ttl"`
	RefreshTokenTTL          time.Duration `mapstructure:"refresh_token_ttl"`
	VerifyEmailTokenTTL      time.Duration `mapstructure:"verify_email_token_ttl"`
	ResetPasswordTokenTTL    time.Duration `mapstructure:"reset_password_token_ttl"`
	EmailChangeTokenTTL      time.Duration `mapstructure:"email_change_token_ttl"`
	BcryptCost               int           `mapstructure:"bcrypt_cost"`
	RequireEmailVerification bool          `mapstructure:"require_email_verification"`
	CookieSecure             bool          `mapstructure:"cookie_secure"`
	LoginRateLimit           int           `mapstructure:"login_rate_limit"`
	PackRateLimit            int           `mapstructure:"pack_rate_limit"`
	ForgotRateLimit          int           `mapstructure:"forgot_rate_limit"`
	ResetRateLimit           int           `mapstructure:"reset_rate_limit"`
	VerifyResendRateLimit    int           `mapstructure:"verify_resend_rate_limit"`
	EmailConfirmRateLimit    int           `mapstructure:"email_confirm_rate_limit"`
}

// ProfileConfig contains Profile settings.
type ProfileConfig struct {
	EmailVerifyTTL        time.Duration `mapstructure:"verify_email_token_ttl"`
	EmailChangeTTL        time.Duration `mapstructure:"email_change_token_ttl"`
	EmailChangeRateLimit  int           `mapstructure:"email_change_rate_limit"`
	EmailConfirmRateLimit int           `mapstructure:"email_confirm_rate_limit"`
}

// CORSConfig contains CORS settings.
type CORSConfig struct {
	AllowOrigins     []string `mapstructure:"allow_origins"`
	AllowMethods     []string `mapstructure:"allow_methods"`
	AllowHeaders     []string `mapstructure:"allow_headers"`
	ExposeHeaders    []string `mapstructure:"expose_headers"`
	AllowCredentials bool     `mapstructure:"allow_credentials"`
	MaxAge           int      `mapstructure:"max_age"`
}

// Load reads configuration from a file and applies environment overrides.
func Load(path string) (*Config, error) {
	v := viper.New()

	v.SetConfigFile(path)
	v.SetConfigType("yaml")

	// Set defaults
	setDefaults(v)

	// Environment variables override YAML keys, for example APP_PORT overrides app.port.
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	// Validate required fields
	if err := validateConfig(&cfg); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}
	return &cfg, nil
}

// setDefaults sets default values for all configuration keys.
func setDefaults(v *viper.Viper) {
	// App defaults
	v.SetDefault("app.env", "dev")
	v.SetDefault("app.port", "8080")
	v.SetDefault("app.public_url", "http://localhost:8080")
	v.SetDefault("app.frontend_url", "http://localhost:3000")
	v.SetDefault("app.migrations_dir", "./migrations")
	v.SetDefault("app.trusted_proxies", []string{})

	// DB defaults
	v.SetDefault("db.max_open_conns", 25)
	v.SetDefault("db.max_idle_conns", 10)
	v.SetDefault("db.conn_max_lifetime", 60)
	v.SetDefault("db.conn_max_idle_time", 30)

	// Redis defaults
	v.SetDefault("redis.url", "redis://localhost:6379/0")
	v.SetDefault("redis.pool_size", 10)
	v.SetDefault("redis.min_idle_conns", 2)
	v.SetDefault("redis.max_idle_conns", 5)
	v.SetDefault("redis.conn_max_idle_time", "5m")
	v.SetDefault("redis.conn_max_lifetime", "1h")
	v.SetDefault("redis.dial_timeout", "5s")
	v.SetDefault("redis.read_timeout", "3s")
	v.SetDefault("redis.write_timeout", "3s")
	v.SetDefault("redis.max_retries", 3)
	v.SetDefault("redis.min_retry_backoff", "8ms")
	v.SetDefault("redis.max_retry_backoff", "512ms")

	// NATS defaults
	v.SetDefault("nats.connection.url", "nats://localhost:4222")
	v.SetDefault("nats.connection.max_reconnect", 5)
	v.SetDefault("nats.connection.ping_interval", "20s")
	v.SetDefault("nats.connection.max_pings_outstanding", 3)

	v.SetDefault("nats.stream.name", "AI_JOBS")
	v.SetDefault("nats.stream.init_timeout", "10s")
	v.SetDefault("nats.stream.max_age", "24h")
	v.SetDefault("nats.stream.max_bytes", 104857600) // 100MB
	v.SetDefault("nats.stream.max_msgs", 100000)
	v.SetDefault("nats.stream.duplicates", "5m")

	v.SetDefault("nats.consumers.tts.ack_wait", "30s")
	v.SetDefault("nats.consumers.tts.max_deliver", 3)
	v.SetDefault("nats.consumers.tts.fetch_max_wait", "5s")

	v.SetDefault("nats.consumers.clamav.ack_wait", "10s")
	v.SetDefault("nats.consumers.clamav.max_deliver", 3)
	v.SetDefault("nats.consumers.clamav.fetch_max_wait", "5s")

	// MinIO defaults
	v.SetDefault("minio.endpoint", "localhost:9000")
	v.SetDefault("minio.access_key", "minioadmin")
	v.SetDefault("minio.secret_key", "minioadmin")
	v.SetDefault("minio.bucket", "linka-media")
	v.SetDefault("minio.use_ssl", false)

	// JWT defaults
	v.SetDefault("jwt.access_ttl", "15m")
	v.SetDefault("jwt.refresh_ttl", "720h")

	// Yandex defaults
	v.SetDefault("yandex.redirect_url", "http://localhost:8080/auth/yandex/callback")

	// OpenAI defaults
	v.SetDefault("openai.base_url", "https://api.openai.com/v1")

	// Pictures Bank defaults
	v.SetDefault("pictures_bank.url", "")

	// SMTP defaults
	v.SetDefault("smtp.host", "smtp.yandex.ru")
	v.SetDefault("smtp.port", 587)
	v.SetDefault("smtp.username", "noreply@yandex.com")
	v.SetDefault("smtp.password", "your-app-password")
	v.SetDefault("smtp.from_email", "noreply@yandex.com")
	v.SetDefault("smtp.tls", true)
	v.SetDefault("smtp.timeout", "10s")

	// Crypto defaults
	v.SetDefault("crypto.aes_key", "")
	v.SetDefault("crypto.hmac_key", "")

	// Auth defaults
	v.SetDefault("auth.bcrypt_cost", 12)
	v.SetDefault("auth.login_rate_limit", 5)
	v.SetDefault("auth.require_email_verification", false)
	v.SetDefault("auth.cookie_secure", false)
	v.SetDefault("auth.pack_rate_limit", 60)
	v.SetDefault("auth.forgot_rate_limit", 3)
	v.SetDefault("auth.reset_rate_limit", 3)
	v.SetDefault("auth.verify_resend_rate_limit", 3)
	v.SetDefault("auth.email_confirm_rate_limit", 10)

	// Profile defaults
	v.SetDefault("profile.verify_email_token_ttl", "24h")
	v.SetDefault("profile.email_change_token_ttl", "24h")
	v.SetDefault("profile.email_change_rate_limit", 3)
	v.SetDefault("profile.email_confirm_rate_limit", 10)

	// CORS defaults
	v.SetDefault("cors.allow_origins", []string{"http://localhost:8080"})
	v.SetDefault("cors.allow_methods", []string{
		"GET",
		"POST",
		"PUT",
		"DELETE",
		"OPTIONS",
		"PATCH",
	})
	v.SetDefault("cors.allow_headers", []string{
		"Content-Type",
		"Content-Length",
		"Accept-Encoding",
		"X-CSRF-Token",
		"Authorization",
		"Accept",
		"Origin",
		"Cache-Control",
		"X-Requested-With",
	})
	v.SetDefault("cors.expose_headers", []string{
		"Content-Length",
		"Content-Type",
		"Date",
		"X-Total-Count",
	})
	v.SetDefault("cors.allow_credentials", true)
	v.SetDefault("cors.max_age", 86400)
}

// validateConfig validates required configuration fields.
func validateConfig(cfg *Config) error {
	// App validation
	if cfg.App.Env == "" {
		return fmt.Errorf("app.env is required")
	}

	// DB validation
	if cfg.DB.URL == "" {
		return fmt.Errorf("db.url is required")
	}

	// Redis validation
	if cfg.Redis.URL == "" {
		return fmt.Errorf("redis.url is required")
	}

	// MinIO validation
	if cfg.MinIO.Endpoint == "" {
		return fmt.Errorf("minio.endpoint is required")
	}
	if cfg.MinIO.AccessKey == "" {
		return fmt.Errorf("minio.access_key is required")
	}
	if cfg.MinIO.SecretKey == "" {
		return fmt.Errorf("minio.secret_key is required")
	}
	if cfg.MinIO.Bucket == "" {
		return fmt.Errorf("minio.bucket is required")
	}

	// JWT validation
	if cfg.JWT.Secret == "" {
		return fmt.Errorf("jwt.secret is required")
	}
	if len(cfg.JWT.Secret) < 32 {
		return fmt.Errorf("jwt.secret must be at least 32 characters")
	}

	aes, err := base64.StdEncoding.DecodeString(cfg.Crypto.AESKeyRaw)
	if err != nil {
		return fmt.Errorf("crypto.aes_key: invalid base64: %w", err)
	}
	if len(aes) != 32 {
		return fmt.Errorf("crypto.aes_key: must be 32 bytes, got %d", len(aes))
	}
	cfg.Crypto.AESKey = aes

	hmacKey, err := base64.StdEncoding.DecodeString(cfg.Crypto.HMACKeyRaw)
	if err != nil {
		return fmt.Errorf("crypto.hmac_key: invalid base64: %w", err)
	}
	if len(hmacKey) < 32 {
		return fmt.Errorf("crypto.hmac_key: must be at least 32 bytes, got %d", len(hmacKey))
	}
	cfg.Crypto.HMACKey = hmacKey
	return nil
}
