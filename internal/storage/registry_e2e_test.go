//go:build e2e

package storage_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Linka-masterskaya/zip-backend/internal/storage"
	"github.com/Linka-masterskaya/zip-backend/internal/testutil"
	"github.com/Linka-masterskaya/zip-backend/migrations"
)

func TestE2EStorageRegistryLifecycle(t *testing.T) {
	pool, cleanupDB := testutil.NewPostgres(t)
	t.Cleanup(cleanupDB)

	sqlDB := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, migrations.Run(sqlDB))

	objectStorage, cleanupStorage := testutil.NewMinIO(t, pool)
	t.Cleanup(cleanupStorage)

	ctx := t.Context()
	payload := []byte("storage-registry-payload")
	key := "tests/storage-registry/lifecycle"
	contentType := "application/octet-stream"

	require.NoError(t, objectStorage.PutObject(ctx, key, bytes.NewReader(payload), int64(len(payload)), contentType))

	var size int64
	var gotContentType, gotSHA string
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT size, content_type, sha256
		FROM storage_objects
		WHERE key = $1
	`, key).Scan(&size, &gotContentType, &gotSHA))

	expectedHash := sha256.Sum256(payload)
	assert.Equal(t, int64(len(payload)), size)
	assert.Equal(t, contentType, gotContentType)
	assert.Equal(t, hex.EncodeToString(expectedHash[:]), gotSHA)

	require.NoError(t, objectStorage.RemoveObject(ctx, key))

	var registered bool
	require.NoError(t, pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM storage_objects WHERE key = $1)`, key).Scan(&registered))
	assert.False(t, registered)
	_, err := objectStorage.ObjectSize(ctx, key)
	assert.ErrorIs(t, err, storage.ErrObjectNotFound)
}

func TestE2EStorageReaperDeletesUnreferencedObject(t *testing.T) {
	pool, cleanupDB := testutil.NewPostgres(t)
	t.Cleanup(cleanupDB)

	sqlDB := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, migrations.Run(sqlDB))

	objectStorage, cleanupStorage := testutil.NewMinIO(t, pool)
	t.Cleanup(cleanupStorage)

	ctx := t.Context()
	payload := []byte("orphan")
	key := "tests/storage-registry/orphan"
	require.NoError(t, objectStorage.PutObject(ctx, key, bytes.NewReader(payload), int64(len(payload)), "application/octet-stream"))

	removed, err := objectStorage.ReapUnreferenced(ctx, 0, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, removed)

	var registered bool
	require.NoError(t, pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM storage_objects WHERE key = $1)`, key).Scan(&registered))
	assert.False(t, registered)
	_, err = objectStorage.ObjectSize(ctx, key)
	assert.ErrorIs(t, err, storage.ErrObjectNotFound)
}

func TestE2EStoragePutCompensatesRegistryFailure(t *testing.T) {
	pool, cleanupDB := testutil.NewPostgres(t)
	t.Cleanup(cleanupDB)

	sqlDB := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, migrations.Run(sqlDB))

	objectStorage, cleanupStorage := testutil.NewMinIO(t, pool)
	t.Cleanup(cleanupStorage)

	ctx := t.Context()
	require.NoError(t, execSQL(ctx, pool, `
		CREATE FUNCTION fail_storage_object_write() RETURNS trigger AS $$
		BEGIN
			RAISE EXCEPTION 'forced storage_objects write failure';
		END;
		$$ LANGUAGE plpgsql
	`))
	require.NoError(t, execSQL(ctx, pool, `
		CREATE TRIGGER storage_objects_fail_write
		BEFORE INSERT OR UPDATE ON storage_objects
		FOR EACH ROW EXECUTE FUNCTION fail_storage_object_write()
	`))

	payload := []byte("registry-failure")
	key := "tests/storage-registry/put-compensation"
	err := objectStorage.PutObject(ctx, key, bytes.NewReader(payload), int64(len(payload)), "application/octet-stream")
	require.Error(t, err)

	_, err = objectStorage.ObjectSize(ctx, key)
	assert.ErrorIs(t, err, storage.ErrObjectNotFound)

	var registered bool
	require.NoError(t, pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM storage_objects WHERE key = $1)`, key).Scan(&registered))
	assert.False(t, registered)
}

func TestE2EStoragePutPreservesPreexistingSharedKeyOnRegistryFailure(t *testing.T) {
	pool, cleanupDB := testutil.NewPostgres(t)
	t.Cleanup(cleanupDB)

	sqlDB := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, migrations.Run(sqlDB))

	objectStorage, cleanupStorage := testutil.NewMinIO(t, pool)
	t.Cleanup(cleanupStorage)

	ctx := t.Context()
	payload := []byte("shared-tts-like-object")
	key := "tests/storage-registry/shared-key"
	require.NoError(t, objectStorage.PutObject(ctx, key, bytes.NewReader(payload), int64(len(payload)), "audio/mpeg"))

	require.NoError(t, execSQL(ctx, pool, `
		CREATE FUNCTION fail_storage_object_write_existing() RETURNS trigger AS $$
		BEGIN
			RAISE EXCEPTION 'forced existing storage_objects write failure';
		END;
		$$ LANGUAGE plpgsql
	`))
	require.NoError(t, execSQL(ctx, pool, `
		CREATE TRIGGER storage_objects_fail_write_existing
		BEFORE INSERT OR UPDATE ON storage_objects
		FOR EACH ROW EXECUTE FUNCTION fail_storage_object_write_existing()
	`))

	err := objectStorage.PutObject(ctx, key, bytes.NewReader(payload), int64(len(payload)), "audio/mpeg")
	require.Error(t, err)

	// A failed registry refresh must not compensate by deleting a key that was
	// already registered and may still be referenced by other organizations.
	size, err := objectStorage.ObjectSize(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, int64(len(payload)), size)

	var registered bool
	require.NoError(t, pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM storage_objects WHERE key = $1)`, key).Scan(&registered))
	assert.True(t, registered)
}

func TestE2EStorageRemoveDoesNotRollbackAfterRegistryFailure(t *testing.T) {
	pool, cleanupDB := testutil.NewPostgres(t)
	t.Cleanup(cleanupDB)

	sqlDB := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, migrations.Run(sqlDB))

	objectStorage, cleanupStorage := testutil.NewMinIO(t, pool)
	t.Cleanup(cleanupStorage)

	ctx := t.Context()
	payload := []byte("remove-registry-failure")
	key := "tests/storage-registry/remove-recovery"
	require.NoError(t, objectStorage.PutObject(ctx, key, bytes.NewReader(payload), int64(len(payload)), "application/octet-stream"))

	require.NoError(t, execSQL(ctx, pool, `
		CREATE FUNCTION fail_storage_object_delete() RETURNS trigger AS $$
		BEGIN
			RAISE EXCEPTION 'forced storage_objects delete failure';
		END;
		$$ LANGUAGE plpgsql
	`))
	require.NoError(t, execSQL(ctx, pool, `
		CREATE TRIGGER storage_objects_fail_delete
		BEFORE DELETE ON storage_objects
		FOR EACH ROW EXECUTE FUNCTION fail_storage_object_delete()
	`))

	// The physical delete succeeded, so callers must be allowed to continue
	// their domain/quota cleanup even when registry maintenance is temporarily
	// unavailable.
	require.NoError(t, objectStorage.RemoveObject(ctx, key))
	_, err := objectStorage.ObjectSize(ctx, key)
	assert.ErrorIs(t, err, storage.ErrObjectNotFound)

	var registered bool
	require.NoError(t, pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM storage_objects WHERE key = $1)`, key).Scan(&registered))
	assert.True(t, registered, "failed registry cleanup should leave a row for the reaper")

	require.NoError(t, execSQL(ctx, pool, `DROP TRIGGER storage_objects_fail_delete ON storage_objects`))
	require.NoError(t, execSQL(ctx, pool, `DROP FUNCTION fail_storage_object_delete()`))

	removed, err := objectStorage.ReapUnreferenced(ctx, 0, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, removed)
	require.NoError(t, pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM storage_objects WHERE key = $1)`, key).Scan(&registered))
	assert.False(t, registered)
}

func TestE2EStoragePutSerializesByObjectKey(t *testing.T) {
	pool, cleanupDB := testutil.NewPostgres(t)
	t.Cleanup(cleanupDB)

	sqlDB := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, migrations.Run(sqlDB))

	objectStorage, cleanupStorage := testutil.NewMinIO(t, pool)
	t.Cleanup(cleanupStorage)

	ctx := t.Context()
	key := "tests/storage-registry/advisory-lock"
	lockHash := sha256.Sum256([]byte(key))
	lockID := int64(binary.BigEndian.Uint64(lockHash[:8]))

	conn, err := pool.Acquire(ctx)
	require.NoError(t, err)
	defer conn.Release()
	require.NoError(t, execConnSQL(ctx, conn, `SELECT pg_advisory_lock($1)`, lockID))

	payload := []byte("serialized")
	started := make(chan struct{})
	putErr := make(chan error, 1)
	go func() {
		close(started)
		putErr <- objectStorage.PutObject(ctx, key, bytes.NewReader(payload), int64(len(payload)), "application/octet-stream")
	}()
	<-started

	select {
	case err = <-putErr:
		require.Failf(t, "PutObject completed while key lock was held", "err=%v", err)
	case <-time.After(150 * time.Millisecond):
	}

	require.NoError(t, execConnSQL(ctx, conn, `SELECT pg_advisory_unlock($1)`, lockID))
	select {
	case err = <-putErr:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		require.Fail(t, "PutObject did not resume after key lock was released")
	}
}

func execSQL(ctx context.Context, pool *pgxpool.Pool, sql string) error {
	_, err := pool.Exec(ctx, sql)
	return err
}

func execConnSQL(ctx context.Context, conn *pgxpool.Conn, sql string, args ...any) error {
	_, err := conn.Exec(ctx, sql, args...)
	return err
}
