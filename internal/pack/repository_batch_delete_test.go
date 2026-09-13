package pack

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRepositoryDeleteBatchSkipsPublishedAndForeign: пачка не падает целиком
// из-за одного элемента. Опубликованный набор остаётся на месте, чужой не
// виден вовсе, остальные уходят вместе со ссылками на медиа.
func TestRepositoryDeleteBatchSkipsPublishedAndForeign(t *testing.T) {
	pool := newPackTestDB(t)
	repo := NewRepository(pool)
	orgID, ownerID, folderID := seedPackOwner(t, pool, "batch delete org")
	libraryFolderID := seedPackLibraryFolder(t, pool, ownerID)
	foreignOrgID, foreignID, foreignFolderID := seedPackOwner(t, pool, "foreign batch org")
	config := []byte(`{"metadata":{"version":"2.0"},"settings":{"columns":1,"rows":1},"blocks":[]}`)

	first, err := repo.Create(t.Context(), ownerID, CreateInput{
		Title: "First", FolderID: folderID, Config: config,
	})
	require.NoError(t, err)
	second, err := repo.Create(t.Context(), ownerID, CreateInput{
		Title: "Second", FolderID: folderID, Config: config,
	})
	require.NoError(t, err)
	published, err := repo.Create(t.Context(), ownerID, CreateInput{
		Title: "Published", FolderID: folderID, Config: config,
	})
	require.NoError(t, err)
	_, err = repo.Publish(t.Context(), ownerID, published.ID, libraryFolderID, false)
	require.NoError(t, err)
	foreignPack, err := repo.Create(t.Context(), foreignID, CreateInput{
		Title: "Foreign", FolderID: foreignFolderID, Config: config,
	})
	require.NoError(t, err)

	mediaID := seedPackMedia(t, pool, orgID, ownerID, "batch.png", 8)
	_, err = pool.Exec(t.Context(), `
		INSERT INTO media_usages (media_id, source_type, source_id)
		VALUES ($1, 'pack', $2)`, mediaID, first.ID)
	require.NoError(t, err)
	_, err = pool.Exec(t.Context(),
		`UPDATE organizations SET storage_used_bytes = 8 WHERE id = $1`, orgID)
	require.NoError(t, err)

	missingID := uuid.New()
	ids := []uuid.UUID{first.ID, second.ID, published.ID, foreignPack.ID, missingID}

	dry, err := repo.DeleteBatch(t.Context(), ownerID, ids, true)
	require.NoError(t, err)
	assert.ElementsMatch(t, []uuid.UUID{first.ID, second.ID}, dry.Deleted)
	assert.Equal(t, []uuid.UUID{published.ID}, dry.Published)
	assert.Equal(t, 4, countPacks(t, pool))

	outcome, err := repo.DeleteBatch(t.Context(), ownerID, ids, false)
	require.NoError(t, err)
	assert.ElementsMatch(t, []uuid.UUID{first.ID, second.ID}, outcome.Deleted)
	assert.Equal(t, []uuid.UUID{published.ID}, outcome.Published)

	assert.Equal(t, 2, countPacks(t, pool))
	var stillThere int
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM packs WHERE id = ANY($1)`,
		[]uuid.UUID{published.ID, foreignPack.ID}).Scan(&stillThere))
	assert.Equal(t, 2, stillThere)

	// Медиа первого набора больше ничем не занято, значит ссылка и квота
	// освобождаются тем же путём, что и при одиночном удалении.
	var mediaCount int
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM media_files WHERE id = $1`, mediaID).Scan(&mediaCount))
	assert.Equal(t, 0, mediaCount)

	var used int64
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT storage_used_bytes FROM organizations WHERE id = $1`, orgID).Scan(&used))
	assert.Equal(t, int64(0), used)

	var foreignUsed int64
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT storage_used_bytes FROM organizations WHERE id = $1`, foreignOrgID).Scan(&foreignUsed))
	assert.Equal(t, int64(0), foreignUsed)
}

// TestRepositoryDeleteBatchKeepsSharedMedia: файл, который остаётся нужен
// уцелевшему набору, из библиотеки не пропадает.
func TestRepositoryDeleteBatchKeepsSharedMedia(t *testing.T) {
	pool := newPackTestDB(t)
	repo := NewRepository(pool)
	orgID, ownerID, folderID := seedPackOwner(t, pool, "batch shared media org")
	config := []byte(`{"metadata":{"version":"2.0"},"settings":{"columns":1,"rows":1},"blocks":[]}`)

	deleted, err := repo.Create(t.Context(), ownerID, CreateInput{
		Title: "Deleted", FolderID: folderID, Config: config,
	})
	require.NoError(t, err)
	kept, err := repo.Create(t.Context(), ownerID, CreateInput{
		Title: "Kept", FolderID: folderID, Config: config,
	})
	require.NoError(t, err)

	mediaID := seedPackMedia(t, pool, orgID, ownerID, "shared-batch.png", 8)
	_, err = pool.Exec(t.Context(), `
		INSERT INTO media_usages (media_id, source_type, source_id)
		VALUES ($1, 'pack', $2), ($1, 'pack', $3)`, mediaID, deleted.ID, kept.ID)
	require.NoError(t, err)
	_, err = pool.Exec(t.Context(),
		`UPDATE organizations SET storage_used_bytes = 8 WHERE id = $1`, orgID)
	require.NoError(t, err)

	outcome, err := repo.DeleteBatch(t.Context(), ownerID, []uuid.UUID{deleted.ID}, false)
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{deleted.ID}, outcome.Deleted)

	var mediaCount int
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM media_files WHERE id = $1`, mediaID).Scan(&mediaCount))
	assert.Equal(t, 1, mediaCount)

	var used int64
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT storage_used_bytes FROM organizations WHERE id = $1`, orgID).Scan(&used))
	assert.Equal(t, int64(8), used)
}

// TestRepositoryDeleteSinglePackKeepsErrors: одиночное удаление теперь ходит
// общим путём, поэтому его ошибки проверяются отдельно.
func TestRepositoryDeleteSinglePackKeepsErrors(t *testing.T) {
	pool := newPackTestDB(t)
	repo := NewRepository(pool)
	_, ownerID, folderID := seedPackOwner(t, pool, "single delete org")
	libraryFolderID := seedPackLibraryFolder(t, pool, ownerID)
	config := []byte(`{"metadata":{"version":"2.0"},"settings":{"columns":1,"rows":1},"blocks":[]}`)

	published, err := repo.Create(t.Context(), ownerID, CreateInput{
		Title: "Published", FolderID: folderID, Config: config,
	})
	require.NoError(t, err)
	_, err = repo.Publish(t.Context(), ownerID, published.ID, libraryFolderID, false)
	require.NoError(t, err)

	assert.ErrorIs(t, repo.Delete(t.Context(), ownerID, published.ID), ErrPackPublished)
	assert.ErrorIs(t, repo.Delete(t.Context(), ownerID, uuid.New()), ErrPackNotFound)
}

func seedPackMedia(
	t *testing.T,
	pool *pgxpool.Pool,
	orgID, ownerID uuid.UUID,
	name string,
	size int64,
) uuid.UUID {
	t.Helper()
	mediaID := uuid.New()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO media_files (
			id, org_id, uploader_id, name, sha256, mime_type, media_type, size_bytes, minio_key
		)
		VALUES ($1, $2, $3, $4, $5, 'image/png', 'image', $6, $7)`,
		mediaID, orgID, ownerID, name, mediaID.String(), size, "media/"+mediaID.String())
	require.NoError(t, err)
	return mediaID
}

func countPacks(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var count int
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT count(*) FROM packs`).Scan(&count))
	return count
}
