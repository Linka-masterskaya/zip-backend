package middleware_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/cache"
	"github.com/Linka-masterskaya/zip-backend/internal/config"
	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	tc "github.com/testcontainers/testcontainers-go"
	rediscontainer "github.com/testcontainers/testcontainers-go/modules/redis"
)

const (
	containerTimeout = 60 * time.Second
	redisImage       = "redis:7-alpine"
)

func newTestRedis(t *testing.T) (*rediscontainer.RedisContainer, *redis.Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), containerTimeout)
	defer cancel()

	container, err := rediscontainer.Run(ctx, redisImage)
	tc.CleanupContainer(t, container)
	if err != nil {
		t.Fatalf("start redis container: %v", err)
	}

	uri, err := container.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("redis connection string: %v", err)
	}

	opt, err := redis.ParseURL(uri)
	if err != nil {
		t.Fatalf("parse redis url: %v", err)
	}

	raw := redis.NewClient(opt)
	t.Cleanup(func() { _ = raw.Close() })
	return container, raw
}

func newTestCacheClient(t *testing.T, container *rediscontainer.RedisContainer) *cache.Client {
	t.Helper()
	uri, err := container.ConnectionString(t.Context())
	require.NoError(t, err)

	c, err := cache.NewClient(config.RedisConfig{
		URL:        uri,
		ClientName: "middleware-test",
		PoolSize:   5,
	})
	require.NoError(t, err)
	return c
}

func TestRateLimitMiddleware(t *testing.T) {
	container, raw := newTestRedis(t)
	cacheClient := newTestCacheClient(t, container)

	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	t.Run("AllowRequestsWithinQuota", func(t *testing.T) {
		require.NoError(t, raw.FlushDB(t.Context()).Err())
		limiter := middleware.RateLimit(cacheClient, "test_quota", 2, time.Minute, []string{"127.0.0.1"})
		handler := limiter(nextHandler)

		for i := 0; i < 2; i++ {
			req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBuffer([]byte(`{"email":"user@test.com"}`))) //nolint:noctx
			req.RemoteAddr = "127.0.0.1:12345"
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)
			require.Equal(t, http.StatusOK, rec.Code)
		}

		reqBlock := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBuffer([]byte(`{"email":"user@test.com"}`))) //nolint:noctx
		reqBlock.RemoteAddr = "127.0.0.1:12345"
		recBlock := httptest.NewRecorder()

		handler.ServeHTTP(recBlock, reqBlock)
		require.Equal(t, http.StatusTooManyRequests, recBlock.Code)
		require.NotEmpty(t, recBlock.Header().Get("Retry-After"))
	})

	t.Run("RejectUntrustedProxyHeaders", func(t *testing.T) {
		require.NoError(t, raw.FlushDB(t.Context()).Err())
		limiter := middleware.RateLimit(cacheClient, "test_spoof", 1, time.Minute, []string{"192.168.1.1"})
		handler := limiter(nextHandler)

		req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBuffer([]byte(`{"email":"test@test.com"}`))) //nolint:noctx
		req.RemoteAddr = "203.0.113.5:12345"
		req.Header.Set("X-Real-IP", "1.1.1.1")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)

		reqBlock := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBuffer([]byte(`{"email":"test2@test.com"}`))) //nolint:noctx
		reqBlock.RemoteAddr = "203.0.113.5:54321"
		reqBlock.Header.Set("X-Real-IP", "2.2.2.2")
		recBlock := httptest.NewRecorder()

		handler.ServeHTTP(recBlock, reqBlock)
		require.Equal(t, http.StatusTooManyRequests, recBlock.Code)
	})

	t.Run("BlockDistributedBruteForceByEmail", func(t *testing.T) {
		require.NoError(t, raw.FlushDB(t.Context()).Err())
		limiter := middleware.RateLimit(cacheClient, "test_dist", 2, time.Minute, []string{"127.0.0.1"})
		handler := limiter(nextHandler)

		req1 := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBuffer([]byte(`{"email":"victim@test.com"}`))) //nolint:noctx
		req1.RemoteAddr = "192.168.1.10:1234"
		rec1 := httptest.NewRecorder()
		handler.ServeHTTP(rec1, req1)
		require.Equal(t, http.StatusOK, rec1.Code)

		req2 := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBuffer([]byte(`{"email":"victim@test.com"}`))) //nolint:noctx
		req2.RemoteAddr = "192.168.1.11:1234"
		rec2 := httptest.NewRecorder()
		handler.ServeHTTP(rec2, req2)
		require.Equal(t, http.StatusOK, rec2.Code)

		req3 := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBuffer([]byte(`{"email":"victim@test.com"}`))) //nolint:noctx
		req3.RemoteAddr = "192.168.1.12:1234"
		rec3 := httptest.NewRecorder()
		handler.ServeHTTP(rec3, req3)
		require.Equal(t, http.StatusTooManyRequests, rec3.Code)
	})
}
