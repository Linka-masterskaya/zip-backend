package settings

import (
	"context"
	"database/sql"
	"net/http"
	"sync"
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

	stored, err := repo.Put(ctx, userA, []byte(`{ "voice": "alena" }`))
	require.NoError(t, err)
	assert.JSONEq(t, `{"voice":"alena"}`, string(stored))
	got, err = repo.Get(ctx, userA)
	require.NoError(t, err)
	assert.Equal(t, string(stored), string(got), "Put must return the same JSONB representation that Get reads")
	assert.JSONEq(t, `{"voice":"alena"}`, string(got))
	other, err := repo.Get(ctx, userB)
	require.NoError(t, err)
	assert.JSONEq(t, `{}`, string(other))

	created, err := repo.CreateTemplate(ctx, userA, "Contrast", []byte(`{"colors":{"background":"#000000"}}`))
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

func TestRepositoryTemplateQuotaAndUniqueName(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool, cleanup := testutil.NewPostgres(t)
	defer cleanup()
	db := stdlib.OpenDBFromPool(pool)
	defer db.Close()
	require.NoError(t, applySettingsMigrations(db))

	ctx := context.Background()
	userID := seedSettingsUser(t, pool, "Quota User")
	repo := NewRepository(pool)

	created, err := repo.CreateTemplate(ctx, userID, "Contrast", []byte(`{"colors":{"background":"#000000"}}`))
	require.NoError(t, err)
	require.NotNil(t, created)

	_, err = repo.CreateTemplate(ctx, userID, "Contrast", []byte(`{}`))
	require.Error(t, err)
	var appErr *apperr.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, "template name already exists", appErr.Message)
	assert.Equal(t, http.StatusConflict, appErr.HTTPStatus)

	require.NoError(t, repo.DeleteTemplate(ctx, userID, created.ID))
	_, err = pool.Exec(ctx, `
		INSERT INTO user_setting_templates (user_id, name, body)
		SELECT $1, 'template-' || n::text, '{}'::jsonb
		FROM generate_series(1, $2::int) AS n
	`, userID, MaxTemplatesPerUser-1)
	require.NoError(t, err)

	// Two concurrent creates starting at 99 rows must result in exactly one
	// insert and one quota conflict. The per-user advisory lock makes the
	// COUNT+INSERT sequence atomic across application instances.
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, name := range []string{"Concurrent A", "Concurrent B"} {
		wg.Add(1)
		go func(templateName string) {
			defer wg.Done()
			_, createErr := repo.CreateTemplate(ctx, userID, templateName, []byte(`{}`))
			errs <- createErr
		}(name)
	}
	wg.Wait()
	close(errs)

	var successCount, conflictCount int
	for createErr := range errs {
		if createErr == nil {
			successCount++
			continue
		}
		var quotaErr *apperr.AppError
		require.ErrorAs(t, createErr, &quotaErr)
		assert.Equal(t, http.StatusConflict, quotaErr.HTTPStatus)
		assert.Equal(t, "template limit of 100 reached", quotaErr.Message)
		conflictCount++
	}
	assert.Equal(t, 1, successCount)
	assert.Equal(t, 1, conflictCount)

	var count int
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT count(*) FROM user_setting_templates WHERE user_id = $1
	`, userID).Scan(&count))
	assert.Equal(t, MaxTemplatesPerUser, count)

	_, err = repo.CreateTemplate(ctx, userID, "Overflow", []byte(`{}`))
	require.Error(t, err)
	appErr = nil
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, "template limit of 100 reached", appErr.Message)
	assert.Equal(t, http.StatusConflict, appErr.HTTPStatus)
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
