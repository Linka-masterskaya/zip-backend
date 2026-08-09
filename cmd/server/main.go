// Command server runs the HTTP API server.
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/Linka-masterskaya/zip-backend/internal/app"
)

const defaultConfigPath = "config/config.dev.yml"

func main() {
	cfgPath := os.Getenv("CONFIG_PATH")
	if cfgPath == "" {
		cfgPath = defaultConfigPath
	}

	application, err := app.Bootstrap(cfgPath)
	if err != nil {
		slog.Error("bootstrap failed", "err", err)
		os.Exit(1)
	}

	if err := application.Run(context.Background()); err != nil {
		slog.Error("application failed", "err", err)
		os.Exit(1)
	}
}
