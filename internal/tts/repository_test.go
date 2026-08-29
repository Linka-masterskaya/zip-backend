package tts

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

func TestCreateMediaIsolatesOrgs(t *testing.T) {
	pool, cleanup := testutil.NewPostgres(t)
	t.Cleanup(cleanup)
	db := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, applyTTSMigrations(db))

	ctx := t.Context()
	orgA, orgB := uuid.New(), uuid.New()
	userA, userB := uuid.New(), uuid.New()

	_, err := pool.Exec(ctx, `
		INSERT INTO organizations (id, name) VALUES ($1, 'org A'), ($2, 'org B')`, orgA, orgB)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO users (id, org_id, display_name) VALUES ($1, $3, 'User A'), ($2, $4, 'User B')`, userA, userB, orgA, orgB)
	require.NoError(t, err)

	input := MediaFileInput{
		MinioKey:  "tts/shared-key-abc",
		SHA256:    "digest",
		SizeBytes: 1024,
		MimeType:  "audio/mpeg",
		Name:      "test text",
	}

	repo := NewRepository(pool)

	// создаём job'ы для каждой орги
	jobA, jobB, jobA2 := uuid.New(), uuid.New(), uuid.New()
	_, err = pool.Exec(ctx, `
		INSERT INTO tts_jobs (id, org_id, text, voice, status) VALUES
		($1, $2, 'test', 'alena', 'in_progress'),
		($3, $4, 'test', 'alena', 'in_progress'),
		($5, $6, 'test2', 'alena', 'in_progress')`,
		jobA, orgA, jobB, orgB, jobA2, orgA)
	require.NoError(t, err)

	mediaA, err := repo.CreateMediaAndCompleteJob(ctx, jobA, orgA, userA, input)
	require.NoError(t, err)

	mediaB, err := repo.CreateMediaAndCompleteJob(ctx, jobB, orgB, userB, input)
	require.NoError(t, err)

	assert.NotEqual(t, mediaA, mediaB, "разные org должны получать разные media_files.id")

	var countA, countB int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM media_files WHERE id = $1 AND org_id = $2`,
		mediaA, orgA).Scan(&countA))
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM media_files WHERE id = $1 AND org_id = $2`,
		mediaB, orgB).Scan(&countB))
	assert.Equal(t, 1, countA)
	assert.Equal(t, 1, countB)

	// повторный вызов для той же орги — тот же media_id
	mediaAAgain, err := repo.CreateMediaAndCompleteJob(ctx, jobA2, orgA, userA, input)
	require.NoError(t, err)
	assert.Equal(t, mediaA, mediaAAgain, "повторный вызов для той же org должен вернуть ту же media_files.id")
}

func applyTTSMigrations(db *sql.DB) error {
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	return goose.Up(db, "../../migrations")
}
