package profile

import (
	"context"
	"database/sql"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

func TestChangePasswordRepo_Get_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := getTestPool()
	repo := NewChangePasswordRepo(pool)

	email := "get-success@example.com"
	passwordHash := "hashed-password"
	userID := insertTestUserForPassword(t, pool, email, passwordHash)

	user, err := repo.Get(context.Background(), userID)

	require.NoError(t, err)
	require.Equal(t, userID, user.ID)
	require.Equal(t, passwordHash, user.Password)
}

func TestChangePasswordRepo_Get_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := getTestPool()
	repo := NewChangePasswordRepo(pool)

	user, err := repo.Get(context.Background(), uuid.Nil)

	require.Nil(t, user)
	require.ErrorIs(t, err, sql.ErrNoRows)
}

func TestChangePasswordRepo_Update_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := getTestPool()
	repo := NewChangePasswordRepo(pool)

	email := "update-success@example.com"
	oldHash := "old-hash"
	newHash := "new-hash"

	userID := insertTestUserForPassword(t, pool, email, oldHash)

	err := repo.Update(context.Background(), userID, newHash)
	require.NoError(t, err)

	var storedHash string
	err = pool.QueryRow(context.Background(), `SELECT password_hash FROM auth_cred WHERE user_id=$1`, userID).Scan(&storedHash)
	require.NoError(t, err)
	require.Equal(t, newHash, storedHash)
}

func TestChangePasswordRepo_Update_UnknownID_NoRows(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := getTestPool()
	repo := NewChangePasswordRepo(pool)

	err := repo.Update(context.Background(), uuid.Nil, "new-hash")

	require.ErrorIs(t, err, sql.ErrNoRows)
}

// insertTestUserForPassword - helper для тестов смены пароля.
func insertTestUserForPassword(t *testing.T, pool *pgxpool.Pool, email, passwordHash string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	crypto := getTestCrypto()

	userID := uuid.New()

	_, err := pool.Exec(ctx, `
		INSERT INTO users (id, email_verified, display_name, created_at, updated_at)
		VALUES ($1, $2, $3, now(), now())
	`, userID, true, "Test User")
	require.NoError(t, err)

	emailEncrypted, err := crypto.Encrypt([]byte(email))
	require.NoError(t, err)

	emailHash := crypto.Hash([]byte(email))
	_, err = pool.Exec(ctx, `
		INSERT INTO auth_cred (user_id, email_hash, email_encrypted, password_hash, role)
		VALUES ($1, $2, $3, $4, 'defectologist')
	`, userID, emailHash, emailEncrypted, passwordHash)
	require.NoError(t, err)

	orgID := uuid.New()
	_, err = pool.Exec(ctx, `
		INSERT INTO organizations (id, name, storage_used_bytes, storage_quota_bytes)
		VALUES ($1, 'Test Org', 0, 10737418240)
	`, orgID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		UPDATE users SET org_id = $1 WHERE id = $2
	`, orgID, userID)
	require.NoError(t, err)

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM verify_tokens WHERE user_id = $1", userID.String())
		_, _ = pool.Exec(ctx, "DELETE FROM auth_cred WHERE user_id = $1", userID.String())
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = $1", userID.String())
	})

	return userID
}
