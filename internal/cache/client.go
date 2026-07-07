// Package cache provides Redis client initialization helpers.
package cache

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrNotFound - sentinel error returned by Client lookups.
var ErrNotFound = errors.New("redis: key not found")

type Config struct {
	URL        string
	ClientName string
	PoolSize   int
}

// Client wraps a Redis connection and provides rate limiting and refresh token storage.
type Client struct {
	rdb *redis.Client
}

// NewClient connects to Redis and verifies the connection with a ping.
func NewClient(cfg Config) (*Client, error) {
	options, err := redis.ParseURL(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	options.ClientName = cfg.ClientName

	options.ReadTimeout = 500 * time.Millisecond
	options.WriteTimeout = 500 * time.Millisecond
	options.DialTimeout = 2 * time.Second

	options.MaxRetries = 3
	options.MinRetryBackoff = 8 * time.Millisecond
	options.MaxRetryBackoff = 512 * time.Millisecond

	options.PoolSize = cfg.PoolSize
	options.MaxActiveConns = cfg.PoolSize * 2
	options.MinIdleConns = 2
	options.ConnMaxIdleTime = 5 * time.Minute

	options.ContextTimeoutEnabled = true

	rdb := redis.NewClient(options)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping: %w", err)
	}

	slog.Info("redis client created",
		"addr", options.Addr, // host:port без пароля, ParseURL заполняет
		"db", options.DB,
		"pool_size", options.PoolSize,
	)

	return &Client{rdb: rdb}, nil
}

// RateLimitRequest defines a fixed-window rate limit check (rl:{scope}:{key}).
type RateLimitRequest struct {
	Scope      string
	Key        string
	Limit      int64
	WindowSize time.Duration
}

// incrWithTTL atomically increments key and sets TTL on first increment.
var incrWithTTL = redis.NewScript(`
-- KEYS[1]: counter key
-- ARGV[1]: ttl in seconds
local count = redis.call("INCR", KEYS[1])
if count == 1 then
    redis.call("EXPIRE", KEYS[1], ARGV[1])
end
return count
`)

// IncrCounter atomically increments key and sets ttl on first increment via Lua.
func (c *Client) IncrCounter(ctx context.Context, key string, ttl time.Duration) (int64, error) {
	count, err := incrWithTTL.Run(ctx, c.rdb, []string{key}, int(ttl.Seconds())).Int64()
	if err != nil {
		return 0, fmt.Errorf("redis.IncrCounter: %w", err)
	}
	return count, nil
}

// Allow reports whether the request is within its rate limit.
func (c *Client) Allow(ctx context.Context, req RateLimitRequest) (bool, int64, error) {
	key := fmt.Sprintf("rl:%s:%s", req.Scope, req.Key)
	count, err := c.IncrCounter(ctx, key, req.WindowSize)
	if err != nil {
		return false, 0, err
	}

	if count > req.Limit {
		ttl, err := c.rdb.TTL(ctx, key).Result()
		if err != nil {
			return false, 0, fmt.Errorf("redis ttl check failed: %w", err)
		}

		seconds := int64(ttl.Seconds())
		if seconds < 1 {
			seconds = 1
		}
		return false, seconds, nil
	}

	return true, 0, nil
}

// RefreshRecord is a refresh token stored as a Redis hash under refresh:{jti}.
type RefreshRecord struct {
	FID    string `redis:"fid"`
	Status string `redis:"status"`
}

// RotateRefreshRequest carries data to rotate a refresh token atomically.
type RotateRefreshRequest struct {
	OldJTI    string
	NewJTI    string
	NewRecord RefreshRecord
	TTL       time.Duration
}

// GetRefresh returns the refresh token for jti, or ErrNotFound.
func (c *Client) GetRefresh(ctx context.Context, jti string) (*RefreshRecord, error) {
	var rec RefreshRecord
	if err := c.getHash(ctx, "refresh:"+jti, &rec); err != nil {
		return nil, fmt.Errorf("redis.GetRefresh: %w", err)
	}
	return &rec, nil
}

// IsFamilyRevoked reports whether the token family is revoked.
// fail-closed: missing = revoked, Redis error returns false + err for caller to handle.
func (c *Client) IsFamilyRevoked(ctx context.Context, fid string) (bool, error) {
	status, err := c.getString(ctx, "refresh_family:"+fid)
	if errors.Is(err, ErrNotFound) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("redis.IsFamilyRevoked: %w", err)
	}
	return status == "revoked", nil
}

// RevokeFamily marks the family revoked, keeping its TTL.
func (c *Client) RevokeFamily(ctx context.Context, fid string) error {
	if err := c.setString(ctx, "refresh_family:"+fid, "revoked", redis.KeepTTL); err != nil {
		return fmt.Errorf("redis.RevokeFamily: %w", err)
	}
	return nil
}

// StoreRefresh saves a refresh token and marks its family active, with ttl.
func (c *Client) StoreRefresh(ctx context.Context, jti string, rec RefreshRecord, ttl time.Duration) error {
	tokenKey := "refresh:" + jti
	familyKey := "refresh_family:" + rec.FID

	_, err := c.rdb.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.HSet(ctx, tokenKey, rec)
		pipe.Expire(ctx, tokenKey, ttl)
		pipe.Set(ctx, familyKey, "active", ttl)
		return nil
	})
	if err != nil {
		return fmt.Errorf("redis.StoreRefresh: %w", err)
	}
	return nil
}

// RotateRefresh atomically revokes the old token, stores the new one, and extends the family TTL.
func (c *Client) RotateRefresh(ctx context.Context, req RotateRefreshRequest) error {
	oldKey := "refresh:" + req.OldJTI
	newKey := "refresh:" + req.NewJTI
	familyKey := "refresh_family:" + req.NewRecord.FID

	_, err := c.rdb.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.HSet(ctx, oldKey, "status", "revoked")
		pipe.HSet(ctx, newKey, req.NewRecord)
		pipe.Expire(ctx, newKey, req.TTL)
		pipe.Expire(ctx, familyKey, req.TTL)
		return nil
	})
	if err != nil {
		return fmt.Errorf("redis.RotateRefresh: %w", err)
	}
	return nil
}

// getHash loads the hash at key into dest, or returns ErrNotFound.
func (c *Client) getHash(ctx context.Context, key string, dest any) error {
	res := c.rdb.HGetAll(ctx, key)
	if err := res.Err(); err != nil {
		return fmt.Errorf("redis.getHash: %w", err)
	}
	if len(res.Val()) == 0 {
		return ErrNotFound
	}
	if err := res.Scan(dest); err != nil {
		return fmt.Errorf("redis.getHash: scan: %w", err)
	}
	return nil
}

// setString stores val at key with ttl.
func (c *Client) setString(ctx context.Context, key, val string, ttl time.Duration) error {
	if err := c.rdb.Set(ctx, key, val, ttl).Err(); err != nil {
		return fmt.Errorf("redis.setString: %w", err)
	}
	return nil
}

// getString returns the value at key, or ErrNotFound.
func (c *Client) getString(ctx context.Context, key string) (string, error) {
	val, err := c.rdb.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("redis.getString: %w", err)
	}
	return val, nil
}

// Ping checks Redis connectivity for readiness probes.
func (c *Client) Ping(ctx context.Context) error {
	if err := c.rdb.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("cache.Ping: %w", err)
	}
	return nil
}

// Close closes the Redis client connection.
func (c *Client) Close() error {
	if c.rdb != nil {
		return c.rdb.Close()
	}
	return nil
}
