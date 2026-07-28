// internal/testutil/containers.go
package testutil

import (
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	redis "github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	rediscontainer "github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/testcontainers/testcontainers-go/wait"
)

// NewPostgres starts a temporary PostgreSQL container for integration tests,
// creates a pgx connection pool and returns a cleanup function that releases
// all allocated resources.
func NewPostgres(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	ctx := context.Background()
	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("postgres"),
		postgres.WithUsername("postgres"),
		postgres.WithPassword("postgres"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("failed to start PostgreSQL container: %v", err)
	}
	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("failed to get PostgreSQL connection string: %v", err)
	}
	dbPool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		if err := pgContainer.Terminate(ctx); err != nil {
			t.Logf("failed to terminate PostgreSQL container after pool creation error: %v", err)
		}
		t.Fatalf("failed to create PostgreSQL connection pool: %v", err)
	}
	// Verify that PostgreSQL is actually reachable before returning the pool.
	// Retry a few times with backoff.
	var pingErr error
	for range 10 {
		pingErr = dbPool.Ping(ctx)
		if pingErr == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if pingErr != nil {
		dbPool.Close()
		if err := pgContainer.Terminate(ctx); err != nil {
			t.Logf("failed to terminate PostgreSQL container after ping error: %v", err)
		}
		t.Fatalf("failed to ping PostgreSQL: %v", pingErr)
	}
	// Cleanup closes all database connections and removes the container.
	cleanup := func() {
		dbPool.Close()
		if err := pgContainer.Terminate(ctx); err != nil {
			t.Logf("failed to terminate PostgreSQL container: %v", err)
		}
	}
	return dbPool, cleanup
}

// NewRedis starts a temporary Redis container for integration tests,
// creates a Redis client and returns a cleanup function that releases
// all allocated resources.
func NewRedis(t *testing.T) (*redis.Client, func()) {
	t.Helper()
	ctx := context.Background()
	redisContainer, err := rediscontainer.Run(ctx, "redis:7.0.11-alpine")
	if err != nil {
		t.Fatalf("failed to start Redis container: %v", err)
	}
	uri, err := redisContainer.Endpoint(ctx, "")
	if err != nil {
		if err := redisContainer.Terminate(ctx); err != nil {
			t.Logf("failed to terminate Redis container after endpoint error: %v", err)
		}
		t.Fatalf("failed to get Redis endpoint: %v", err)
	}
	client := redis.NewClient(&redis.Options{
		Addr: uri,
		DB:   0,
	})
	// Verify that Redis is actually reachable before returning the client.
	if err := client.Ping(ctx).Err(); err != nil {
		if err := client.Close(); err != nil {
			t.Logf("close redis client: %v", err)
		}
		if err := redisContainer.Terminate(ctx); err != nil {
			t.Logf("failed to terminate Redis container after ping error: %v", err)
		}
		t.Fatalf("failed to ping Redis: %v", err)
	}
	// Cleanup closes the Redis client and removes the container.
	cleanup := func() {
		if err := client.Close(); err != nil {
			t.Logf("close redis client: %v", err)
		}
		if err := redisContainer.Terminate(ctx); err != nil {
			t.Logf("failed to terminate Redis container: %v", err)
		}
	}
	return client, cleanup
}

// NewPostgresCtx — реальная логика. Возвращает error, не зависит от *testing.T.
// Используется в TestMain (где нет *testing.T).
func NewPostgresCtx(ctx context.Context) (*pgxpool.Pool, func(), error) {
	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("postgres"),
		postgres.WithUsername("postgres"),
		postgres.WithPassword("postgres"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second),
		),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("start postgres container: %w", err)
	}

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		if err := pgContainer.Terminate(ctx); err != nil {
			slog.Error("failed to terminate postgres container", "err", err)
		}
		return nil, nil, fmt.Errorf("get connection string: %w", err)
	}

	dbPool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		if err := pgContainer.Terminate(ctx); err != nil {
			slog.Error("failed to terminate postgres container", "err", err)
		}
		return nil, nil, fmt.Errorf("create pool: %w", err)
	}

	// Retry ping a few times with backoff.
	var pingErr error
	for range 10 {
		pingErr = dbPool.Ping(ctx)
		if pingErr == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if pingErr != nil {
		dbPool.Close()
		if err := pgContainer.Terminate(ctx); err != nil {
			slog.Error("failed to terminate postgres container", "err", err)
		}
		return nil, nil, fmt.Errorf("ping postgres: %w", pingErr)
	}

	cleanup := func() {
		dbPool.Close()
		if err := pgContainer.Terminate(ctx); err != nil {
			slog.Error("failed to terminate postgres container", "err", err)
		}
	}

	return dbPool, cleanup, nil
}
