// Package config loads application configuration.
package config

import (
	"fmt"
	"strings"

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
	URL string `mapstructure:"url"`
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
