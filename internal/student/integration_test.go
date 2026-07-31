package student

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/authctx"
	"github.com/Linka-masterskaya/zip-backend/internal/testutil"
	"github.com/Linka-masterskaya/zip-backend/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStudentCRUDScopeAndFolderDeleteConflict(t *testing.T) {
	pool := studentTestDB(t)
	ownerID := seedStudentUser(t, pool, "owner")
	foreignID := seedStudentUser(t, pool, "foreign")
	service := NewService(NewRepository(pool), identityCrypto{})

	created, err := service.Create(studentContext(ownerID), CreateInput{
		Email: " Student@Example.com ", Name: " Анна ", Age: intPtr(7),
	})
	require.NoError(t, err)
	assert.Equal(t, "student@example.com", created.Email)
	assert.Equal(t, "Анна", created.Name)
	assert.Equal(t, "active", created.Status)

	ownerList, err := service.List(studentContext(ownerID))
	require.NoError(t, err)
	require.Len(t, ownerList, 1)
	foreignList, err := service.List(studentContext(foreignID))
	require.NoError(t, err)
	assert.Empty(t, foreignList)

	newName := "Анна П."
	updated, err := service.Update(
		studentContext(ownerID), created.ID, UpdateInput{Name: &newName},
	)
	require.NoError(t, err)
	assert.Equal(t, newName, updated.Name)

	_, err = pool.Exec(context.Background(), `
		INSERT INTO folders (
			org_id, owner_id, section, kind, student_id, name, depth
		)
		SELECT org_id, id, 'students', 'student', $2, 'Анна', 0
		FROM users WHERE id = $1`, ownerID, created.ID)
	require.NoError(t, err)

	err = service.Delete(studentContext(ownerID), created.ID)
	assertStudentStatus(t, err, apperr.ErrConflict.HTTPStatus)
	_, err = pool.Exec(context.Background(), `DELETE FROM folders WHERE student_id = $1`, created.ID)
	require.NoError(t, err)
	require.NoError(t, service.Delete(studentContext(ownerID), created.ID))

	ownerList, err = service.List(studentContext(ownerID))
	require.NoError(t, err)
	assert.Empty(t, ownerList)
	err = service.Delete(studentContext(foreignID), created.ID)
	assertStudentStatus(t, err, apperr.ErrNotFound.HTTPStatus)
}

func TestStudentCreateRequiresEmail(t *testing.T) {
	pool := studentTestDB(t)
	ownerID := seedStudentUser(t, pool, "owner")
	service := NewService(NewRepository(pool), identityCrypto{})

	_, err := service.Create(studentContext(ownerID), CreateInput{Name: "No email"})
	assertStudentStatus(t, err, apperr.ErrBadRequest.HTTPStatus)
}

type identityCrypto struct{}

func (identityCrypto) Encrypt(value []byte) ([]byte, error) {
	return append([]byte(nil), value...), nil
}

func (identityCrypto) Decrypt(value []byte) ([]byte, error) {
	return append([]byte(nil), value...), nil
}

func studentTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, cleanup := testutil.NewPostgres(t)
	t.Cleanup(cleanup)
	db := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, applyStudentMigrations(db))
	return pool
}

func applyStudentMigrations(db *sql.DB) error {
	return migrations.Run(db)
}

func seedStudentUser(t *testing.T, pool *pgxpool.Pool, name string) uuid.UUID {
	t.Helper()
	orgID, userID := uuid.New(), uuid.New()
	_, err := pool.Exec(
		context.Background(),
		`INSERT INTO organizations (id, name) VALUES ($1, $2)`,
		orgID, name,
	)
	require.NoError(t, err)
	_, err = pool.Exec(
		context.Background(),
		`INSERT INTO users (id, org_id) VALUES ($1, $2)`,
		userID, orgID,
	)
	require.NoError(t, err)
	return userID
}

func studentContext(userID uuid.UUID) context.Context {
	ctx := authctx.SetUserIDToCtx(context.Background(), userID)
	return authctx.SetRoleToCtx(ctx, "defectologist")
}

func intPtr(value int) *int {
	return &value
}

func assertStudentStatus(t *testing.T, err error, status int) {
	t.Helper()
	var appErr *apperr.AppError
	require.Error(t, err)
	require.True(t, errors.As(err, &appErr))
	assert.Equal(t, status, appErr.HTTPStatus)
}
