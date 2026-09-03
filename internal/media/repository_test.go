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

func TestRepositoryListMarksOnlyOwnFilesDeletable(t *testing.T) {
	env := newMediaEnv(t)

	own := env.seed(env.orgID, env.userID, "sha-own", 10)
	mates := env.seed(env.orgID, env.mateID, "sha-mate", 10)

	items, total, err := env.repo.ListWithTotal(t.Context(),
		ListQuery{OrgID: env.orgID, UserID: env.userID, Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, 2, total, "список орг-скоупный, чужой файл в нём виден")

	// can_delete отражает то же правило, по которому работает удаление.
	deletable := map[uuid.UUID]bool{}
	for _, item := range items {
		deletable[item.ID] = item.CanDelete
	}
	assert.True(t, deletable[own.ID])
	assert.False(t, deletable[mates.ID], "чужой файл помечен как неудаляемый")
}

func TestRepositoryUnusedFilterCoversAvatarsAndTTS(t *testing.T) {
	env := newMediaEnv(t)

	free := env.seed(env.orgID, env.userID, "sha-free", 10)
	usedByPack := env.seed(env.orgID, env.userID, "sha-pack", 10)
	avatar := env.seed(env.orgID, env.userID, "sha-avatar", 10)
	voiced := env.seed(env.orgID, env.userID, "sha-tts", 10)

	env.attachPackUsage(usedByPack.ID)
	env.attachAvatar(avatar.ID)
	env.attachTTSJob(voiced.ID)

	unused := ListQuery{OrgID: env.orgID, UserID: env.userID, Unused: true, Limit: 10}
	items, total, err := env.repo.ListWithTotal(t.Context(), unused)
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{free.ID}, idsOf(items),
		"аватар ученика и результат TTS не считаются неиспользуемыми")
	assert.Equal(t, 1, total, "счётчик считается тем же предикатом, что и выдача")

	all, allTotal, err := env.repo.ListWithTotal(t.Context(),
		ListQuery{OrgID: env.orgID, UserID: env.userID, Limit: 10})
	require.NoError(t, err)
	assert.Len(t, all, 4)
	assert.Equal(t, 4, allTotal)
}

func TestRepositoryDeleteBatchSkipsEveryKindOfReference(t *testing.T) {
	env := newMediaEnv(t)

	free := env.seed(env.orgID, env.userID, "sha-free", 100)
	alsoFree := env.seed(env.orgID, env.userID, "sha-also-free", 40)
	usedByPack := env.seed(env.orgID, env.userID, "sha-pack", 700)
	avatar := env.seed(env.orgID, env.userID, "sha-avatar", 300)
	voiced := env.seed(env.orgID, env.userID, "sha-tts", 200)
	mates := env.seed(env.orgID, env.mateID, "sha-mate", 5)
	foreign := env.seed(env.otherOrgID, env.strangerID, "sha-foreign", 9)
	missing := uuid.New()

	env.attachPackUsage(usedByPack.ID)
	env.attachAvatar(avatar.ID)
	env.attachTTSJob(voiced.ID)

	outcome, err := env.repo.DeleteBatch(t.Context(), env.userID, []uuid.UUID{
		free.ID, alsoFree.ID, usedByPack.ID, avatar.ID, voiced.ID, mates.ID, foreign.ID, missing,
	}, false)
	require.NoError(t, err)
	assert.ElementsMatch(t, []uuid.UUID{free.ID, alsoFree.ID}, outcome.Deleted)
	assert.ElementsMatch(t, []uuid.UUID{usedByPack.ID, avatar.ID, voiced.ID}, outcome.InUse,
		"аватар и TTS пропускаются наравне с файлом из набора")
	assert.Equal(t, int64(140), outcome.FreedBytes)

	// Квота уменьшается ровно на сумму размеров реально удалённых файлов.
	assert.Equal(t, int64(1205), env.storageUsed(env.orgID))
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
	mates := env.seed(env.orgID, env.mateID, "sha-mate", 40)
	foreign := env.seed(env.otherOrgID, env.strangerID, "sha-foreign", 9)

	outcome, err := env.repo.DeleteBatch(t.Context(), env.userID,
		[]uuid.UUID{mates.ID, foreign.ID, uuid.New()}, false)
	require.NoError(t, err)
	assert.Empty(t, outcome.Deleted, "пачка целиком из чужих файлов ничего не удаляет")
	assert.Empty(t, outcome.InUse)
	assert.Zero(t, outcome.FreedBytes)

	assert.Equal(t, int64(140), env.storageUsed(env.orgID), "квота своей организации не тронута")
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

func (e *mediaEnv) attachTTSJob(mediaID uuid.UUID) {
	_, err := e.pool.Exec(e.t.Context(), `
		INSERT INTO tts_jobs (org_id, text, voice, status, media_id)
		VALUES ($1, $2, 'alena', 'succeeded', $3)`, e.orgID, "text-"+mediaID.String(), mediaID)
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
