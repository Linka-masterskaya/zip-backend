package media

import (
	"database/sql"
	"testing"
	"time"

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
		INSERT INTO users (id, org_id, display_name) VALUES ($1, $2, 'Test User')`, userID, orgID)
	require.NoError(t, err)

	repo := NewRepository(pool)
	input := File{
		OrgID: orgID, UploaderID: userID, SHA256: "digest",
		MIMEType: "image/png", SizeBytes: 123, MinIOKey: "media/key",
	}
	first, err := repo.Upsert(t.Context(), input)
	require.NoError(t, err)
	second, err := repo.Upsert(t.Context(), input)
	require.NoError(t, err)
	assert.Equal(t, first.ID, second.ID)
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

func TestRepositoryListScopesSearchesFiltersAndPaginatesByCursor(t *testing.T) {
	pool, cleanup := testutil.NewPostgres(t)
	t.Cleanup(cleanup)
	db := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, applyMediaMigrations(db))

	orgID, otherOrgID, userID := uuid.New(), uuid.New(), uuid.New()
	_, err := pool.Exec(t.Context(), `
		INSERT INTO organizations (id, name) VALUES ($1, 'media org'), ($2, 'other org')`,
		orgID, otherOrgID)
	require.NoError(t, err)
	_, err = pool.Exec(t.Context(), `
    INSERT INTO users (id, org_id, display_name) VALUES ($1, $2, 'Test User')`, userID, orgID)
	require.NoError(t, err)

	repo := NewRepository(pool)
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	seed := func(org uuid.UUID, name, sha, mediaType string, createdAt time.Time) File {
		created, upsertErr := repo.Upsert(t.Context(), File{
			OrgID: org, UploaderID: userID, Name: name, SHA256: sha,
			MIMEType: mediaType + "/x", MediaType: mediaType, SizeBytes: 10, MinIOKey: "media/" + sha,
		})
		require.NoError(t, upsertErr)
		_, execErr := pool.Exec(t.Context(),
			`UPDATE media_files SET created_at = $2 WHERE id = $1`, created.ID, createdAt)
		require.NoError(t, execErr)
		created.CreatedAt = createdAt
		return *created
	}

	// Newest first: cat, dog, note, oldCat.
	cat := seed(orgID, "cat.png", "sha-cat", "image", base.Add(4*time.Minute))
	dog := seed(orgID, "dog.png", "sha-dog", "image", base.Add(3*time.Minute))
	note := seed(orgID, "notes.mp3", "sha-note", "audio", base.Add(2*time.Minute))
	oldCat := seed(orgID, "old-cat.png", "sha-old-cat", "image", base.Add(1*time.Minute))
	seed(otherOrgID, "other-cat.png", "sha-other", "image", base.Add(5*time.Minute))

	all, err := repo.List(t.Context(), orgID, "", "", nil, 10)
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{cat.ID, dog.ID, note.ID, oldCat.ID}, idsOf(all),
		"only the caller's org, newest first")

	byName, err := repo.List(t.Context(), orgID, "CAT", "", nil, 10)
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{cat.ID, oldCat.ID}, idsOf(byName), "case-insensitive substring match")

	byType, err := repo.List(t.Context(), orgID, "", "audio", nil, 10)
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{note.ID}, idsOf(byType))

	firstPage, err := repo.List(t.Context(), orgID, "", "", nil, 2)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{cat.ID, dog.ID}, idsOf(firstPage))

	cursor := &mediaCursor{
		CreatedAt: firstPage[len(firstPage)-1].CreatedAt,
		ID:        firstPage[len(firstPage)-1].ID,
	}
	secondPage, err := repo.List(t.Context(), orgID, "", "", cursor, 2)
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{note.ID, oldCat.ID}, idsOf(secondPage), "resumes right after the cursor")
}

func idsOf(files []File) []uuid.UUID {
	ids := make([]uuid.UUID, len(files))
	for i, f := range files {
		ids[i] = f.ID
	}
	return ids
}

func applyMediaMigrations(db *sql.DB) error {
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	return goose.Up(db, "../../migrations")
}
