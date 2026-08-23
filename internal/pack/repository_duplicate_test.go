package pack

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRepositoryDuplicateCopiesPackAndReusesMedia(t *testing.T) {
	pool := newPackTestDB(t)
	repo := NewRepository(pool)
	orgID, ownerID, folderID := seedPackOwner(t, pool, "duplicate owner org")
	config := []byte(`{"metadata":{"version":"2.0"},"settings":{"columns":1,"rows":1},"blocks":[]}`)
	source, err := repo.Create(t.Context(), ownerID, CreateInput{
		Title: "Исходный набор", FolderID: folderID, Config: config,
	})
	require.NoError(t, err)

	ageMin, ageMax, difficulty := 5, 9, "medium"
	goals, notes := []string{"speech", "attention"}, "Исходные заметки"
	source, err = repo.Update(t.Context(), ownerID, source.ID, UpdateInput{
		FilterMetadata: &FilterMetadataPatch{
			AgeMin:     NullablePatch[int]{Set: true, Value: &ageMin},
			AgeMax:     NullablePatch[int]{Set: true, Value: &ageMax},
			Difficulty: NullablePatch[string]{Set: true, Value: &difficulty},
			Goals:      &goals,
		},
		Notes: NullablePatch[string]{Set: true, Value: &notes},
	})
	require.NoError(t, err)

	mediaID := uuid.New()
	_, err = pool.Exec(t.Context(), `
		INSERT INTO media_files (
			id, org_id, uploader_id, name, sha256, mime_type, media_type, size_bytes, minio_key
		)
		VALUES ($1, $2, $3, 'duplicate.png', $4, 'image/png', 'image', 4, $5)`,
		mediaID, orgID, ownerID, "duplicate-media-sha", "media/"+mediaID.String(),
	)
	require.NoError(t, err)
	_, err = pool.Exec(t.Context(), `
		UPDATE organizations SET storage_used_bytes = 4 WHERE id = $1`, orgID)
	require.NoError(t, err)
	_, err = pool.Exec(t.Context(), `
		INSERT INTO media_usages (media_id, source_type, source_id)
		VALUES ($1, 'pack', $2)`, mediaID, source.ID)
	require.NoError(t, err)

	studentID, _ := seedPackStudentFolder(t, pool, ownerID, "Duplicate Student")
	_, err = repo.Assign(t.Context(), ownerID, source.ID, []uuid.UUID{studentID})
	require.NoError(t, err)

	duplicated, err := repo.Duplicate(t.Context(), ownerID, source.ID, DuplicateInput{})
	require.NoError(t, err)
	assert.NotEqual(t, source.ID, duplicated.ID)
	assert.Equal(t, ownerID, duplicated.OwnerID)
	assert.Equal(t, folderID, duplicated.FolderID)
	assert.Equal(t, "Исходный набор (копия)", duplicated.Title)
	assert.Equal(t, "draft", duplicated.Status)
	assert.Nil(t, duplicated.PublishedAt)
	assert.Nil(t, duplicated.LibraryFolderID)
	assert.Equal(t, source.AgeMin, duplicated.AgeMin)
	assert.Equal(t, source.AgeMax, duplicated.AgeMax)
	assert.Equal(t, source.Difficulty, duplicated.Difficulty)
	assert.Equal(t, source.Goals, duplicated.Goals)
	assert.Equal(t, source.Notes, duplicated.Notes)
	assert.JSONEq(t, string(source.Config), string(duplicated.Config))

	var duplicatedUsageIDs []uuid.UUID
	rows, err := pool.Query(t.Context(), `
		SELECT media_id FROM media_usages
		WHERE source_type = 'pack' AND source_id = $1`, duplicated.ID)
	require.NoError(t, err)
	for rows.Next() {
		var id uuid.UUID
		require.NoError(t, rows.Scan(&id))
		duplicatedUsageIDs = append(duplicatedUsageIDs, id)
	}
	require.NoError(t, rows.Err())
	rows.Close()
	assert.Equal(t, []uuid.UUID{mediaID}, duplicatedUsageIDs)

	var mediaCount, adaptationCount int
	var storageUsed int64
	require.NoError(t, pool.QueryRow(t.Context(), `SELECT count(*) FROM media_files`).Scan(&mediaCount))
	require.NoError(t, pool.QueryRow(t.Context(), `
		SELECT storage_used_bytes FROM organizations WHERE id = $1`, orgID).Scan(&storageUsed))
	require.NoError(t, pool.QueryRow(t.Context(), `
		SELECT count(*) FROM pack_adaptations WHERE pack_id = $1`, duplicated.ID).Scan(&adaptationCount))
	assert.Equal(t, 1, mediaCount)
	assert.Equal(t, int64(4), storageUsed)
	assert.Zero(t, adaptationCount)

	changedTitle := "Изменённая копия"
	_, err = repo.Update(t.Context(), ownerID, duplicated.ID, UpdateInput{Title: &changedTitle})
	require.NoError(t, err)
	require.NoError(t, repo.Delete(t.Context(), ownerID, duplicated.ID))
	unchanged, err := repo.Get(t.Context(), ownerID, source.ID)
	require.NoError(t, err)
	assert.Equal(t, "Исходный набор", unchanged.Title)
	assert.JSONEq(t, string(source.Config), string(unchanged.Config))
}

func TestRepositoryDuplicateAccessAndDestinationPolicy(t *testing.T) {
	pool := newPackTestDB(t)
	repo := NewRepository(pool)
	orgID, ownerID, ownerFolderID := seedPackOwner(t, pool, "duplicate shared org")
	readerID, readerFolderID := seedPackUserInOrg(t, pool, orgID, "my")
	_, readerStudentFolderID := seedPackStudentFolder(t, pool, readerID, "Reader Student")
	readerLibraryFolderID := seedPackLibraryFolder(t, pool, readerID)
	config := []byte(`{"metadata":{"version":"2.0"},"settings":{"columns":1,"rows":1},"blocks":[]}`)

	privatePack, err := repo.Create(t.Context(), ownerID, CreateInput{
		Title: "Private", FolderID: ownerFolderID, Config: config,
	})
	require.NoError(t, err)
	publishedPack, err := repo.Create(t.Context(), ownerID, CreateInput{
		Title: "Published", FolderID: ownerFolderID, Config: config,
	})
	require.NoError(t, err)
	ownerLibraryFolderID := seedPackLibraryFolder(t, pool, ownerID)
	publishedPack, err = repo.Publish(
		t.Context(), ownerID, publishedPack.ID, ownerLibraryFolderID, false,
	)
	require.NoError(t, err)

	foreignOrgID, foreignOwnerID, foreignFolderID := seedPackOwner(t, pool, "duplicate foreign org")
	foreignPublished, err := repo.Create(t.Context(), foreignOwnerID, CreateInput{
		Title: "Foreign published", FolderID: foreignFolderID, Config: config,
	})
	require.NoError(t, err)
	foreignLibraryFolderID := seedPackLibraryFolder(t, pool, foreignOwnerID)
	foreignPublished, err = repo.Publish(
		t.Context(), foreignOwnerID, foreignPublished.ID, foreignLibraryFolderID, false,
	)
	require.NoError(t, err)
	assert.NotEqual(t, orgID, foreignOrgID)

	tests := []struct {
		name       string
		packID     uuid.UUID
		folderID   *uuid.UUID
		wantFolder uuid.UUID
		wantErr    error
	}{
		{
			name:   "same organization published into my section folder",
			packID: publishedPack.ID, folderID: &readerFolderID, wantFolder: readerFolderID,
		},
		{
			name:   "same organization published into students section folder",
			packID: publishedPack.ID, folderID: &readerStudentFolderID, wantFolder: readerStudentFolderID,
		},
		{name: "foreign published requires destination", packID: publishedPack.ID, wantErr: ErrDuplicateDestinationRequired},
		{name: "foreign private is hidden", packID: privatePack.ID, folderID: &readerFolderID, wantErr: ErrPackNotFound},
		{name: "cross organization published is hidden", packID: foreignPublished.ID, folderID: &readerFolderID, wantErr: ErrPackNotFound},
		{name: "another users folder is forbidden", packID: publishedPack.ID, folderID: &ownerFolderID, wantErr: ErrFolderNotAllowed},
		{name: "library folder is forbidden", packID: publishedPack.ID, folderID: &readerLibraryFolderID, wantErr: ErrFolderNotAllowed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, duplicateErr := repo.Duplicate(
				t.Context(), readerID, test.packID, DuplicateInput{FolderID: test.folderID},
			)
			if test.wantErr != nil {
				assert.ErrorIs(t, duplicateErr, test.wantErr)
				assert.Nil(t, result)
				return
			}
			require.NoError(t, duplicateErr)
			assert.Equal(t, readerID, result.OwnerID)
			assert.Equal(t, test.wantFolder, result.FolderID)
			assert.Equal(t, "draft", result.Status)
		})
	}
}

func TestRepositoryDuplicateRollsBackWhenMediaUsagesFail(t *testing.T) {
	pool := newPackTestDB(t)
	repo := NewRepository(pool)
	orgID, ownerID, folderID := seedPackOwner(t, pool, "duplicate rollback org")
	config := []byte(`{"metadata":{"version":"2.0"},"settings":{"columns":1,"rows":1},"blocks":[]}`)
	source, err := repo.Create(t.Context(), ownerID, CreateInput{
		Title: "Rollback source", FolderID: folderID, Config: config,
	})
	require.NoError(t, err)
	mediaID := uuid.New()
	_, err = pool.Exec(t.Context(), `
		INSERT INTO media_files (
			id, org_id, uploader_id, name, sha256, mime_type, media_type, size_bytes, minio_key
		)
		VALUES ($1, $2, $3, 'rollback.png', $4, 'image/png', 'image', 4, $5)`,
		mediaID, orgID, ownerID, "rollback-media-sha", "media/"+mediaID.String(),
	)
	require.NoError(t, err)
	_, err = pool.Exec(t.Context(), `
		INSERT INTO media_usages (media_id, source_type, source_id)
		VALUES ($1, 'pack', $2)`, mediaID, source.ID)
	require.NoError(t, err)

	_, err = pool.Exec(t.Context(), `
		CREATE FUNCTION fail_duplicate_media_usage() RETURNS trigger AS $$
		BEGIN
			RAISE EXCEPTION 'forced duplicate media usage failure';
		END;
		$$ LANGUAGE plpgsql;
		CREATE TRIGGER fail_duplicate_media_usage
		BEFORE INSERT ON media_usages
		FOR EACH ROW EXECUTE FUNCTION fail_duplicate_media_usage()`)
	require.NoError(t, err)

	var beforeCount int
	require.NoError(t, pool.QueryRow(t.Context(), `SELECT count(*) FROM packs`).Scan(&beforeCount))
	result, err := repo.Duplicate(t.Context(), ownerID, source.ID, DuplicateInput{})
	require.Error(t, err)
	assert.Nil(t, result)

	var afterCount, duplicateUsages int
	require.NoError(t, pool.QueryRow(t.Context(), `SELECT count(*) FROM packs`).Scan(&afterCount))
	require.NoError(t, pool.QueryRow(t.Context(), `
		SELECT count(*) FROM media_usages
		WHERE source_type = 'pack' AND source_id <> $1`, source.ID).Scan(&duplicateUsages))
	assert.Equal(t, beforeCount, afterCount)
	assert.Zero(t, duplicateUsages)
}
