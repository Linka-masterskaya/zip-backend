// Package cache provides Redis client initialization helpers.
package cache

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const emailSentKeyPrefix = "email:sent:"

var (
	// ErrNotFound is returned when a requested Redis record does not exist.
	ErrNotFound = errors.New("redis: key not found")

	// ErrRefreshNotActive is returned when a refresh token has already
	// been used or revoked and therefore cannot be rotated again.
	ErrRefreshNotActive = errors.New("redis: refresh token is not active")

	// ErrSessionRevoked is returned when a refresh token predates the
	// user's latest bulk session revocation.
	ErrSessionRevoked = errors.New("redis: refresh session is revoked")

	// ErrUserSessionsDisabled is returned when account deletion has disabled
	// creation of new sessions for the user.
	ErrUserSessionsDisabled = errors.New("redis: user sessions are disabled")
)

type Config struct {
	URL        string
	ClientName string
	PoolSize   int
}

// Client wraps a Redis connection and provides rate limiting and refresh token storage.
type Client struct {
	rdb *redis.Client
}

// NewClientFromRedis creates 'Client' from 'redis.Client'.
func NewClientFromRedis(rdb *redis.Client) *Client {
	return &Client{rdb: rdb}
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

// storeRefreshForLogin serializes login session issuance with account
// disabling. If deletion wins the race, no refresh session is stored. If login
// wins, DisableUserSessions increments the version afterwards, making the
// freshly-issued session stale before it can be used.
//
// Return values:
// -1 - new sessions are disabled for the user;
// >=0 - the session version stamped into the new refresh record.
var storeRefreshForLogin = redis.NewScript(`
-- KEYS[1]: refresh token key
-- KEYS[2]: refresh family key
-- KEYS[3]: user session version key
-- KEYS[4]: user sessions disabled key

-- ARGV[1]: family ID
-- ARGV[2]: user ID
-- ARGV[3]: TTL in seconds

if redis.call("GET", KEYS[4]) == "1" then
	return -1
end

local current_version = tonumber(redis.call("GET", KEYS[3]) or "0")
redis.call(
	"HSET",
	KEYS[1],
	"fid", ARGV[1],
	"status", "active",
	"user_id", ARGV[2],
	"sess_ver", current_version
)
redis.call("EXPIRE", KEYS[1], ARGV[3])
redis.call("SET", KEYS[2], "active", "EX", ARGV[3])
return current_version
`)

const userSessionsDisableBarrierTTL = 10 * time.Minute

// disableUserSessions atomically prevents future login sessions and advances
// the version that invalidates all access/refresh sessions already issued.
// The barrier has a TTL so a process crash before the PostgreSQL soft-delete
// cannot leave an otherwise-active account permanently locked in Redis.
var disableUserSessions = redis.NewScript(`
-- KEYS[1]: user session version key
-- KEYS[2]: user sessions disabled key
-- ARGV[1]: barrier TTL in seconds
local version = redis.call("INCR", KEYS[1])
redis.call("SET", KEYS[2], "1", "EX", ARGV[1])
return version
`)

// rotateRefresh atomically checks and revokes the old refresh token,
// stores the new token, and extends the refresh-family TTL.
//
// Return values:
// 0 - old refresh record does not exist;
// 1 - rotation completed successfully;
// 2 - old refresh token is no longer active;
// 3 - old refresh token predates the latest bulk session revocation.
var rotateRefresh = redis.NewScript(`
-- KEYS[1]: old refresh key
-- KEYS[2]: new refresh key
-- KEYS[3]: refresh family key
-- KEYS[4]: user session version key

-- ARGV[1]: family ID
-- ARGV[2]: user ID
-- ARGV[3]: TTL in seconds

local status = redis.call("HGET", KEYS[1], "status")

if not status then
	return 0
end

if status ~= "active" then
	return 2
end

local token_version = tonumber(redis.call("HGET", KEYS[1], "sess_ver") or "0")
local current_version = tonumber(redis.call("GET", KEYS[4]) or "0")

if token_version < current_version then
	return 3
end

redis.call(
	"HSET",
	KEYS[1],
	"status", "revoked"
)

redis.call(
	"HSET",
	KEYS[2],
	"fid", ARGV[1],
	"status", "active",
	"user_id", ARGV[2],
	"sess_ver", current_version
)

redis.call("EXPIRE", KEYS[2], ARGV[3])
redis.call("EXPIRE", KEYS[3], ARGV[3])

return 1
`)

var reserveCounterWithTTL = redis.NewScript(`
local current = tonumber(redis.call("GET", KEYS[1]) or "0")
local delta = tonumber(ARGV[1])
local limit = tonumber(ARGV[2])
if delta < 0 or limit < 0 then
  return redis.error_reply("invalid counter arguments")
end
if current + delta > limit then
  return {0, current}
end
local next = redis.call("INCRBY", KEYS[1], delta)
if next == delta then
  redis.call("EXPIRE", KEYS[1], ARGV[3])
end
return {1, next}
`)

var reserveCounterOnceWithTTL = redis.NewScript(`
local current = tonumber(redis.call("GET", KEYS[1]) or "0")
local requested = tonumber(ARGV[1])
local limit = tonumber(ARGV[2])
local ttl = tonumber(ARGV[3])
if requested < 0 or limit < 0 or ttl <= 0 then
  return redis.error_reply("invalid counter arguments")
end
local reserved = tonumber(redis.call("GET", KEYS[2]) or "0")
local delta = requested - reserved
if delta <= 0 then
  return {1, current}
end
if current + delta > limit then
  return {0, current}
end
local next = redis.call("INCRBY", KEYS[1], delta)
if next == delta then
  redis.call("EXPIRE", KEYS[1], ttl)
end
redis.call("SET", KEYS[2], requested, "EX", ttl)
return {1, next}
`)

// ReserveCounter atomically adds delta only when the resulting value does not exceed limit.
func (c *Client) ReserveCounter(ctx context.Context, key string, delta, limit int64, ttl time.Duration) (bool, int64, error) {
	if delta < 0 || limit < 0 {
		return false, 0, fmt.Errorf("redis.ReserveCounter: invalid counter arguments")
	}
	result, err := reserveCounterWithTTL.Run(ctx, c.rdb, []string{key}, delta, limit, int(ttl.Seconds())).Slice()
	if err != nil {
		return false, 0, fmt.Errorf("redis.ReserveCounter: %w", err)
	}
	if len(result) != 2 {
		return false, 0, fmt.Errorf("redis.ReserveCounter: unexpected result")
	}
	allowed, ok := result[0].(int64)
	if !ok {
		return false, 0, fmt.Errorf("redis.ReserveCounter: invalid allowed result")
	}
	value, ok := result[1].(int64)
	if !ok {
		return false, 0, fmt.Errorf("redis.ReserveCounter: invalid value result")
	}
	return allowed == 1, value, nil
}

// ReserveCounterOnce atomically reserves a per-reservation amount against a shared counter.
// Repeating the same reservation key does not charge the same amount twice; if the
// requested amount grows, only the positive difference is added.
func (c *Client) ReserveCounterOnce(
	ctx context.Context,
	counterKey, reservationKey string,
	amount, limit int64,
	ttl time.Duration,
) (bool, int64, error) {
	if amount < 0 || limit < 0 || ttl <= 0 {
		return false, 0, fmt.Errorf("redis.ReserveCounterOnce: invalid counter arguments")
	}
	result, err := reserveCounterOnceWithTTL.Run(
		ctx, c.rdb, []string{counterKey, reservationKey}, amount, limit, int(ttl.Seconds()),
	).Slice()
	if err != nil {
		return false, 0, fmt.Errorf("redis.ReserveCounterOnce: %w", err)
	}
	if len(result) != 2 {
		return false, 0, fmt.Errorf("redis.ReserveCounterOnce: unexpected result")
	}
	allowed, ok := result[0].(int64)
	if !ok {
		return false, 0, fmt.Errorf("redis.ReserveCounterOnce: invalid allowed result")
	}
	value, ok := result[1].(int64)
	if !ok {
		return false, 0, fmt.Errorf("redis.ReserveCounterOnce: invalid value result")
	}
	return allowed == 1, value, nil
}

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
	FID            string `redis:"fid"`
	Status         string `redis:"status"`
	UserID         string `redis:"user_id"`
	SessionVersion int64  `redis:"sess_ver"`
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

// GetUserSessionVersion returns the current session-version counter for a
// user, or 0 if the user has never had their sessions bulk-revoked.
func (c *Client) GetUserSessionVersion(ctx context.Context, userID string) (int64, error) {
	val, err := c.getString(ctx, "user_session_version:"+userID)
	if errors.Is(err, ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("redis.GetUserSessionVersion: %w", err)
	}
	version, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("redis.GetUserSessionVersion: parse: %w", err)
	}
	return version, nil
}

// The IsSessionRevoked function reports if the record is no longer valid: either its family
// has been revoked individually (RevokeFamily), or it was released before the last
// mass revocation of user sessions (RevokeAllSessions).
func (c *Client) IsSessionRevoked(ctx context.Context, rec RefreshRecord) (bool, error) {
	familyRevoked, err := c.IsFamilyRevoked(ctx, rec.FID)
	if err != nil {
		return false, fmt.Errorf("redis.IsSessionRevoked: %w", err)
	}
	if familyRevoked {
		return true, nil
	}

	version, err := c.GetUserSessionVersion(ctx, rec.UserID)
	if err != nil {
		return false, fmt.Errorf("redis.IsSessionRevoked: %w", err)
	}
	return rec.SessionVersion < version, nil
}

// StoreRefresh saves a refresh token for non-login callers. It is kept as a
// compatibility wrapper around StoreRefreshForLogin.
func (c *Client) StoreRefresh(ctx context.Context, jti string, rec RefreshRecord, ttl time.Duration) error {
	_, err := c.StoreRefreshForLogin(ctx, jti, rec, ttl)
	return err
}

// StoreRefreshForLogin atomically checks that account deletion has not disabled
// new sessions, stores the refresh record, and returns the exact session
// version stamped into it. The returned version must be used for the access JWT.
func (c *Client) StoreRefreshForLogin(
	ctx context.Context,
	jti string,
	rec RefreshRecord,
	ttl time.Duration,
) (int64, error) {
	tokenKey := "refresh:" + jti
	familyKey := "refresh_family:" + rec.FID
	sessionVersionKey := "user_session_version:" + rec.UserID
	disabledKey := "user_sessions_disabled:" + rec.UserID

	version, err := storeRefreshForLogin.Run(
		ctx,
		c.rdb,
		[]string{tokenKey, familyKey, sessionVersionKey, disabledKey},
		rec.FID,
		rec.UserID,
		int64(ttl.Seconds()),
	).Int64()
	if err != nil {
		return 0, fmt.Errorf("redis.StoreRefreshForLogin: %w", err)
	}
	if version == -1 {
		return 0, ErrUserSessionsDisabled
	}
	return version, nil
}

// RevokeAllSessions advances the user's session generation. Refresh records
// from older generations become stale, and access-token middleware rejects JWTs
// carrying an older sess_ver claim. It does not prevent future logins.
func (c *Client) RevokeAllSessions(ctx context.Context, userID string) error {
	if err := c.rdb.Incr(ctx, "user_session_version:"+userID).Err(); err != nil {
		return fmt.Errorf("redis.RevokeAllSessions: %w", err)
	}
	return nil
}

// DisableUserSessions atomically blocks future login session creation and
// invalidates every session issued before this call. It is used by soft-delete.
func (c *Client) DisableUserSessions(ctx context.Context, userID string) error {
	_, err := disableUserSessions.Run(
		ctx,
		c.rdb,
		[]string{"user_session_version:" + userID, "user_sessions_disabled:" + userID},
		int64(userSessionsDisableBarrierTTL.Seconds()),
	).Int64()
	if err != nil {
		return fmt.Errorf("redis.DisableUserSessions: %w", err)
	}
	return nil
}

// EnableUserSessions removes the deletion barrier after a failed database
// soft-delete. The already-advanced session version is intentionally retained,
// so sessions that were revoked while attempting deletion do not become valid
// again.
func (c *Client) EnableUserSessions(ctx context.Context, userID string) error {
	if err := c.rdb.Del(ctx, "user_sessions_disabled:"+userID).Err(); err != nil {
		return fmt.Errorf("redis.EnableUserSessions: %w", err)
	}
	return nil
}

// RotateRefresh atomically checks and revokes the old refresh token,
// stores the new token, and extends the family TTL.
func (c *Client) RotateRefresh(ctx context.Context, req RotateRefreshRequest) error {
	oldKey := "refresh:" + req.OldJTI
	newKey := "refresh:" + req.NewJTI
	familyKey := "refresh_family:" + req.NewRecord.FID
	sessionVersionKey := "user_session_version:" + req.NewRecord.UserID

	result, err := rotateRefresh.Run(
		ctx,
		c.rdb,
		[]string{
			oldKey,
			newKey,
			familyKey,
			sessionVersionKey,
		},
		req.NewRecord.FID,
		req.NewRecord.UserID,
		int64(req.TTL.Seconds()),
	).Int()
	if err != nil {
		return fmt.Errorf("redis.RotateRefresh: execute script: %w", err)
	}

	switch result {
	case 0:
		return ErrNotFound
	case 1:
		return nil
	case 2:
		return ErrRefreshNotActive
	case 3:
		return ErrSessionRevoked
	default:
		return fmt.Errorf(
			"redis.RotateRefresh: unexpected script result: %d",
			result,
		)
	}
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

// emailSentKey returns the Redis key for today's email counter.
func emailSentKey() string {
	today := time.Now().Format("2006-01-02")
	return emailSentKeyPrefix + today
}

// secondsUntilMidnight returns the number of seconds until the next midnight.
func secondsUntilMidnight() int64 {
	now := time.Now()
	tomorrow := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
	return int64(tomorrow.Sub(now).Seconds())
}

// IncrEmailSentToday atomically increments today's email counter and sets TTL until midnight.
func (c *Client) IncrEmailSentToday(ctx context.Context) (int64, error) {
	key := emailSentKey()
	ttl := time.Duration(secondsUntilMidnight()) * time.Second
	if ttl <= 0 {
		ttl = time.Second
	}

	count, err := c.IncrCounter(ctx, key, ttl)
	if err != nil {
		return 0, fmt.Errorf("redis.IncrEmailSentToday: %w", err)
	}
	return count, nil
}

// GetEmailSentToday returns today's email counter value, or 0 if no key exists.
func (c *Client) GetEmailSentToday(ctx context.Context) (int64, error) {
	key := emailSentKey()
	val, err := c.rdb.Get(ctx, key).Int64()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("redis.GetEmailSentToday: %w", err)
	}
	return val, nil
}

// ResetEmailSentToday deletes today's email counter key.
func (c *Client) ResetEmailSentToday(ctx context.Context) error {
	key := emailSentKey()
	if err := c.rdb.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("redis.ResetEmailSentToday: %w", err)
	}
	return nil
}

// GetTTL returns the TTL of a key for testing purposes.
func (c *Client) GetTTL(ctx context.Context, key string) (time.Duration, error) {
	ttl, err := c.rdb.TTL(ctx, key).Result()
	if err != nil {
		return 0, fmt.Errorf("redis.GetTTL: %w", err)
	}
	return ttl, nil
}
