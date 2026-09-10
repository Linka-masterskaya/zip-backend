package storage

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const upsertStorageObject = `
INSERT INTO storage_objects (key, size, content_type, sha256, created_at, updated_at)
VALUES ($1, $2, $3, $4, now(), now())
ON CONFLICT (key) DO UPDATE
SET size = EXCLUDED.size,
    content_type = EXCLUDED.content_type,
    sha256 = EXCLUDED.sha256,
    updated_at = EXCLUDED.updated_at`

const (
	objectLockTimeout      = 5 * time.Second
	objectUploadProtection = 15 * time.Minute
)

type registryExecutor interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

// objectLockID maps an object key to the bigint namespace used by PostgreSQL
// advisory locks. Collisions are cryptographically improbable and, if one did
// occur, would only serialize two unrelated object operations.
func objectLockID(key string) int64 {
	digest := sha256.Sum256([]byte(key))
	// Keep the advisory-lock ID in the non-negative int64 range without a
	// narrowing uint64 -> int64 conversion (gosec G115). Sixty-three hash bits
	// still make accidental collisions negligibly unlikely.
	high := int64(binary.BigEndian.Uint32(digest[:4]) & 0x7fffffff)
	low := int64(binary.BigEndian.Uint32(digest[4:8]))
	return (high << 32) | low
}

// acquireObjectLock serializes lifecycle mutations for one MinIO key across
// PutObject, RemoveObject and the reaper. The lock is session-scoped because a
// MinIO request cannot participate in a PostgreSQL transaction. The returned
// release function always uses a detached timeout so a cancelled request does
// not leak a lock into the pool connection.
func acquireObjectLock(ctx context.Context, pool *pgxpool.Pool, key string) (*pgxpool.Conn, func(), error) {
	if pool == nil {
		return nil, func() {}, nil
	}

	acquireCtx, cancelAcquire := context.WithTimeout(ctx, objectLockTimeout)
	defer cancelAcquire()
	conn, err := pool.Acquire(acquireCtx)
	if err != nil {
		return nil, nil, fmt.Errorf("acquire storage registry connection for %q: %w", key, err)
	}

	lockID := objectLockID(key)
	if _, err = conn.Exec(acquireCtx, `SELECT set_config('lock_timeout', $1, false)`, objectLockTimeout.String()); err != nil {
		// set_config is session-scoped. If the client lost the response after the
		// server applied it, returning this connection to the pool would leak the
		// timeout into an unrelated request. Close the session instead.
		closeCtx, cancel := context.WithTimeout(context.Background(), objectLockTimeout)
		defer cancel()
		raw := conn.Hijack()
		if closeErr := raw.Close(closeCtx); closeErr != nil {
			return nil, nil, fmt.Errorf(
				"configure storage object lock timeout for %q: %w; close hijacked connection: %v",
				key, err, closeErr,
			)
		}
		return nil, nil, fmt.Errorf("configure storage object lock timeout for %q: %w", key, err)
	}
	if _, err = conn.Exec(acquireCtx, `SELECT pg_advisory_lock($1)`, lockID); err != nil {
		// The server may have acquired the session lock before the client observed
		// a cancellation/transport error. Never return that session to the pool.
		closeCtx, cancel := context.WithTimeout(context.Background(), objectLockTimeout)
		defer cancel()
		raw := conn.Hijack()
		if closeErr := raw.Close(closeCtx); closeErr != nil {
			return nil, nil, fmt.Errorf(
				"lock storage object %q: %w; close hijacked connection: %v",
				key, err, closeErr,
			)
		}
		return nil, nil, fmt.Errorf("lock storage object %q: %w", key, err)
	}
	if _, err = conn.Exec(acquireCtx, `SELECT set_config('lock_timeout', '0', false)`); err != nil {
		closeCtx, cancel := context.WithTimeout(context.Background(), objectLockTimeout)
		defer cancel()
		raw := conn.Hijack()
		if closeErr := raw.Close(closeCtx); closeErr != nil {
			return nil, nil, fmt.Errorf(
				"reset storage object lock timeout for %q: %w; close hijacked connection: %v",
				key, err, closeErr,
			)
		}
		return nil, nil, fmt.Errorf("reset storage object lock timeout for %q: %w", key, err)
	}

	released := false
	release := func() {
		if released {
			return
		}
		released = true

		unlockCtx, cancel := context.WithTimeout(context.Background(), objectLockTimeout)
		defer cancel()
		if _, unlockErr := conn.Exec(unlockCtx, `SELECT pg_advisory_unlock($1)`, lockID); unlockErr != nil {
			// A session advisory lock must never be returned to the pool if unlock
			// failed. Hijacking and closing the underlying connection guarantees
			// PostgreSQL releases all session locks held by it.
			raw := conn.Hijack()
			if closeErr := raw.Close(unlockCtx); closeErr != nil {
				slog.WarnContext(
					unlockCtx,
					"close hijacked storage registry connection failed",
					"key", key,
					"err", closeErr,
				)
			}
			return
		}
		conn.Release()
	}

	return conn, release, nil
}

// protectStorageObjectForUpload briefly serializes with a same-key reaper and
// moves updated_at into the near future for an existing row. The advisory lock
// and pool connection are released before MinIO I/O starts, so parallel uploads
// cannot exhaust the PostgreSQL pool. The short lease also closes the race where
// a reaper selected the old row just before the upload began.
func protectStorageObjectForUpload(ctx context.Context, pool *pgxpool.Pool, key string) (bool, error) {
	if pool == nil {
		return false, nil
	}

	conn, releaseLock, err := acquireObjectLock(ctx, pool, key)
	if err != nil {
		return false, err
	}
	defer releaseLock()

	tag, err := conn.Exec(ctx, `
		UPDATE storage_objects
		SET updated_at = GREATEST(updated_at, now() + $2::interval)
		WHERE key = $1
	`, key, objectUploadProtection.String())
	if err != nil {
		return false, fmt.Errorf("protect storage object %q before upload: %w", key, err)
	}
	return tag.RowsAffected() > 0, nil
}

func registryExecutorFor(conn *pgxpool.Conn, pool *pgxpool.Pool) registryExecutor {
	if conn != nil {
		return conn
	}
	if pool != nil {
		return pool
	}
	return nil
}

func registerObject(
	ctx context.Context,
	exec registryExecutor,
	key string,
	size int64,
	contentType, sha256 string,
) error {
	if exec == nil {
		return nil
	}
	if _, err := exec.Exec(ctx, upsertStorageObject, key, size, contentType, sha256); err != nil {
		return fmt.Errorf("register storage object %q: %w", key, err)
	}
	return nil
}

func unregisterObject(ctx context.Context, exec registryExecutor, key string) error {
	if exec == nil {
		return nil
	}
	if _, err := exec.Exec(ctx, `DELETE FROM storage_objects WHERE key = $1`, key); err != nil {
		return fmt.Errorf("unregister storage object %q: %w", key, err)
	}
	return nil
}
