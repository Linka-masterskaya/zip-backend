package db

import (
	"context"
	"fmt"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/config"
	"github.com/jackc/pgx/v5/pgxpool"
)

func New(cfg config.DBConfig) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(cfg.URL)

	if err != nil {
		return nil, fmt.Errorf("failed to parse postgres URL (%s): %w", cfg.URL, err)
	}

	config.MaxConns = cfg.MaxConns
	config.MinConns = cfg.MinConns
	config.MaxConnLifetime = cfg.MaxConnLifetime
	config.HealthCheckPeriod = cfg.HealthCheckPeriod

	dbpool, err := pgxpool.NewWithConfig(context.Background(), config)

	if err != nil {
		return nil, fmt.Errorf("failed to create pgxpool: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err = dbpool.Ping(ctx)

	if err != nil {
		return nil, fmt.Errorf("failed to ping postgres: %w", err)
	}

	return dbpool, nil
}
