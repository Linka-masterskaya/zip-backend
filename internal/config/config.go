// Package config loads application configuration.
package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config contains all application settings.
type Config struct {
	App    AppConfig    `mapstructure:"app"`
	DB     DBConfig     `mapstructure:"db"`
	Redis  RedisConfig  `mapstructure:"redis"`
	NATS   NATSConfig   `mapstructure:"nats"`
	MinIO  MinIOConfig  `mapstructure:"minio"`
	JWT    JWTConfig    `mapstructure:"jwt"`
	Google GoogleConfig `mapstructure:"google"`
}

// AppConfig contains application runtime settings.
type AppConfig struct {
	Env  string `mapstructure:"env"`
	Port string `mapstructure:"port"`
}

// DBConfig contains database connection settings.
type DBConfig struct {
	URL string `mapstructure:"url"`
}

// RedisConfig contains Redis connection settings.
type RedisConfig struct {
	URL string `mapstructure:"url"`
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
}

// JWTConfig contains JWT signing and expiration settings.
type JWTConfig struct {
	Secret     string `mapstructure:"secret"`
	AccessTTL  string `mapstructure:"access_ttl"`
	RefreshTTL string `mapstructure:"refresh_ttl"`
}

// GoogleConfig contains Google OAuth settings.
type GoogleConfig struct {
	ClientID     string `mapstructure:"client_id"`
	ClientSecret string `mapstructure:"client_secret"`
	RedirectURL  string `mapstructure:"redirect_url"`
}

// Load reads configuration from a file and applies environment overrides.
func Load(path string) (*Config, error) {
	v := viper.New()

	v.SetConfigFile(path)
	v.SetConfigType("yaml")

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
	return &cfg, nil
}
