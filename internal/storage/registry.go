package storage

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
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

const objectLockTimeout = 5 * time.Second

type registryExecutor interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

// objectLockID maps an object key to the bigint namespace used by PostgreSQL
// advisory locks. Collisions are cryptographically improbable and, if one did
// occur, would only serialize two unrelated object operations.
func objectLockID(key string) int64 {
	digest := sha256.Sum256([]byte(key))
	return int64(binary.BigEndian.Uint64(digest[:8]))
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

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("acquire storage registry connection for %q: %w", key, err)
	}

	lockID := objectLockID(key)
	if _, err = conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, lockID); err != nil {
		// The server may have acquired the session lock before the client observed
		// a cancellation/transport error. Never return that session to the pool.
		closeCtx, cancel := context.WithTimeout(context.Background(), objectLockTimeout)
		defer cancel()
		raw := conn.Hijack()
		_ = raw.Close(closeCtx)
		return nil, nil, fmt.Errorf("lock storage object %q: %w", key, err)
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
			_ = raw.Close(unlockCtx)
			return
		}
		conn.Release()
	}

	return conn, release, nil
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
