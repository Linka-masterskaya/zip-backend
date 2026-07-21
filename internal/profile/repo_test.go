package profile

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func setupDBTestUserRepo(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	req := testcontainers.ContainerRequest{
		Image:        "postgres:15",
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_USER":     "test",
			"POSTGRES_PASSWORD": "test",
			"POSTGRES_DB":       "testdb",
		},
		WaitingFor: wait.ForListeningPort("5432/tcp").
			WithStartupTimeout(30 * time.Second),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.NoError(t, err)

	t.Cleanup(func() {
		if err := container.Terminate(ctx); err != nil {
			t.Logf("terminate postgres container: %v", err)
		}
	})

	host, err := container.Host(ctx)
	require.NoError(t, err)

	port, err := container.MappedPort(ctx, "5432")
	require.NoError(t, err)

	dsn := fmt.Sprintf(
		"postgres://test:test@%s:%s/testdb?sslmode=disable",
		host,
		port.Port(),
	)

	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	require.Eventually(t, func() bool {
		return pool.Ping(ctx) == nil
	}, 10*time.Second, 500*time.Millisecond)

	_, err = pool.Exec(ctx, `
	CREATE TABLE users (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		email TEXT UNIQUE NOT NULL
	);
	CREATE TABLE auth_cred (
		user_id       UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
		password_hash TEXT,
		updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
	);
	`)
	require.NoError(t, err)

	return pool
}

// insertTestUser inserts a user and its auth_cred row directly via SQL and
// returns the user id.
func insertTestUser(t *testing.T, pool *pgxpool.Pool, email, passwordHash string) uuid.UUID {
	t.Helper()
	ctx := context.Background()

	var id uuid.UUID
	err := pool.QueryRow(ctx, `INSERT INTO users (email) VALUES ($1) RETURNING id`, email).Scan(&id)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `INSERT INTO auth_cred (user_id, password_hash) VALUES ($1, $2)`, id, passwordHash)
	require.NoError(t, err)

	return id
}

func TestChangePasswordRepo_Get_Success(t *testing.T) {
	db := setupDBTestUserRepo(t, context.Background())
	repo := NewChangePasswordRepo(db)

	id := insertTestUser(t, db, "get-success@example.com", "hashed-password")

	user, err := repo.Get(context.Background(), id)

	require.NoError(t, err)
	require.Equal(t, id, user.ID)
	require.Equal(t, "hashed-password", user.Password)
}

func TestChangePasswordRepo_Get_NotFound(t *testing.T) {
	db := setupDBTestUserRepo(t, context.Background())
	repo := NewChangePasswordRepo(db)

	user, err := repo.Get(context.Background(), uuid.Nil)

	require.Nil(t, user)
	require.ErrorIs(t, err, sql.ErrNoRows)
}

func TestChangePasswordRepo_Update_Success(t *testing.T) {
	db := setupDBTestUserRepo(t, context.Background())
	repo := NewChangePasswordRepo(db)

	id := insertTestUser(t, db, "update-success@example.com", "old-hash")

	err := repo.Update(context.Background(), id, "new-hash")
	require.NoError(t, err)

	var storedHash string
	require.NoError(t, db.QueryRow(context.Background(), `SELECT password_hash FROM auth_cred WHERE user_id=$1`, id).Scan(&storedHash))
	require.Equal(t, "new-hash", storedHash)
}

func TestChangePasswordRepo_Update_UnknownID_NoRows(t *testing.T) {
	db := setupDBTestUserRepo(t, context.Background())
	repo := NewChangePasswordRepo(db)

	err := repo.Update(context.Background(), uuid.Nil, "new-hash")

	require.ErrorIs(t, err, sql.ErrNoRows)
}
