// Package config loads application configuration.
package config

import (
	"fmt"
	"net/url"
	"os"
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
	FeatureFlags FeatureFlagsConfig `mapstructure:"feature_flags"`
	Crypto       CryptoConfig       `mapstructure:"crypto"`
	RateLimit    RateLimitConfig    `mapstructure:"rate_limit"`
	Server       ServerConfig       `mapstructure:"server"`
	TTS          TTSConfig          `mapstructure:"ttsapi"`
	Cron         CronConfig         `mapstructure:"cron"`
	Media        MediaConfig        `mapstructure:"media"`
}

// MigrationConfig contains only the settings required by the migration binary.
// Keeping it separate prevents migrations from depending on unrelated runtime
// secrets such as JWT, MinIO and SMTP credentials.
type MigrationConfig struct {
	App AppConfig `mapstructure:"app"`
	DB  DBConfig  `mapstructure:"db"`
}

// ServerConfig contains HTTP server ports and timeouts.
type ServerConfig struct {
	MetricsPort         string        `mapstructure:"metrics_port"`
	ReadTimeout         time.Duration `mapstructure:"read_timeout"`
	WriteTimeout        time.Duration `mapstructure:"write_timeout"`
	IdleTimeout         time.Duration `mapstructure:"idle_timeout"`
	MetricsReadTimeout  time.Duration `mapstructure:"metrics_read_timeout"`
	MetricsWriteTimeout time.Duration `mapstructure:"metrics_write_timeout"`
	ShutdownTimeout     time.Duration `mapstructure:"shutdown_timeout"`
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
	DocsEnabled    bool     `mapstructure:"docs_enabled"`
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

// RateLimitConfig describes rate-limit configuration.
type RateLimitConfig struct {
	Resend RateLimitRule `mapstructure:"resend"`
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
	URL               string        `mapstructure:"url"`
	Timeout           time.Duration `mapstructure:"timeout"`
	RequestsPerSecond int64         `mapstructure:"requests_per_second"`
	InboundPerMinute  int64         `mapstructure:"inbound_per_minute"`
	MaxConcurrent     int           `mapstructure:"max_concurrent"`
	CacheTTL          time.Duration `mapstructure:"cache_ttl"`
	MaxMetadataBytes  int64         `mapstructure:"max_metadata_bytes"`
	MaxImageBytes     int64         `mapstructure:"max_image_bytes"`
}

// FeatureFlagsConfig controls optional runtime behavior.
type FeatureFlagsConfig struct {
	LocalBank bool `mapstructure:"local_bank"`
}

// SMTPConfig contains Email settings.
type SMTPConfig struct {
	Host             string        `mapstructure:"host"`
	Port             int           `mapstructure:"port"`
	Username         string        `mapstructure:"username"`
	Password         string        `mapstructure:"password"`
	From             string        `mapstructure:"from_email"`
	TLS              bool          `mapstructure:"tls"`
	Timeout          time.Duration `mapstructure:"timeout"`
	RequireFromMatch bool          `mapstructure:"require_from_match"`
	DailyLimit       int           `mapstructure:"daily_limit_alert"`
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
	// UnverifiedRetention — сколько живёт регистрация без подтверждения адреса.
	// Ноль отключает уборку.
	UnverifiedRetention   time.Duration `mapstructure:"unverified_retention"`
	CookieSecure          bool          `mapstructure:"cookie_secure"`
	LoginRateLimit        int           `mapstructure:"login_rate_limit"`
	RegisterRateLimit     int           `mapstructure:"register_rate_limit"`
	RefreshRateLimit      int           `mapstructure:"refresh_rate_limit"`
	PackRateLimit         int           `mapstructure:"pack_rate_limit"`
	ForgotRateLimit       int           `mapstructure:"forgot_rate_limit"`
	ResetRateLimit        int           `mapstructure:"reset_rate_limit"`
	VerifyResendRateLimit int           `mapstructure:"verify_resend_rate_limit"`
	EmailConfirmRateLimit int           `mapstructure:"email_confirm_rate_limit"`
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
	AllowOrigins     []string      `mapstructure:"allow_origins"`
	AllowMethods     []string      `mapstructure:"allow_methods"`
	AllowHeaders     []string      `mapstructure:"allow_headers"`
	ExposeHeaders    []string      `mapstructure:"expose_headers"`
	AllowCredentials bool          `mapstructure:"allow_credentials"`
	MaxAge           time.Duration `mapstructure:"max_age"`
}

// TTSConfig contains TTS settings.
type TTSConfig struct {
	ServiceURL    string        `mapstructure:"service_url"`
	Timeout       time.Duration `mapstructure:"timeout"`
	MaxConcurrent int           `mapstructure:"max_concurrent"`
	RateLimit     int           `mapstructure:"rate_limit"`
	MaxTextLen    int           `mapstructure:"max_text_len"`
	MaxBodySize   int64         `mapstructure:"max_body_size"`
	MimeType      string        `mapstructure:"mime_type"`
}

// CronConfig contains scheduled task settings.
type CronConfig struct {
	VoiceRefresh VoiceRefreshCron `mapstructure:"voice_refresh"`
	TTSCleanup   TTSCleanupCron   `mapstructure:"tts_cleanup"`
}

// VoiceRefreshCron contains voice cache refresh settings.
type VoiceRefreshCron struct {
	Interval time.Duration `mapstructure:"interval"`
}

// TTSCleanupCron contains TTS cleanup job settings.
type TTSCleanupCron struct {
	Interval    time.Duration `mapstructure:"interval"`
	CleanPeriod time.Duration `mapstructure:"clean_period"`
	JobsTTL     time.Duration `mapstructure:"jobs_ttl"`
	Limit       int           `mapstructure:"limit"`
}

// Load reads application settings from a configuration file and applies
// environment overrides. Feature flags are deliberately owned by the file.
func Load(path string) (*Config, error) {
	v, err := readConfig(path)
	if err != nil {
		return nil, err
	}

	// Capture file-owned flags before enabling environment overrides.
	localBank := v.GetBool("feature_flags.local_bank")
	enableEnvironment(v)

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	cfg.FeatureFlags.LocalBank = localBank

	if envOrigins := os.Getenv("CORS_ALLOW_ORIGINS"); envOrigins != "" {
		cfg.CORS.AllowOrigins = strings.Split(envOrigins, ",")
	}
	cfg.CORS.AllowOrigins = normalizeStringSlice(cfg.CORS.AllowOrigins)
	cfg.CORS.AllowMethods = normalizeStringSlice(cfg.CORS.AllowMethods)
	cfg.CORS.AllowHeaders = normalizeStringSlice(cfg.CORS.AllowHeaders)
	cfg.CORS.ExposeHeaders = normalizeStringSlice(cfg.CORS.ExposeHeaders)

	// Validate required fields
	if err := validateConfig(&cfg); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}
	return &cfg, nil
}

// LoadMigration reads only the settings needed to run database migrations.
func LoadMigration(path string) (*MigrationConfig, error) {
	v, err := readConfig(path)
	if err != nil {
		return nil, err
	}
	enableEnvironment(v)

	var cfg MigrationConfig
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal migration config: %w", err)
	}
	cfg.App.Env = strings.TrimSpace(cfg.App.Env)
	if cfg.DB.URL == "" {
		return nil, fmt.Errorf("validate migration config: db.url is required")
	}
	if isProductionEnvironment(cfg.App.Env) {
		if err := validatePostgresProductionURL(cfg.DB.URL); err != nil {
			return nil, fmt.Errorf("validate migration config: %w", err)
		}
	}
	return &cfg, nil
}

func readConfig(path string) (*viper.Viper, error) {
	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")
	setDefaults(v)

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	return v, nil
}

func enableEnvironment(v *viper.Viper) {
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
}

// setDefaults sets default values for all configuration keys.
func setDefaults(v *viper.Viper) {
	// App defaults
	v.SetDefault("app.env", "dev")
	v.SetDefault("app.port", "8080")
	v.SetDefault("app.public_url", "http://localhost:8080")
	v.SetDefault("app.frontend_url", "http://localhost:5173")
	v.SetDefault("app.migrations_dir", "./migrations")
	v.SetDefault("app.trusted_proxies", []string{})
	v.SetDefault("app.docs_enabled", false)

	// Server defaults
	v.SetDefault("server.metrics_port", "9091")
	v.SetDefault("server.read_timeout", "10s")
	v.SetDefault("server.write_timeout", "30s")
	v.SetDefault("server.idle_timeout", "60s")
	v.SetDefault("server.metrics_read_timeout", "5s")
	v.SetDefault("server.metrics_write_timeout", "5s")
	v.SetDefault("server.shutdown_timeout", "30s")

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
	v.SetDefault("rate_limit.resend.scope", "resend")
	v.SetDefault("rate_limit.resend.limit", 5)
	v.SetDefault("rate_limit.resend.window", "1h")

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
	v.SetDefault("minio.access_key", "")
	v.SetDefault("minio.secret_key", "")
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
	v.SetDefault("pictures_bank.url", "https://pictures.linka.su")
	v.SetDefault("pictures_bank.timeout", "5s")
	v.SetDefault("pictures_bank.requests_per_second", 5)
	v.SetDefault("pictures_bank.inbound_per_minute", 120)
	v.SetDefault("pictures_bank.max_concurrent", 4)
	v.SetDefault("pictures_bank.cache_ttl", "1h")
	v.SetDefault("pictures_bank.max_metadata_bytes", 2097152)
	v.SetDefault("pictures_bank.max_image_bytes", 10485760)

	// External Pictures Bank remains the default; local mode is file-owned.
	v.SetDefault("feature_flags.local_bank", false)

	// SMTP defaults
	v.SetDefault("smtp.host", "smtp.yandex.ru")
	v.SetDefault("smtp.port", 587)
	v.SetDefault("smtp.username", "")
	v.SetDefault("smtp.password", "")
	v.SetDefault("smtp.from_email", "")
	v.SetDefault("smtp.tls", true)
	v.SetDefault("smtp.timeout", "10s")
	v.SetDefault("smtp.require_from_match", true)
	v.SetDefault("smtp.daily_limit_alert", 300)

	// Crypto defaults
	v.SetDefault("crypto.aes_key", "")
	v.SetDefault("crypto.hmac_key", "")

	// Auth defaults
	v.SetDefault("auth.reset_password_token_ttl", "1h")
	v.SetDefault("auth.bcrypt_cost", 12)
	v.SetDefault("auth.login_rate_limit", 5)
	v.SetDefault("auth.register_rate_limit", 5)
	v.SetDefault("auth.refresh_rate_limit", 10)
	v.SetDefault("auth.require_email_verification", false)
	v.SetDefault("auth.unverified_retention", 168*time.Hour)
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

	// TTSApi defaults
	v.SetDefault("ttsapi.timeout", "30s")
	v.SetDefault("ttsapi.max_text_len", 5000)
	v.SetDefault("ttsapi.max_body_size", 65536)
	v.SetDefault("ttsapi.mime_type", "audio/mpeg")
	v.SetDefault("ttsapi.max_concurrent", 10)
	v.SetDefault("ttsapi.rate_limit", 120)

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
		"Authorization",
		"X-Request-Id",
	})
	v.SetDefault("cors.expose_headers", []string{
		"X-Request-Id",
	})
	v.SetDefault("cors.allow_credentials", true)
	v.SetDefault("cors.max_age", "24h")

	// Cron defaults
	v.SetDefault("cron.voice_refresh.interval", "1h")
	v.SetDefault("cron.tts_cleanup.interval", "6h")
	v.SetDefault("cron.tts_cleanup.clean_period", "2160h") // 90 days
	v.SetDefault("cron.tts_cleanup.jobs_ttl", "72h")       // 3 days
	v.SetDefault("cron.tts_cleanup.limit", 100)
}

// validateConfig validates required configuration fields.
func validateConfig(cfg *Config) error {
	// App validation
	cfg.App.Env = strings.TrimSpace(cfg.App.Env)
	if err := validateAppConfig(&cfg.App); err != nil {
		return err
	}

	// Production validation
	if isProductionEnvironment(cfg.App.Env) {
		if err := validateProductionConfig(cfg); err != nil {
			return err
		}
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
	if err := validateMinioConfig(&cfg.MinIO); err != nil {
		return err
	}

	// JWT validation
	if err := validateJWTConfig(&cfg.JWT); err != nil {
		return err
	}

	// Crypto validation
	if err := validateCryptoCongig(&cfg.Crypto); err != nil {
		return err
	}

	// TTSapi validation
	if cfg.TTS.ServiceURL == "" {
		return fmt.Errorf("ttsapi.service_url is required")
	}
	if cfg.TTS.RateLimit <= 0 {
		return fmt.Errorf("ttsapi.rate_limit must be > 0")
	}
	if cfg.TTS.MaxConcurrent <= 0 {
		return fmt.Errorf("ttsapi.max_concurrent must be > 0")
	}

	// CORS validation
	if err := validateCORSConfig(&cfg.CORS); err != nil {
		return err
	}
	return nil
}

func validateAppConfig(cfg *AppConfig) error {
	if cfg.Env == "" {
		return fmt.Errorf("app.env is required")
	}
	if cfg.FrontendURL == "" {
		return fmt.Errorf("app.frontend_url is required")
	}
	if u, err := url.Parse(cfg.FrontendURL); err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("app.frontend_url must be an absolute URL (scheme and host)")
	}
	return nil
}

func validateMinioConfig(cfg *MinIOConfig) error {
	if cfg.Endpoint == "" {
		return fmt.Errorf("minio.endpoint is required")
	}
	if cfg.AccessKey == "" {
		return fmt.Errorf("minio.access_key is required")
	}
	if cfg.SecretKey == "" {
		return fmt.Errorf("minio.secret_key is required")
	}
	if cfg.Bucket == "" {
		return fmt.Errorf("minio.bucket is required")
	}
	return nil
}

func validateJWTConfig(cfg *JWTConfig) error {
	if cfg.Secret == "" {
		return fmt.Errorf("jwt.secret is required")
	}
	if len(cfg.Secret) < 32 {
		return fmt.Errorf("jwt.secret must be at least 32 characters")
	}
	return nil
}

func validateCryptoCongig(cfg *CryptoConfig) error {
	if len(cfg.AESKey) == 0 || len(cfg.HMACKey) == 0 {
		aesKey, hmacKey, err := decodeConfiguredCryptoKeys(cfg.AESKeyRaw, cfg.HMACKeyRaw)
		if err != nil {
			return err
		}
		cfg.AESKey = aesKey
		cfg.HMACKey = hmacKey
	}

	return nil
}

func validateCORSConfig(cfg *CORSConfig) error {
	if len(cfg.AllowOrigins) == 0 {
		return fmt.Errorf("cors.allow_origins is required")
	}
	if len(cfg.AllowMethods) == 0 {
		return fmt.Errorf("cors.allow_methods is required")
	}
	if len(cfg.AllowHeaders) == 0 {
		return fmt.Errorf("cors.allow_headers is required")
	}
	if cfg.MaxAge < 0 {
		return fmt.Errorf("cors.max_age must be non-negative")
	}
	if cfg.MaxAge > 0 && cfg.MaxAge < time.Second {
		return fmt.Errorf("cors.max_age is %s, which looks like a raw number misparsed as nanoseconds — use a duration string with a unit (e.g. \"24h\")", cfg.MaxAge)
	}
	return nil
}

func normalizeStringSlice(items []string) []string {
	result := make([]string, 0, len(items))
	for _, s := range items {
		if s = strings.TrimSpace(s); s != "" {
			result = append(result, s)
		}
	}
	return result
}

// MediaConfig contains media library settings.
type MediaConfig struct {
	BatchDeleteLimit int `mapstructure:"batch_delete_limit"`
}
