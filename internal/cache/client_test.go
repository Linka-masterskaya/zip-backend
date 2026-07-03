package cache_test

import (
	"context"
	"testing"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/cache"
	"github.com/Linka-masterskaya/zip-backend/internal/config"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	tc "github.com/testcontainers/testcontainers-go"
	rediscontainer "github.com/testcontainers/testcontainers-go/modules/redis"
)

const (
	testTimeout      = 10 * time.Second
	containerTimeout = 60 * time.Second
)

const redisImage = "redis:7-alpine"

func newRedis(t *testing.T) (*rediscontainer.RedisContainer, *redis.Client) {
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
	opt.ReadTimeout = 500 * time.Millisecond
	opt.WriteTimeout = 500 * time.Millisecond
	opt.DialTimeout = 2 * time.Second
	opt.ContextTimeoutEnabled = true

	raw := redis.NewClient(opt)
	t.Cleanup(func() { _ = raw.Close() })

	if err := raw.Ping(ctx).Err(); err != nil {
		t.Fatalf("ping redis: %v", err)
	}

	return container, raw
}

func newClient(t *testing.T, container *rediscontainer.RedisContainer) *cache.Client {
	t.Helper()
	uri, err := container.ConnectionString(t.Context())
	require.NoError(t, err)

	c, err := cache.NewClient(config.RedisConfig{
		URL:        uri,
		ClientName: "test",
		PoolSize:   10,
	})
	require.NoError(t, err)
	return c
}

func subCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	t.Cleanup(cancel)
	return ctx
}

func flush(ctx context.Context, t *testing.T, raw *redis.Client) {
	t.Helper()
	require.NoError(t, raw.FlushDB(ctx).Err(), "flush before subtest")
}

func TestCache(t *testing.T) {
	container, raw := newRedis(t)
	c := newClient(t, container)

	t.Run("StoreAndGetRefresh", func(t *testing.T) {
		ctx := subCtx(t)
		flush(ctx, t, raw)

		rec := cache.RefreshRecord{FID: "fam1", Status: "active"}
		require.NoError(t, c.StoreRefresh(ctx, "jti1", rec, time.Minute))

		got, err := c.GetRefresh(ctx, "jti1")
		require.NoError(t, err)
		require.Equal(t, rec, *got)
	})

	t.Run("GetRefresh_NotFound", func(t *testing.T) {
		ctx := subCtx(t)
		flush(ctx, t, raw)

		_, err := c.GetRefresh(ctx, "missing")
		require.ErrorIs(t, err, cache.ErrNotFound)
	})

	t.Run("StoreRefresh_SetsTTL", func(t *testing.T) {
		ctx := subCtx(t)
		flush(ctx, t, raw)

		require.NoError(t, c.StoreRefresh(ctx, "jti1", cache.RefreshRecord{FID: "fam1", Status: "active"}, time.Minute))

		ttl, err := raw.TTL(ctx, "refresh:jti1").Result()
		require.NoError(t, err)
		require.Greater(t, ttl, time.Duration(0))
	})

	t.Run("IsFamilyRevoked", func(t *testing.T) {
		ctx := subCtx(t)
		flush(ctx, t, raw)

		require.NoError(t, c.StoreRefresh(ctx, "jti1", cache.RefreshRecord{FID: "fam1", Status: "active"}, time.Minute))
		revoked, err := c.IsFamilyRevoked(ctx, "fam1")
		require.NoError(t, err)
		require.False(t, revoked)

		require.NoError(t, c.RevokeFamily(ctx, "fam1"))
		revoked, err = c.IsFamilyRevoked(ctx, "fam1")
		require.NoError(t, err)
		require.True(t, revoked)

		revoked, err = c.IsFamilyRevoked(ctx, "nonexistent")
		require.NoError(t, err)
		require.True(t, revoked)
	})

	t.Run("RotateRefresh", func(t *testing.T) {
		ctx := subCtx(t)
		flush(ctx, t, raw)

		require.NoError(t, c.StoreRefresh(ctx, "old", cache.RefreshRecord{FID: "fam1", Status: "active"}, time.Minute))

		req := cache.RotateRefreshRequest{
			OldJTI:    "old",
			NewJTI:    "new",
			NewRecord: cache.RefreshRecord{FID: "fam1", Status: "active"},
			TTL:       time.Minute,
		}
		require.NoError(t, c.RotateRefresh(ctx, req))

		oldRec, err := c.GetRefresh(ctx, "old")
		require.NoError(t, err)
		require.Equal(t, "revoked", oldRec.Status)

		newRec, err := c.GetRefresh(ctx, "new")
		require.NoError(t, err)
		require.Equal(t, "active", newRec.Status)
	})

	t.Run("Allow_RateLimit", func(t *testing.T) {
		ctx := subCtx(t)
		flush(ctx, t, raw)

		req := cache.RateLimitRequest{Scope: "login", Key: "user1", Limit: 3, WindowSize: time.Minute}

		for i := 1; i <= 3; i++ {
			allowed, retryAfter, err := c.Allow(ctx, req)
			require.NoError(t, err)
			require.Zero(t, retryAfter)
			require.True(t, allowed)
		}

		allowed, retryAfter, err := c.Allow(ctx, req)
		require.NoError(t, err)
		require.False(t, allowed)
		require.Greater(t, retryAfter, int64(0))
	})

	t.Run("IncrCounter_SetsTTLOnFirst", func(t *testing.T) {
		ctx := subCtx(t)
		flush(ctx, t, raw)

		_, err := c.IncrCounter(ctx, "rl:test:k1", time.Minute)
		require.NoError(t, err)

		ttl, err := raw.TTL(ctx, "rl:test:k1").Result()
		require.NoError(t, err)
		require.Greater(t, ttl, time.Duration(0))
	})
}
