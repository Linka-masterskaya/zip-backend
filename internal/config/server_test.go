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

	if cfg.Server.MetricsPort != "9091" {
		t.Errorf("MetricsPort = %q, want %q", cfg.Server.MetricsPort, "9091")
	}
}

func TestProductionMetricsPortMatchesDeployment(t *testing.T) {
	setValidProductionEnv(t)

	cfg, err := Load("../../config/config.prod.yml")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Server.MetricsPort != "9091" {
		t.Errorf("MetricsPort = %q, want %q", cfg.Server.MetricsPort, "9091")
	}
}
