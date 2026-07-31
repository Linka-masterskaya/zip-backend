package config

import (
	"testing"
	"time"
)

func TestServerDefaults(t *testing.T) {
	cfg, err := Load("../../config/config.dev.yml")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	cases := []struct {
		name string
		got  time.Duration
		want time.Duration
	}{
		{"read", cfg.Server.ReadTimeout, 10 * time.Second},
		{"write", cfg.Server.WriteTimeout, 30 * time.Second},
		{"idle", cfg.Server.IdleTimeout, 60 * time.Second},
		{"metrics read", cfg.Server.MetricsReadTimeout, 5 * time.Second},
		{"metrics write", cfg.Server.MetricsWriteTimeout, 5 * time.Second},
		{"shutdown", cfg.Server.ShutdownTimeout, 30 * time.Second},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s timeout = %v, want %v", c.name, c.got, c.want)
		}
	}

	if cfg.Server.MetricsPort != "9090" {
		t.Errorf("MetricsPort = %q, want %q", cfg.Server.MetricsPort, "9090")
	}
}

func TestProductionMetricsPortMatchesDeployment(t *testing.T) {
	t.Setenv("JWT_SECRET", "environment-secret-that-is-at-least-32-bytes")
	t.Setenv("CRYPTO_AES_KEY", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	t.Setenv("CRYPTO_HMAC_KEY", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")

	cfg, err := Load("../../config/config.prod.yml")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Server.MetricsPort != "9091" {
		t.Errorf("MetricsPort = %q, want %q", cfg.Server.MetricsPort, "9091")
	}
}
