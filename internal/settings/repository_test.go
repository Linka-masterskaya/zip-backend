package settings

import (
	"context"
	"database/sql"
	"testing"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/testutil"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRepositorySettingsAndTemplatesAreUserScoped(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool, cleanup := testutil.NewPostgres(t)
	defer cleanup()
	db := stdlib.OpenDBFromPool(pool)
	defer db.Close()
	require.NoError(t, applySettingsMigrations(db))

	ctx := context.Background()
	userA := seedSettingsUser(t, pool, "User A")
	userB := seedSettingsUser(t, pool, "User B")
	repo := NewRepository(pool)

	got, err := repo.Get(ctx, userA)
	require.NoError(t, err)
	assert.JSONEq(t, `{}`, string(got))

	require.NoError(t, repo.Put(ctx, userA, []byte(`{"voice":"alena"}`)))
	got, err = repo.Get(ctx, userA)
	require.NoError(t, err)
	assert.JSONEq(t, `{"voice":"alena"}`, string(got))
	other, err := repo.Get(ctx, userB)
	require.NoError(t, err)
	assert.JSONEq(t, `{}`, string(other))

	created, err := repo.CreateTemplate(ctx, userA, "Contrast", []byte(`{"colors":{"background":"black"}}`))
	require.NoError(t, err)
	assert.Equal(t, "Contrast", created.Name)

	items, err := repo.ListTemplates(ctx, userA)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, created.ID, items[0].ID)

	foreignItems, err := repo.ListTemplates(ctx, userB)
	require.NoError(t, err)
	assert.Empty(t, foreignItems)

	err = repo.DeleteTemplate(ctx, userB, created.ID)
	require.ErrorIs(t, err, apperr.ErrNotFound)
	items, err = repo.ListTemplates(ctx, userA)
	require.NoError(t, err)
	require.Len(t, items, 1)

	require.NoError(t, repo.DeleteTemplate(ctx, userA, created.ID))
	items, err = repo.ListTemplates(ctx, userA)
	require.NoError(t, err)
	assert.Empty(t, items)
}

func applySettingsMigrations(db *sql.DB) error {
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	return goose.Up(db, "../../migrations")
}

func seedSettingsUser(t *testing.T, pool *pgxpool.Pool, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := pool.Exec(context.Background(), `INSERT INTO users (id, display_name) VALUES ($1, $2)`, id, name)
	require.NoError(t, err)
	return id
}
