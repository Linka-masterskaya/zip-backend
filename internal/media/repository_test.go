package media

import (
	"database/sql"
	"testing"

	"github.com/Linka-masterskaya/zip-backend/internal/testutil"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRepositoryDeduplicatesQuotaAndProtectsUsages(t *testing.T) {
	pool, cleanup := testutil.NewPostgres(t)
	t.Cleanup(cleanup)
	db := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, applyMediaMigrations(db))

	orgID, userID := uuid.New(), uuid.New()
	_, err := pool.Exec(t.Context(), `
		INSERT INTO organizations (id, name) VALUES ($1, 'media org')`, orgID)
	require.NoError(t, err)
	_, err = pool.Exec(t.Context(), `
		INSERT INTO users (id, org_id) VALUES ($1, $2)`, userID, orgID)
	require.NoError(t, err)

	repo := NewRepository(pool)
	input := File{
		OrgID: orgID, UploaderID: userID, Name: "Кот.png", SHA256: "digest",
		MIMEType: "image/png", SizeBytes: 123, MinIOKey: "media/key",
	}
	first, err := repo.Upsert(t.Context(), input)
	require.NoError(t, err)
	second, err := repo.Upsert(t.Context(), input)
	require.NoError(t, err)
	assert.Equal(t, first.ID, second.ID)
	assert.Equal(t, "Кот.png", first.Name)

	listed, err := repo.ListAccessible(t.Context(), userID, "image", "кот", 100, 0)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, first.ID, listed[0].ID)
	listed, err = repo.ListAccessible(t.Context(), userID, "audio", "", 100, 0)
	require.NoError(t, err)
	assert.Empty(t, listed)
	listed, err = repo.ListAccessible(t.Context(), userID, "image", "собака", 100, 0)
	require.NoError(t, err)
	assert.Empty(t, listed)
	var storageUsed int64
	require.NoError(t, pool.QueryRow(t.Context(), `
		SELECT storage_used_bytes FROM organizations WHERE id = $1`, orgID,
	).Scan(&storageUsed))
	assert.Equal(t, int64(123), storageUsed)

	_, err = pool.Exec(t.Context(), `
		INSERT INTO media_usages (media_id, source_type, source_id)
		VALUES ($1, 'pack', $2)`, first.ID, uuid.New())
	require.NoError(t, err)
	_, err = repo.Delete(t.Context(), userID, first.ID)
	require.ErrorIs(t, err, ErrInUse)
	_, err = pool.Exec(t.Context(), `DELETE FROM media_usages WHERE media_id = $1`, first.ID)
	require.NoError(t, err)
	deleted, err := repo.Delete(t.Context(), userID, first.ID)
	require.NoError(t, err)
	assert.Equal(t, first.ID, deleted.ID)
	require.NoError(t, pool.QueryRow(t.Context(), `
		SELECT storage_used_bytes FROM organizations WHERE id = $1`, orgID,
	).Scan(&storageUsed))
	assert.Zero(t, storageUsed)
}

func applyMediaMigrations(db *sql.DB) error {
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	return goose.Up(db, "../../migrations")
}
