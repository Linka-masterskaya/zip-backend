package health

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/nats-io/nats.go"
	"golang.org/x/sync/errgroup"

	"github.com/Linka-masterskaya/zip-backend/internal/cache"
)

// Pinger — интерфейс для проверки PostgreSQL.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Lister — интерфейс для проверки MinIO.
type Lister interface {
	ListBuckets(ctx context.Context) ([]minio.BucketInfo, error)
}

type checkResult struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// Checker содержит клиенты для проверки зависимостей.
type Checker struct {
	DB          Pinger
	RedisClient *cache.Client
	NatsConn    *nats.Conn
	MinioClient Lister
}

// Run запускает параллельные проверки с таймаутом 2 секунды.
func (c *Checker) Run(ctx context.Context) (int, interface{}) {
	checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	g, ctxGroup := errgroup.WithContext(checkCtx)
	var mu sync.Mutex
	results := make(map[string]checkResult)

	checks := map[string]func(context.Context) error{
		"postgres": func(ctx context.Context) error {
			if c.DB == nil {
				return errors.New("postgres client not initialized")
			}
			return c.DB.Ping(ctx)
		},
		"redis": func(ctx context.Context) error {
			if c.RedisClient == nil {
				return errors.New("redis client not initialized")
			}
			return c.RedisClient.Ping(ctx)
		},
		"nats": func(ctx context.Context) error {
			if c.NatsConn == nil {
				return errors.New("nats client not initialized")
			}
			if !c.NatsConn.IsConnected() {
				return errors.New("nats connection is closed")
			}
			return nil
		},
		"minio": func(ctx context.Context) error {
			if c.MinioClient == nil {
				return errors.New("minio client not initialized")
			}
			_, err := c.MinioClient.ListBuckets(ctx)
			return err
		},
	}

	for name, check := range checks {
		name, check := name, check
		g.Go(func() error {
			err := check(ctxGroup)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				results[name] = checkResult{Status: "ok"}
			} else {
				results[name] = checkResult{Status: "error", Error: err.Error()}
			}
			return nil
		})
	}

	_ = g.Wait()

	finalStatus := "ok"
	for _, res := range results {
		if res.Status == "error" {
			finalStatus = "degraded"
			break
		}
	}

	httpStatus := http.StatusOK
	if finalStatus == "degraded" {
		httpStatus = http.StatusServiceUnavailable
	}

	response := struct {
		Status string                 `json:"status"`
		Checks map[string]checkResult `json:"checks"`
	}{
		Status: finalStatus,
		Checks: results,
	}
	return httpStatus, response
}
