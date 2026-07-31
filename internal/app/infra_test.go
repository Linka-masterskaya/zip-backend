package app

import (
	"context"
	"testing"

	"github.com/Linka-masterskaya/zip-backend/internal/config"
)

// initInfra needs live Postgres/Redis/NATS/MinIO, so the reachable assertion is
// the failure path: unreachable dependencies must produce an error, and the
// closer must release whatever was created before the failing step.
func TestInitInfraFailsOnUnreachableDependencies(t *testing.T) {
	cfg := minimalUnreachableConfig()
	var c Closer

	in, err := initInfra(cfg, &c)
	if err == nil {
		t.Fatal("initInfra() = nil error, want failure on unreachable dependencies")
	}
	if in != nil {
		t.Errorf("initInfra() returned %v on failure, want nil", in)
	}
	if closeErr := c.Close(context.Background()); closeErr != nil {
		t.Errorf("Close() after failed init = %v, want nil", closeErr)
	}
}

func minimalUnreachableConfig() *config.Config {
	cfg := &config.Config{}
	cfg.MinIO.Endpoint = "127.0.0.1:1"
	cfg.NATS.Connection.URL = "nats://127.0.0.1:1"
	cfg.Redis.URL = "redis://127.0.0.1:1/0"
	cfg.DB.URL = "postgres://127.0.0.1:1/none"
	return cfg
}
