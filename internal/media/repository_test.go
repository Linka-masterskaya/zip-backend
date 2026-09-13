package media

import (
	"database/sql"
	"testing"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/testutil"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
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

	query := ListQuery{OrgID: orgID, UserID: userID, Limit: 10}

	all, total, err := repo.ListWithTotal(t.Context(), query)
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{cat.ID, dog.ID, note.ID, oldCat.ID}, idsOf(all),
		"only the own org, newest first")
	assert.Equal(t, 4, total)

	byName := query
	byName.Query = "CAT"
	named, namedTotal, err := repo.ListWithTotal(t.Context(), byName)
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{cat.ID, oldCat.ID}, idsOf(named), "case-insensitive substring match")
	assert.Equal(t, 2, namedTotal)

	byType := query
	byType.MediaType = "audio"
	typed, typedTotal, err := repo.ListWithTotal(t.Context(), byType)
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{note.ID}, idsOf(typed))
	assert.Equal(t, 1, typedTotal)

	paged := query
	paged.Limit = 2
	firstPage, pagedTotal, err := repo.ListWithTotal(t.Context(), paged)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{cat.ID, dog.ID}, idsOf(firstPage))
	assert.Equal(t, 4, pagedTotal, "total не зависит от размера страницы и курсора")

	paged.Cursor = &mediaCursor{
		CreatedAt: firstPage[len(firstPage)-1].CreatedAt,
		ID:        firstPage[len(firstPage)-1].ID,
	}
	secondPage, _, err := repo.ListWithTotal(t.Context(), paged)
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{note.ID, oldCat.ID}, idsOf(secondPage), "resumes right after the cursor")
}

func TestRepositoryListMarksUnreferencedOrgFilesDeletable(t *testing.T) {
	env := newMediaEnv(t)

	own := env.seed(env.orgID, env.userID, "sha-own", 10)
	mates := env.seed(env.orgID, env.mateID, "sha-mate", 10)

	items, total, err := env.repo.ListWithTotal(t.Context(),
		ListQuery{OrgID: env.orgID, UserID: env.userID, Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, 2, total, "список орг-скоупный, чужой файл в нём виден")

	// can_delete true когда файл не используется ни в одной из трёх таблиц.
	deletable := map[uuid.UUID]bool{}
	for _, item := range items {
		deletable[item.ID] = item.CanDelete
	}
	assert.True(t, deletable[own.ID])
	assert.True(t, deletable[mates.ID], "чужой файл без ссылок тоже удаляем")
}

func TestRepositoryUnusedFilterCoversAvatarsAndTTS(t *testing.T) {
	env := newMediaEnv(t)

	free := env.seed(env.orgID, env.userID, "sha-free", 10)
	usedByPack := env.seed(env.orgID, env.userID, "sha-pack", 10)
	avatar := env.seed(env.orgID, env.userID, "sha-avatar", 10)
	archived := env.seed(env.orgID, env.userID, "sha-archived", 10)
	voiced := env.seed(env.orgID, env.userID, "sha-tts", 10)

	env.attachPackUsage(usedByPack.ID)
	env.attachAvatar(avatar.ID)
	env.attachArchivedAvatar(archived.ID)
	env.attachTTSJob(voiced.ID)

	unused := ListQuery{OrgID: env.orgID, UserID: env.userID, Unused: true, Limit: 10}
	items, total, err := env.repo.ListWithTotal(t.Context(), unused)
	require.NoError(t, err)
	assert.ElementsMatch(t, []uuid.UUID{free.ID, archived.ID, voiced.ID}, idsOf(items),
		"аватар активного ученика занят, succeeded TTS и аватар архивированного свободны")
	assert.Equal(t, 3, total, "счётчик считается тем же предикатом, что и выдача")

	all, allTotal, err := env.repo.ListWithTotal(t.Context(),
		ListQuery{OrgID: env.orgID, UserID: env.userID, Limit: 10})
	require.NoError(t, err)
	assert.Len(t, all, 5)
	assert.Equal(t, 5, allTotal)
}

func TestRepositoryDeleteBatchSkipsEveryKindOfReference(t *testing.T) {
	env := newMediaEnv(t)

	free := env.seed(env.orgID, env.userID, "sha-free", 100)
	alsoFree := env.seed(env.orgID, env.userID, "sha-also-free", 40)
	usedByPack := env.seed(env.orgID, env.userID, "sha-pack", 700)
	avatar := env.seed(env.orgID, env.userID, "sha-avatar", 300)
	voiced := env.seed(env.orgID, env.userID, "sha-tts", 200)
	archived := env.seed(env.orgID, env.userID, "sha-archived", 60)
	mates := env.seed(env.orgID, env.mateID, "sha-mate", 5)
	foreign := env.seed(env.otherOrgID, env.strangerID, "sha-foreign", 9)
	missing := uuid.New()

	env.attachPackUsage(usedByPack.ID)
	env.attachAvatar(avatar.ID)
	env.attachArchivedAvatar(archived.ID)
	env.attachTTSJob(voiced.ID)

	outcome, err := env.repo.DeleteBatch(t.Context(), env.userID, []uuid.UUID{
		free.ID, alsoFree.ID, archived.ID, usedByPack.ID, avatar.ID, voiced.ID,
		mates.ID, foreign.ID, missing,
	}, false)
	require.NoError(t, err)
	assert.ElementsMatch(t, []uuid.UUID{free.ID, alsoFree.ID, archived.ID, mates.ID, voiced.ID}, outcome.Deleted,
		"аватар архивированного ученика удаляется наравне со свободными файлами")
	assert.ElementsMatch(t, []uuid.UUID{usedByPack.ID, avatar.ID}, outcome.InUse,
		"аватар активного ученика и TTS пропускаются наравне с файлом из набора")
	assert.Equal(t, int64(405), outcome.FreedBytes)

	// Квота уменьшается ровно на сумму размеров реально удалённых файлов.
	assert.Equal(t, int64(1000), env.storageUsed(env.orgID))
	// Чужая организация не затронута ни строкой, ни квотой.
	assert.Equal(t, int64(9), env.storageUsed(env.otherOrgID))

	// Аватар ученика остался на месте, ссылка не обнулена.
	var avatarID *uuid.UUID
	require.NoError(t, env.pool.QueryRow(t.Context(),
		`SELECT avatar_media_id FROM students WHERE id = $1`, env.studentID).Scan(&avatarID))
	require.NotNil(t, avatarID)
	assert.Equal(t, avatar.ID, *avatarID)
}

func TestRepositoryDeleteBatchDryRunChangesNothing(t *testing.T) {
	env := newMediaEnv(t)

	free := env.seed(env.orgID, env.userID, "sha-free", 100)
	usedByPack := env.seed(env.orgID, env.userID, "sha-pack", 40)
	env.attachPackUsage(usedByPack.ID)

	outcome, err := env.repo.DeleteBatch(t.Context(), env.userID,
		[]uuid.UUID{free.ID, usedByPack.ID}, true)
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{free.ID}, outcome.Deleted, "dry-run отвечает так же, как боевой вызов")
	assert.Equal(t, []uuid.UUID{usedByPack.ID}, outcome.InUse)
	assert.Equal(t, int64(100), outcome.FreedBytes)

	// Но в базе ничего не изменилось.
	assert.Equal(t, int64(140), env.storageUsed(env.orgID))
	var alive int
	require.NoError(t, env.pool.QueryRow(t.Context(),
		`SELECT count(*) FROM media_files WHERE id = ANY($1::uuid[])`,
		[]uuid.UUID{free.ID, usedByPack.ID}).Scan(&alive))
	assert.Equal(t, 2, alive)
}

func TestRepositoryDeleteBatchOfForeignFilesOnly(t *testing.T) {
	env := newMediaEnv(t)

	own := env.seed(env.orgID, env.userID, "sha-own", 100)
	foreign := env.seed(env.otherOrgID, env.strangerID, "sha-foreign", 9)

	outcome, err := env.repo.DeleteBatch(t.Context(), env.userID,
		[]uuid.UUID{foreign.ID, uuid.New()}, false)
	require.NoError(t, err)
	assert.Empty(t, outcome.Deleted, "пачка целиком из чужих файлов ничего не удаляет")
	assert.Empty(t, outcome.InUse)
	assert.Zero(t, outcome.FreedBytes)

	assert.Equal(t, int64(100), env.storageUsed(env.orgID), "квота своей организации не тронута")
	assert.Equal(t, int64(9), env.storageUsed(env.otherOrgID))

	var alive int
	require.NoError(t, env.pool.QueryRow(t.Context(),
		`SELECT count(*) FROM media_files WHERE id = $1`, own.ID).Scan(&alive))
	assert.Equal(t, 1, alive)
}

// mediaEnv поднимает базу с двумя организациями, тремя пользователями и учеником,
// чтобы каждый тест не повторял одну и ту же подготовку.
type mediaEnv struct {
	t          *testing.T
	pool       *pgxpool.Pool
	repo       *Repository
	orgID      uuid.UUID
	otherOrgID uuid.UUID
	userID     uuid.UUID
	mateID     uuid.UUID
	strangerID uuid.UUID
	studentID  uuid.UUID
}

func newMediaEnv(t *testing.T) *mediaEnv {
	t.Helper()
	pool, cleanup := testutil.NewPostgres(t)
	t.Cleanup(cleanup)
	db := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, applyMediaMigrations(db))

	env := &mediaEnv{
		t: t, pool: pool, repo: NewRepository(pool),
		orgID: uuid.New(), otherOrgID: uuid.New(),
		userID: uuid.New(), mateID: uuid.New(), strangerID: uuid.New(),
		studentID: uuid.New(),
	}
	_, err := pool.Exec(t.Context(), `
		INSERT INTO organizations (id, name) VALUES ($1, 'media org'), ($2, 'other org')`,
		env.orgID, env.otherOrgID)
	require.NoError(t, err)
	_, err = pool.Exec(t.Context(), `
		INSERT INTO users (id, org_id, display_name)
		VALUES ($1, $2, 'Owner'), ($3, $2, 'Mate'), ($4, $5, 'Stranger')`,
		env.userID, env.orgID, env.mateID, env.strangerID, env.otherOrgID)
	require.NoError(t, err)
	_, err = pool.Exec(t.Context(), `
		INSERT INTO students (id, defectologist_id, email_encrypted, name, status)
		VALUES ($1, $2, $3, 'Ученик', 'active')`, env.studentID, env.userID, []byte{1})
	require.NoError(t, err)
	return env
}

func (e *mediaEnv) seed(org, uploader uuid.UUID, sha string, size int64) File {
	created, err := e.repo.Upsert(e.t.Context(), File{
		OrgID: org, UploaderID: uploader, Name: sha, SHA256: sha,
		MIMEType: "image/png", MediaType: "image", SizeBytes: size, MinIOKey: "media/" + sha,
	})
	require.NoError(e.t, err)
	return *created
}

func (e *mediaEnv) attachPackUsage(mediaID uuid.UUID) {
	_, err := e.pool.Exec(e.t.Context(), `
		INSERT INTO media_usages (media_id, source_type, source_id)
		VALUES ($1, 'pack', $2)`, mediaID, uuid.New())
	require.NoError(e.t, err)
}

func (e *mediaEnv) attachAvatar(mediaID uuid.UUID) {
	_, err := e.pool.Exec(e.t.Context(), `
		UPDATE students SET avatar_media_id = $2 WHERE id = $1`, e.studentID, mediaID)
	require.NoError(e.t, err)
}

// attachArchivedAvatar вешает файл на архивированного ученика. Восстановления
// ученика в API нет, поэтому такой аватар считается свободным.
func (e *mediaEnv) attachArchivedAvatar(mediaID uuid.UUID) {
	_, err := e.pool.Exec(e.t.Context(), `
		INSERT INTO students
			(id, defectologist_id, email_encrypted, name, status, avatar_media_id, deleted_at)
		VALUES ($1, $2, $3, 'Архивный', 'archived', $4, now())`,
		uuid.New(), e.userID, []byte{2}, mediaID)
	require.NoError(e.t, err)
}

func (e *mediaEnv) attachTTSJob(mediaID uuid.UUID) {
	_, err := e.pool.Exec(e.t.Context(), `
		INSERT INTO tts_jobs (org_id, text, voice, status, media_id)
		VALUES ($1, $2, 'alena', 'succeeded', $3)`, e.orgID, "text-"+mediaID.String(), mediaID)
	require.NoError(e.t, err)
}

func (e *mediaEnv) attachActiveTTSJob(mediaID uuid.UUID) {
	_, err := e.pool.Exec(e.t.Context(), `
		INSERT INTO tts_jobs (org_id, text, voice, status, media_id)
		VALUES ($1, $2, 'alena', 'in_progress', $3)`, e.orgID, "active-"+mediaID.String(), mediaID)
	require.NoError(e.t, err)
}

func (e *mediaEnv) storageUsed(org uuid.UUID) int64 {
	var used int64
	require.NoError(e.t, e.pool.QueryRow(e.t.Context(),
		`SELECT storage_used_bytes FROM organizations WHERE id = $1`, org).Scan(&used))
	return used
}

func idsOf(items []ListItem) []uuid.UUID {
	ids := make([]uuid.UUID, len(items))
	for i, item := range items {
		ids[i] = item.ID
	}
	return ids
}

func applyMediaMigrations(db *sql.DB) error {
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	return goose.Up(db, "../../migrations")
}

func TestRepositoryOrphanScannerDeletesInBatchesAndReturnsQuota(t *testing.T) {
	env := newMediaEnv(t)

	freeA := env.seed(env.orgID, env.userID, "scan-free-a", 10)
	freeB := env.seed(env.orgID, env.userID, "scan-free-b", 20)
	freeC := env.seed(env.orgID, env.userID, "scan-free-c", 30)
	usedByPack := env.seed(env.orgID, env.userID, "scan-pack", 40)
	activeAvatar := env.seed(env.orgID, env.userID, "scan-avatar", 50)
	archivedAvatar := env.seed(env.orgID, env.userID, "scan-archived", 60)
	usedByTTS := env.seed(env.orgID, env.userID, "scan-tts", 70)

	env.attachPackUsage(usedByPack.ID)
	env.attachAvatar(activeAvatar.ID)
	env.attachArchivedAvatar(archivedAvatar.ID)
	env.attachActiveTTSJob(usedByTTS.ID)

	cleared, err := env.repo.ClearSoftDeletedStudentAvatars(t.Context())
	require.NoError(t, err)
	assert.Equal(t, int64(1), cleared)

	first, err := env.repo.DeleteOrphanBatch(t.Context(), 2, time.Now().Add(time.Minute))
	require.NoError(t, err)
	assert.Equal(t, int64(2), first.Count)
	assert.Equal(t, int64(2), first.Candidates)

	second, err := env.repo.DeleteOrphanBatch(t.Context(), 2, time.Now().Add(time.Minute))
	require.NoError(t, err)
	assert.Equal(t, int64(2), second.Count)
	assert.Equal(t, int64(2), second.Candidates)

	last, err := env.repo.DeleteOrphanBatch(t.Context(), 2, time.Now().Add(time.Minute))
	require.NoError(t, err)
	assert.Zero(t, last.Count)
	assert.Zero(t, last.Candidates)

	assert.Equal(t, int64(4), first.Count+second.Count)
	assert.Equal(t, int64(120), first.Bytes+second.Bytes)
	assert.Equal(t, int64(160), env.storageUsed(env.orgID))

	var orphanCount int
	require.NoError(t, env.pool.QueryRow(t.Context(), `
		SELECT count(*) FROM media_files
		WHERE id = ANY($1::uuid[])`, []uuid.UUID{
		freeA.ID, freeB.ID, freeC.ID, archivedAvatar.ID,
	}).Scan(&orphanCount))
	assert.Zero(t, orphanCount)

	var referencedCount int
	require.NoError(t, env.pool.QueryRow(t.Context(), `
		SELECT count(*) FROM media_files
		WHERE id = ANY($1::uuid[])`, []uuid.UUID{
		usedByPack.ID, activeAvatar.ID, usedByTTS.ID,
	}).Scan(&referencedCount))
	assert.Equal(t, 3, referencedCount, "активная tts_jobs и активный avatar должны удерживать media")

	var staleArchivedLinks int
	require.NoError(t, env.pool.QueryRow(t.Context(), `
		SELECT count(*) FROM students
		WHERE deleted_at IS NOT NULL AND avatar_media_id IS NOT NULL`).Scan(&staleArchivedLinks))
	assert.Zero(t, staleArchivedLinks)
}

func TestRepositoryOrphanScannerGracePeriodProtectsFreshMedia(t *testing.T) {
	env := newMediaEnv(t)
	fresh := env.seed(env.orgID, env.userID, "scan-fresh", 25)

	cutoff := time.Now().Add(-time.Minute)
	batch, err := env.repo.DeleteOrphanBatch(t.Context(), 10, cutoff)
	require.NoError(t, err)
	assert.Zero(t, batch.Count)
	assert.Zero(t, batch.Candidates)
	assert.Equal(t, int64(25), env.storageUsed(env.orgID),
		"fresh unreferenced media must keep quota during the grace period")

	_, err = env.pool.Exec(t.Context(), `
		UPDATE media_files SET created_at = $2 WHERE id = $1`,
		fresh.ID, time.Now().Add(-2*time.Minute))
	require.NoError(t, err)

	batch, err = env.repo.DeleteOrphanBatch(t.Context(), 10, cutoff)
	require.NoError(t, err)
	assert.Equal(t, int64(1), batch.Count)
	assert.Equal(t, int64(1), batch.Candidates)
	assert.Equal(t, int64(25), batch.Bytes)
	assert.Zero(t, env.storageUsed(env.orgID))
}

func TestRepositoryOrphanScannerGracePeriodProtectsRecentlyReuploadedMedia(t *testing.T) {
	env := newMediaEnv(t)
	file := env.seed(env.orgID, env.userID, "scan-reuploaded", 25)
	old := time.Now().Add(-10 * time.Minute)
	cutoff := time.Now().Add(-time.Minute)

	_, err := env.pool.Exec(t.Context(), `
		UPDATE media_files SET created_at = $2 WHERE id = $1`, file.ID, old)
	require.NoError(t, err)
	_, err = env.pool.Exec(t.Context(), `
		INSERT INTO storage_objects (key, size, content_type, sha256, created_at, updated_at)
		VALUES ($1, $2, 'image/png', $3, $4, now())`,
		file.MinIOKey, file.SizeBytes, file.SHA256, old)
	require.NoError(t, err)

	batch, err := env.repo.DeleteOrphanBatch(t.Context(), 10, cutoff)
	require.NoError(t, err)
	assert.Zero(t, batch.Count)
	assert.Zero(t, batch.Candidates)
	assert.Equal(t, int64(25), env.storageUsed(env.orgID),
		"a recent PutObject must protect an older deduplicated media_files row")

	_, err = env.pool.Exec(t.Context(), `
		UPDATE storage_objects SET updated_at = $2 WHERE key = $1`, file.MinIOKey, old)
	require.NoError(t, err)

	batch, err = env.repo.DeleteOrphanBatch(t.Context(), 10, cutoff)
	require.NoError(t, err)
	assert.Equal(t, int64(1), batch.Count)
	assert.Equal(t, int64(1), batch.Candidates)
	assert.Equal(t, int64(25), batch.Bytes)
	assert.Zero(t, env.storageUsed(env.orgID))
}

func TestRepositoryOrphanScannerRechecksRecentReuploadBeforeDelete(t *testing.T) {
	env := newMediaEnv(t)
	file := env.seed(env.orgID, env.userID, "scan-race-reupload", 25)
	old := time.Now().Add(-10 * time.Minute)
	cutoff := time.Now().Add(-time.Minute)

	_, err := env.pool.Exec(t.Context(), `
		UPDATE media_files SET created_at = $2 WHERE id = $1`, file.ID, old)
	require.NoError(t, err)
	_, err = env.pool.Exec(t.Context(), `
		INSERT INTO storage_objects (key, size, content_type, sha256, created_at, updated_at)
		VALUES ($1, $2, 'image/png', $3, $4, $4)`,
		file.MinIOKey, file.SizeBytes, file.SHA256, old)
	require.NoError(t, err)

	tx, err := env.pool.Begin(t.Context())
	require.NoError(t, err)
	defer rollbackMediaTx(t.Context(), tx)

	candidateIDs, err := selectOrphanCandidates(t.Context(), tx, 1, cutoff)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{file.ID}, candidateIDs)

	_, err = tx.Exec(t.Context(), `
		UPDATE storage_objects SET updated_at = now() WHERE key = $1`, file.MinIOKey)
	require.NoError(t, err)

	var deleted OrphanBatchResult
	require.NoError(t, tx.QueryRow(t.Context(), deleteLockedOrphansQuery, candidateIDs, cutoff).
		Scan(&deleted.Count, &deleted.Bytes))
	assert.Zero(t, deleted.Count, "second-stage freshness check must protect a re-upload that starts after selection")
	assert.Zero(t, deleted.Bytes)
	require.NoError(t, tx.Commit(t.Context()))

	var alive int
	require.NoError(t, env.pool.QueryRow(t.Context(), `
		SELECT count(*) FROM media_files WHERE id = $1`, file.ID).Scan(&alive))
	assert.Equal(t, 1, alive)
	assert.Equal(t, int64(25), env.storageUsed(env.orgID))
}

func TestRepositoryOrphanScannerRechecksAvatarBeforeDelete(t *testing.T) {
	env := newMediaEnv(t)
	file := env.seed(env.orgID, env.userID, "scan-race-avatar", 25)

	tx, err := env.pool.Begin(t.Context())
	require.NoError(t, err)
	defer rollbackMediaTx(t.Context(), tx)

	candidateIDs, err := selectOrphanCandidates(t.Context(), tx, 1, time.Now().Add(time.Minute))
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{file.ID}, candidateIDs)

	_, err = tx.Exec(t.Context(), `
		UPDATE students SET avatar_media_id = $2 WHERE id = $1`, env.studentID, file.ID)
	require.NoError(t, err)

	var deleted OrphanBatchResult
	require.NoError(t, tx.QueryRow(t.Context(), deleteLockedOrphansQuery, candidateIDs, time.Now().Add(time.Minute)).
		Scan(&deleted.Count, &deleted.Bytes))
	assert.Zero(t, deleted.Count, "second-stage reference check must protect a newly attached avatar")
	assert.Zero(t, deleted.Bytes)
	require.NoError(t, tx.Commit(t.Context()))

	var avatarID uuid.UUID
	require.NoError(t, env.pool.QueryRow(t.Context(), `
		SELECT avatar_media_id FROM students WHERE id = $1`, env.studentID).Scan(&avatarID))
	assert.Equal(t, file.ID, avatarID)

	var alive int
	require.NoError(t, env.pool.QueryRow(t.Context(), `
		SELECT count(*) FROM media_files WHERE id = $1`, file.ID).Scan(&alive))
	assert.Equal(t, 1, alive)
	assert.Equal(t, int64(25), env.storageUsed(env.orgID), "quota must stay reserved for referenced media")
}

func TestRepositoryOrphanScannerRechecksTTSBeforeDelete(t *testing.T) {
	env := newMediaEnv(t)
	file := env.seed(env.orgID, env.userID, "scan-race-tts", 35)

	tx, err := env.pool.Begin(t.Context())
	require.NoError(t, err)
	defer rollbackMediaTx(t.Context(), tx)

	candidateIDs, err := selectOrphanCandidates(t.Context(), tx, 1, time.Now().Add(time.Minute))
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{file.ID}, candidateIDs)

	_, err = tx.Exec(t.Context(), `
		INSERT INTO tts_jobs (org_id, text, voice, status, media_id)
		VALUES ($1, $2, 'alena', 'in_progress', $3)`,
		env.orgID, "race-tts-"+file.ID.String(), file.ID)
	require.NoError(t, err)

	var deleted OrphanBatchResult
	require.NoError(t, tx.QueryRow(t.Context(), deleteLockedOrphansQuery, candidateIDs, time.Now().Add(time.Minute)).
		Scan(&deleted.Count, &deleted.Bytes))
	assert.Zero(t, deleted.Count, "second-stage reference check must protect a newly attached TTS media")
	assert.Zero(t, deleted.Bytes)
	require.NoError(t, tx.Commit(t.Context()))

	var linkedMediaID uuid.UUID
	require.NoError(t, env.pool.QueryRow(t.Context(), `
		SELECT media_id FROM tts_jobs WHERE text = $1`, "race-tts-"+file.ID.String()).Scan(&linkedMediaID))
	assert.Equal(t, file.ID, linkedMediaID)

	var alive int
	require.NoError(t, env.pool.QueryRow(t.Context(), `
		SELECT count(*) FROM media_files WHERE id = $1`, file.ID).Scan(&alive))
	assert.Equal(t, 1, alive)
	assert.Equal(t, int64(35), env.storageUsed(env.orgID), "quota must stay reserved for referenced media")
}

func TestRepositoryOrphanScannerIgnoresFinishedTTSJobs(t *testing.T) {
	env := newMediaEnv(t)

	succeeded := env.seed(env.orgID, env.userID, "scan-tts-succeeded", 25)
	failed := env.seed(env.orgID, env.userID, "scan-tts-failed", 35)

	_, err := env.pool.Exec(t.Context(), `
		INSERT INTO tts_jobs (org_id, text, voice, status, media_id)
		VALUES
			($1, $2, 'alena', 'succeeded', $3),
			($1, $4, 'alena', 'failed', $5)`,
		env.orgID, "finished-succeeded-"+succeeded.ID.String(), succeeded.ID,
		"finished-failed-"+failed.ID.String(), failed.ID)
	require.NoError(t, err)

	batch, err := env.repo.DeleteOrphanBatch(t.Context(), 10, time.Now().Add(time.Minute))
	require.NoError(t, err)
	assert.Equal(t, int64(2), batch.Count)
	assert.Equal(t, int64(2), batch.Candidates)
	assert.Equal(t, int64(60), batch.Bytes)
	assert.Zero(t, env.storageUsed(env.orgID))

	var alive int
	require.NoError(t, env.pool.QueryRow(t.Context(), `
		SELECT count(*) FROM media_files WHERE id = ANY($1::uuid[])`,
		[]uuid.UUID{succeeded.ID, failed.ID}).Scan(&alive))
	assert.Zero(t, alive)

	var nulledLinks int
	require.NoError(t, env.pool.QueryRow(t.Context(), `
		SELECT count(*) FROM tts_jobs
		WHERE text = ANY($1::text[]) AND media_id IS NULL`,
		[]string{
			"finished-succeeded-" + succeeded.ID.String(),
			"finished-failed-" + failed.ID.String(),
		}).Scan(&nulledLinks))
	assert.Equal(t, 2, nulledLinks, "finished TTS jobs must not retain media and their FK must be nulled")
}
