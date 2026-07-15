package testutil

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	pool         *pgxpool.Pool
	migrationsDB *sql.DB
	ctx          = context.Background()
)

func TestMain(m *testing.M) {
	dbPool, cleanup, err := NewPostgresCtx(ctx)
	if err != nil {
		slog.Error("start postgres container", "err", err)
		os.Exit(1)
	}
	pool = dbPool

	sqlDB, err := sql.Open("pgx", pool.Config().ConnString())
	if err != nil {
		slog.Error("open sql.DB for migrations", "err", err)
		cleanup()
		os.Exit(1)
	}
	migrationsDB = sqlDB

	if err := goose.Up(migrationsDB, "../../migrations"); err != nil {
		slog.Error("apply migrations", "err", err)
		migrationsDB.Close()
		cleanup()
		os.Exit(1)
	}

	code := m.Run()

	migrationsDB.Close()
	cleanup()
	os.Exit(code)
}

// ---------- helpers ----------

func insertTestOrg(t *testing.T) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO organizations (id, name)
		VALUES ($1, 'test-org')
	`, id)
	require.NoError(t, err)
	return id
}

func insertTestUser(t *testing.T, orgID uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO users (id, org_id, display_name)
		VALUES ($1, $2, 'Test User')
	`, id, orgID)
	require.NoError(t, err)
	return id
}

func insertTestStudent(t *testing.T, defectologistID uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO students (id, defectologist_id, email_encrypted, name, age, status)
		VALUES ($1, $2, $3, 'Test Student', 7, 'active')
	`, id, defectologistID, []byte("enc"+id.String()))
	require.NoError(t, err)
	return id
}

func insertTestFolder(t *testing.T, ownerID uuid.UUID, section, kind string, studentID *uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO folders (id, owner_id, section, kind, student_id)
		VALUES ($1, $2, $3, $4, $5)
	`, id, ownerID, section, kind, studentID)
	require.NoError(t, err)
	return id
}

func insertTestPack(t *testing.T, orgID, ownerID, folderID uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO packs (id, org_id, owner_id, folder_id, title, difficulty)
		VALUES ($1, $2, $3, $4, 'Test Pack', 'easy')
	`, id, orgID, ownerID, folderID)
	require.NoError(t, err)
	return id
}

func insertTestPackVersion(t *testing.T, packID, createdBy uuid.UUID, version int) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO pack_versions (id, pack_id, version, config, created_by)
		VALUES ($1, $2, $3, '{}', $4)
	`, id, packID, version, createdBy)
	require.NoError(t, err)
	return id
}

func insertTestPackAdaptation(t *testing.T, packID, studentID, createdBy uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO pack_adaptations (id, pack_id, student_id, config, created_by)
		VALUES ($1, $2, $3, '{}', $4)
	`, id, packID, studentID, createdBy)
	require.NoError(t, err)
	return id
}

func insertTestFavorite(t *testing.T, userID, packID uuid.UUID) {
	t.Helper()
	_, err := pool.Exec(ctx, `
		INSERT INTO favorite_packs (user_id, pack_id)
		VALUES ($1, $2)
	`, userID, packID)
	require.NoError(t, err)
}

func insertTestMediaFile(t *testing.T, orgID, uploaderID uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO media_files (id, org_id, uploader_id, status, source_text)
		VALUES ($1, $2, $3, 'pending', 'test tts text')
	`, id, orgID, uploaderID)
	require.NoError(t, err)
	return id
}

func insertTestMediaUsage(t *testing.T, mediaID uuid.UUID, sourceType string, sourceID uuid.UUID) {
	t.Helper()
	_, err := pool.Exec(ctx, `
		INSERT INTO media_usages (media_id, source_type, source_id)
		VALUES ($1, $2, $3)
	`, mediaID, sourceType, sourceID)
	require.NoError(t, err)
}

func truncateAll(t *testing.T) {
	t.Helper()
	_, err := pool.Exec(ctx, `
		TRUNCATE
			media_usages, media_files, favorite_packs,
			pack_adaptations, pack_versions, packs,
			folders, students, users, organizations
		RESTART IDENTITY CASCADE
	`)
	require.NoError(t, err)
}

// ---------- tests ----------

func TestPacksDifficultyCheck(t *testing.T) {
	defer truncateAll(t)

	orgID := insertTestOrg(t)
	ownerID := insertTestUser(t, orgID)
	folderID := insertTestFolder(t, ownerID, "my", "folder", nil)

	_, err := pool.Exec(ctx, `
		INSERT INTO packs (org_id, owner_id, folder_id, title, difficulty)
		VALUES ($1, $2, $3, 'bad pack', 'impossible')
	`, orgID, ownerID, folderID)

	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	assert.Equal(t, "23514", pgErr.Code)
}

func TestFoldersKindStudentIDCheck(t *testing.T) {
	defer truncateAll(t)

	orgID := insertTestOrg(t)
	ownerID := insertTestUser(t, orgID)
	studentID := insertTestStudent(t, ownerID)

	_, err := pool.Exec(ctx, `
		INSERT INTO folders (owner_id, section, kind, student_id)
		VALUES ($1, 'my', 'folder', $2)
	`, ownerID, studentID)

	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	assert.Equal(t, "23514", pgErr.Code)
	assert.Equal(t, "folders_kind_student_id_chk", pgErr.ConstraintName)
}

func TestDeletePackCascadesFavoritesAndVersions(t *testing.T) {
	defer truncateAll(t)

	orgID := insertTestOrg(t)
	ownerID := insertTestUser(t, orgID)
	folderID := insertTestFolder(t, ownerID, "my", "folder", nil)
	packID := insertTestPack(t, orgID, ownerID, folderID)

	insertTestPackVersion(t, packID, ownerID, 1)
	insertTestFavorite(t, ownerID, packID)

	_, err := pool.Exec(ctx, `DELETE FROM packs WHERE id = $1`, packID)
	require.NoError(t, err)

	var versionsCount, favoritesCount int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM pack_versions WHERE pack_id = $1`, packID).Scan(&versionsCount))
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM favorite_packs WHERE pack_id = $1`, packID).Scan(&favoritesCount))

	assert.Zero(t, versionsCount)
	assert.Zero(t, favoritesCount)
}

func TestMediaFilesDeleteRestrictedByUsage(t *testing.T) {
	defer truncateAll(t)

	orgID := insertTestOrg(t)
	ownerID := insertTestUser(t, orgID)
	folderID := insertTestFolder(t, ownerID, "my", "folder", nil)
	packID := insertTestPack(t, orgID, ownerID, folderID)
	mediaID := insertTestMediaFile(t, orgID, ownerID)

	insertTestMediaUsage(t, mediaID, "pack", packID)

	_, err := pool.Exec(ctx, `DELETE FROM media_files WHERE id = $1`, mediaID)

	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	assert.Equal(t, "23503", pgErr.Code)
}

func TestPackAdaptationsUniquePerStudent(t *testing.T) {
	defer truncateAll(t)

	orgID := insertTestOrg(t)
	ownerID := insertTestUser(t, orgID)
	folderID := insertTestFolder(t, ownerID, "my", "folder", nil)
	packID := insertTestPack(t, orgID, ownerID, folderID)
	studentID := insertTestStudent(t, ownerID)

	insertTestPackAdaptation(t, packID, studentID, ownerID)

	_, err := pool.Exec(ctx, `
		INSERT INTO pack_adaptations (pack_id, student_id, created_by)
		VALUES ($1, $2, $3)
	`, packID, studentID, ownerID)

	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	assert.Equal(t, "23505", pgErr.Code)
}

func TestMigrationsDownUpCycle(t *testing.T) {
	err := goose.DownTo(migrationsDB, "../../migrations", 0)
	require.NoError(t, err, "goose down-to 0 should succeed")

	var count int
	err = migrationsDB.QueryRow(`
		SELECT count(*) FROM information_schema.tables
		WHERE table_schema = 'public' AND table_name != 'goose_db_version'
	`).Scan(&count)
	require.NoError(t, err)
	assert.Zero(t, count, "all tables should be dropped after down-to 0")

	err = goose.Up(migrationsDB, "../../migrations")
	require.NoError(t, err, "goose up should succeed again after full rollback")
}
