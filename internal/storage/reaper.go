package storage

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const selectUnreferencedObjects = `
SELECT so.key
FROM storage_objects so
WHERE so.updated_at < $1
  AND NOT EXISTS (SELECT 1 FROM media_files mf WHERE mf.minio_key = so.key)
  AND NOT EXISTS (SELECT 1 FROM audio_bank ab WHERE ab.minio_key = so.key)
  AND NOT EXISTS (SELECT 1 FROM picture_bank_images pbi WHERE pbi.minio_key = so.key)
  AND NOT EXISTS (SELECT 1 FROM users u WHERE u.avatar_key = so.key)
  AND NOT EXISTS (SELECT 1 FROM avatar_cleanup_jobs acj WHERE acj.object_key = so.key AND acj.completed_at IS NULL)
ORDER BY so.updated_at ASC, so.key ASC
LIMIT $2`

const storageObjectStillUnreferenced = `
SELECT EXISTS (
    SELECT 1
    FROM storage_objects so
    WHERE so.key = $1
      AND so.updated_at < $2
      AND NOT EXISTS (SELECT 1 FROM media_files mf WHERE mf.minio_key = so.key)
      AND NOT EXISTS (SELECT 1 FROM audio_bank ab WHERE ab.minio_key = so.key)
      AND NOT EXISTS (SELECT 1 FROM picture_bank_images pbi WHERE pbi.minio_key = so.key)
      AND NOT EXISTS (SELECT 1 FROM users u WHERE u.avatar_key = so.key)
      AND NOT EXISTS (SELECT 1 FROM avatar_cleanup_jobs acj WHERE acj.object_key = so.key AND acj.completed_at IS NULL)
)`

func validateReaperArgs(c *Client, gracePeriod time.Duration, limit int) error {
	switch {
	case c == nil || c.client == nil:
		return errors.New("minio client is not initialized")
	case c.registry == nil:
		return errors.New("storage object registry is not initialized")
	case limit <= 0:
		return errors.New("reaper limit must be positive")
	case gracePeriod < 0:
		return errors.New("reaper grace period must be non-negative")
	default:
		return nil
	}
}

// ReapUnreferenced removes old registry objects that are no longer referenced
// by any domain table. It continues after individual MinIO failures so one bad
// key does not block the rest of the batch. RemoveObject removes the registry
// row only after MinIO accepted the deletion, making retries idempotent.
func (c *Client) ReapUnreferenced(ctx context.Context, gracePeriod time.Duration, limit int) (int, error) {
	if err := validateReaperArgs(c, gracePeriod, limit); err != nil {
		return 0, err
	}

	cutoff := time.Now().Add(-gracePeriod)
	rows, err := c.registry.Query(ctx, selectUnreferencedObjects, cutoff, limit)
	if err != nil {
		return 0, fmt.Errorf("select unreferenced storage objects: %w", err)
	}
	defer rows.Close()

	keys := make([]string, 0, limit)
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			return 0, fmt.Errorf("scan unreferenced storage object: %w", err)
		}
		keys = append(keys, key)
	}
	if err = rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate unreferenced storage objects: %w", err)
	}
	// Release the query connection before acquiring per-key advisory-lock
	// connections. This also avoids pool starvation when MaxConns is small.
	rows.Close()

	removed := 0
	var reapErr error
	for _, key := range keys {
		// Serialize the final reference check and physical deletion against
		// PutObject/RemoveObject for the same key. Without this lock a writer could
		// refresh the object after the recheck but before MinIO deletion.
		registryConn, releaseLock, lockErr := acquireObjectLock(ctx, c.registry, key)
		if lockErr != nil {
			reapErr = errors.Join(reapErr, lockErr)
			continue
		}

		var stillUnreferenced bool
		err = registryConn.QueryRow(ctx, storageObjectStillUnreferenced, key, cutoff).Scan(&stillUnreferenced)
		if err != nil {
			releaseLock()
			reapErr = errors.Join(reapErr, fmt.Errorf("recheck storage object %q: %w", key, err))
			continue
		}
		if !stillUnreferenced {
			releaseLock()
			continue
		}

		blobDeleted, removeErr := c.removeObjectLocked(ctx, key, registryConn)
		releaseLock()
		if removeErr != nil {
			reapErr = errors.Join(reapErr, fmt.Errorf("reap storage object %q: %w", key, removeErr))
			continue
		}
		if blobDeleted {
			removed++
		}
	}
	return removed, reapErr
}
