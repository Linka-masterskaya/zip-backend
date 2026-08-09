// Command migrate applies database migrations and exits.
package main

import (
	"database/sql"
	"fmt"
	"log/slog"
	"os"

	_ "github.com/lib/pq"

	"github.com/Linka-masterskaya/zip-backend/internal/config"
	"github.com/Linka-masterskaya/zip-backend/internal/logger"
	"github.com/Linka-masterskaya/zip-backend/migrations"
)

const defaultConfigPath = "config/config.dev.yml"

func main() {
	if err := run(); err != nil {
		slog.Error("migrate failed", "err", err)
		os.Exit(1)
	}
	slog.Info("migrations completed")
}

// run holds the whole flow so deferred cleanup still runs: os.Exit skips
// deferred calls, so calling it directly from main would leak the connection.
func run() error {
	cfgPath := os.Getenv("CONFIG_PATH")
	if cfgPath == "" {
		cfgPath = defaultConfigPath
	}

	cfg, err := config.LoadMigration(cfgPath)
	if err != nil {
		return fmt.Errorf("config load: %w", err)
	}
	logger.Init(cfg.App.Env)

	conn, err := sql.Open("postgres", cfg.DB.URL)
	if err != nil {
		return fmt.Errorf("connect to postgres: %w", err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			slog.Error("close db connection failed", "error", err)
		}
	}()

	if err := migrations.Run(conn); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}
	return nil
}
