package cache_test

import (
	"context"
	"testing"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/cache"
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
	// таймауты, чтобы зависший Redis не повесил весь прогон
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

// newClient builds a cache.Client via the real constructor (private rdb field).
func newClient(t *testing.T, container *rediscontainer.RedisContainer) *cache.Client {
	t.Helper()
	uri, err := container.ConnectionString(t.Context())
	require.NoError(t, err)

	c, err := cache.NewClient(
		cache.Config{
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
		require.Greater(t, ttl, time.Duration(0))
	})

	t.Run("IsFamilyRevoked", func(t *testing.T) {
		// Три состояния семьи: active→false, revoked→true,
		// отсутствует→true (fail-closed: нет записи = считаем мёртвой).
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

	t.Run("RevokeAllSessions", func(t *testing.T) {
		// Отзыв всех сессий пользователя — это инкремент счётчика версии,
		// токены, выпущенные до отзыва, становятся "старее" текущей версии и считаются отозванными,
		// чужой пользователь не затрагивается, новый токен после отзыва валиден.
		ctx := subCtx(t)
		flush(ctx, t, raw)

		require.NoError(t, c.StoreRefresh(ctx, "jti1", cache.RefreshRecord{FID: "fam1", Status: "active", UserID: "user1"}, time.Minute))
		require.NoError(t, c.StoreRefresh(ctx, "jti3", cache.RefreshRecord{FID: "fam3", Status: "active", UserID: "user2"}, time.Minute))

		rec1, err := c.GetRefresh(ctx, "jti1")
		require.NoError(t, err)
		rec3, err := c.GetRefresh(ctx, "jti3")
		require.NoError(t, err)

		require.NoError(t, c.RevokeAllSessions(ctx, "user1"))

		revoked, err := c.IsSessionRevoked(ctx, *rec1)
		require.NoError(t, err)
		require.True(t, revoked, "сессия, выпущенная до отзыва, должна считаться отозванной")

		revoked, err = c.IsSessionRevoked(ctx, *rec3)
		require.NoError(t, err)
		require.False(t, revoked, "чужая сессия не должна отозваться")

		require.NoError(t, c.StoreRefresh(ctx, "jti2", cache.RefreshRecord{FID: "fam2", Status: "active", UserID: "user1"}, time.Minute))
		rec2, err := c.GetRefresh(ctx, "jti2")
		require.NoError(t, err)

		revoked, err = c.IsSessionRevoked(ctx, *rec2)
		require.NoError(t, err)
		require.False(t, revoked, "новый токен, выпущенный после отзыва, не должен считаться отозванным")
	})

	t.Run("GetUserSessionVersion_DefaultsToZero", func(t *testing.T) {
		// Пользователь, который никогда не отзывал сессии, имеет версию 0.
		ctx := subCtx(t)
		flush(ctx, t, raw)

		version, err := c.GetUserSessionVersion(ctx, "user1")
		require.NoError(t, err)
		require.Zero(t, version)

		require.NoError(t, c.RevokeAllSessions(ctx, "user1"))
		version, err = c.GetUserSessionVersion(ctx, "user1")
		require.NoError(t, err)
		require.Equal(t, int64(1), version)
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
			allowed, retry, err := c.Allow(ctx, req)
			require.NoError(t, err)
			require.True(t, allowed, "request %d within limit must be allowed", i)
			require.Zero(t, retry, "retry-after must be 0 while allowed")
		}

		allowed, retry, err := c.Allow(ctx, req)
		require.NoError(t, err)
		require.False(t, allowed, "request over limit must be denied")
		require.GreaterOrEqual(t, retry, int64(1), "retry-after must be >= 1s when denied")
		require.LessOrEqual(t, retry, int64(60), "retry-after must not exceed window")
	})

	t.Run("IncrCounter_SetsTTLOnFirst", func(t *testing.T) {
		// TTL ставится на ПЕРВОМ инкременте (count==1), окно лимита не вечное.
		ctx := subCtx(t)
		flush(ctx, t, raw)

		_, err := c.IncrCounter(ctx, "rl:test:k1", time.Minute)
		require.NoError(t, err)

		ttl, err := raw.TTL(ctx, "rl:test:k1").Result()
		require.NoError(t, err)
		require.Greater(t, ttl, time.Duration(0))
	})
}
