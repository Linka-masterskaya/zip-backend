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

// testTimeout —  жёсткий лимит на каждый подтест. Меняй здесь, а не в команде запуска.
const (
	testTimeout      = 10 * time.Second
	containerTimeout = 60 * time.Second
)

// redisImage pinned to match docker-compose and prod.
const redisImage = "redis:7-alpine"

// newRedis starts one Redis container for the whole test function and returns
// the container plus a raw client (for FlushDB setup and TTL assertions).
// Teardown is registered via t.Cleanup.
func newRedis(t *testing.T) (*rediscontainer.RedisContainer, *redis.Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), containerTimeout) // лимит на старт контейнера
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
	// таймауты, чтобы зависший Redis не повесил весь прогон
	opt.ReadTimeout = 500 * time.Millisecond
	opt.WriteTimeout = 500 * time.Millisecond
	opt.DialTimeout = 2 * time.Second
	opt.ContextTimeoutEnabled = true // уважать дедлайн контекста на уровне команды

	raw := redis.NewClient(opt)
	t.Cleanup(func() { _ = raw.Close() })

	if err := raw.Ping(ctx).Err(); err != nil {
		t.Fatalf("ping redis: %v", err)
	}

	return container, raw
}

// newClient builds a cache.Client via the real constructor (private rdb field).
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

// subCtx returns a context bounded by both the subtest lifetime and a hard timeout.
func subCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	t.Cleanup(cancel)
	return ctx
}

// flush wipes the DB so each subtest starts on a clean Redis.
func flush(ctx context.Context, t *testing.T, raw *redis.Client) {
	t.Helper()
	require.NoError(t, raw.FlushDB(ctx).Err(), "flush before subtest")
}

// TestCache runs all cache tests against a single shared Redis container.
// Subtests run sequentially (no t.Parallel) and each flushes the DB first,
// so no subtest depends on another's state.
func TestCache(t *testing.T) {
	container, raw := newRedis(t)
	c := newClient(t, container)

	t.Run("StoreAndGetRefresh", func(t *testing.T) {
		// Roundtrip: записанный токен читается обратно без искажений.
		ctx := subCtx(t)
		flush(ctx, t, raw)

		rec := cache.RefreshRecord{FID: "fam1", Status: "active"}
		require.NoError(t, c.StoreRefresh(ctx, "jti1", rec, time.Minute))

		got, err := c.GetRefresh(ctx, "jti1")
		require.NoError(t, err)
		require.Equal(t, rec, *got)
	})

	t.Run("GetRefresh_NotFound", func(t *testing.T) {
		// Отсутствующий токен → доменная ошибка ErrNotFound, не пустая запись.
		ctx := subCtx(t)
		flush(ctx, t, raw)

		_, err := c.GetRefresh(ctx, "missing")
		require.ErrorIs(t, err, cache.ErrNotFound)
	})

	t.Run("StoreRefresh_SetsTTL", func(t *testing.T) {
		// На ключ токена реально выставлен TTL (не вечный). Проверяем TTL > 0,
		// не дожидаясь истечения — быстро и не флейки.
		ctx := subCtx(t)
		flush(ctx, t, raw)

		require.NoError(t, c.StoreRefresh(ctx, "jti1", cache.RefreshRecord{FID: "fam1", Status: "active"}, time.Minute))

		ttl, err := raw.TTL(ctx, "refresh:jti1").Result()
		require.NoError(t, err)
		require.Greater(t, ttl, time.Duration(0), "token must have a TTL")
	})

	t.Run("IsFamilyRevoked", func(t *testing.T) {
		// Три состояния семьи: active→false, revoked→true,
		// отсутствует→true (fail-closed: нет записи = считаем мёртвой).
		ctx := subCtx(t)
		flush(ctx, t, raw)

		require.NoError(t, c.StoreRefresh(ctx, "jti1", cache.RefreshRecord{FID: "fam1", Status: "active"}, time.Minute))
		revoked, err := c.IsFamilyRevoked(ctx, "fam1")
		require.NoError(t, err)
		require.False(t, revoked, "active family must not be revoked")

		require.NoError(t, c.RevokeFamily(ctx, "fam1"))
		revoked, err = c.IsFamilyRevoked(ctx, "fam1")
		require.NoError(t, err)
		require.True(t, revoked, "revoked family must report revoked")

		revoked, err = c.IsFamilyRevoked(ctx, "nonexistent")
		require.NoError(t, err)
		require.True(t, revoked, "missing family must be treated as revoked (fail-closed)")
	})

	t.Run("RotateRefresh", func(t *testing.T) {
		// Ротация: старый JTI → revoked, новый JTI → active, оба в Redis.
		// Detect-reuse / атомарность (Lua) здесь не проверяется — tech debt.
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
		require.Equal(t, "revoked", oldRec.Status, "old token must be revoked after rotation")

		newRec, err := c.GetRefresh(ctx, "new")
		require.NoError(t, err)
		require.Equal(t, "active", newRec.Status, "new token must be active")
	})

	t.Run("Allow_RateLimit", func(t *testing.T) {
		// Лимитер фиксированного окна: первые Limit запросов проходят,
		// следующий сверх лимита блокируется.
		ctx := subCtx(t)
		flush(ctx, t, raw)

		req := cache.RateLimitRequest{Scope: "login", Key: "user1", Limit: 3, WindowSize: time.Minute}

		for i := 1; i <= 3; i++ {
			allowed, err := c.Allow(ctx, req)
			require.NoError(t, err)
			require.True(t, allowed, "request %d within limit must be allowed", i)
		}

		allowed, err := c.Allow(ctx, req)
		require.NoError(t, err)
		require.False(t, allowed, "request over limit must be denied")
	})

	t.Run("IncrCounter_SetsTTLOnFirst", func(t *testing.T) {
		// TTL ставится на ПЕРВОМ инкременте (count==1), окно лимита не вечное.
		ctx := subCtx(t)
		flush(ctx, t, raw)

		_, err := c.IncrCounter(ctx, "rl:test:k1", time.Minute)
		require.NoError(t, err)

		ttl, err := raw.TTL(ctx, "rl:test:k1").Result()
		require.NoError(t, err)
		require.Greater(t, ttl, time.Duration(0), "counter must have TTL after first incr")
	})
}
