package profile

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func setupDBTestUserRepo(t *testing.T, ctx context.Context) *sql.DB {
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

	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	require.Eventually(t, func() bool {
		return db.Ping() == nil
	}, 10*time.Second, 500*time.Millisecond)

	_, err = db.Exec(`
	CREATE TABLE users (
	id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email           TEXT UNIQUE NOT NULL,
    password_hash   TEXT
	);
	`)
	require.NoError(t, err)

	return db
}

// insertTestUser inserts a user row directly via SQL and returns its id.
func insertTestUser(t *testing.T, db *sql.DB, email, passwordHash string) string {
	t.Helper()
	var id string
	err := db.QueryRow(
		`INSERT INTO users (email, password_hash) VALUES ($1, $2) RETURNING id`,
		email, passwordHash,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

func TestUserRepo_Get_Success(t *testing.T) {
	db := setupDBTestUserRepo(t, context.Background())
	repo := NewUserRepo(db)

	id := insertTestUser(t, db, "get-success@example.com", "hashed-password")

	user, err := repo.Get(context.Background(), id)

	require.NoError(t, err)
	require.Equal(t, id, user.ID)
	require.Equal(t, "hashed-password", user.Password)
}

func TestUserRepo_Get_NotFound(t *testing.T) {
	db := setupDBTestUserRepo(t, context.Background())
	repo := NewUserRepo(db)

	user, err := repo.Get(context.Background(), "00000000-0000-0000-0000-000000000000")

	require.Nil(t, user)
	require.ErrorIs(t, err, sql.ErrNoRows)
}

func TestUserRepo_Update_Success(t *testing.T) {
	db := setupDBTestUserRepo(t, context.Background())
	repo := NewUserRepo(db)

	id := insertTestUser(t, db, "update-success@example.com", "old-hash")

	err := repo.Update(context.Background(), id, "new-hash")
	require.NoError(t, err)

	var storedHash string
	require.NoError(t, db.QueryRow(`SELECT password_hash FROM users WHERE id=$1`, id).Scan(&storedHash))
	require.Equal(t, "new-hash", storedHash)
}

func TestUserRepo_Update_UnknownID_NoError(t *testing.T) {
	db := setupDBTestUserRepo(t, context.Background())
	repo := NewUserRepo(db)

	// Update issues an UPDATE without checking rows affected, so an unknown
	// id is a silent no-op rather than an error.
	err := repo.Update(context.Background(), "00000000-0000-0000-0000-000000000000", "new-hash")

	require.NoError(t, err)
}
