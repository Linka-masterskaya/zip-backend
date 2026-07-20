package profile

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Linka-masterskaya/zip-backend/internal/testutil"
)

// ============ Test Helpers for Repository Tests ============

// runMigrationsRepo creates all necessary tables for repository tests.
func runMigrationsRepo(db *sql.DB) error {
	ctx := context.Background()

	// Create users table
	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS users (
			id UUID PRIMARY KEY,
			email TEXT NOT NULL UNIQUE,
			email_verified BOOLEAN NOT NULL DEFAULT FALSE,
			display_name TEXT,
			avatar_key TEXT,
			org_id UUID,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			deleted_at TIMESTAMPTZ
		)
	`)
	if err != nil {
		return err
	}

	// Create organizations table
	_, err = db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS organizations (
			id UUID PRIMARY KEY,
			storage_used_bytes BIGINT NOT NULL DEFAULT 0,
			storage_quota_bytes BIGINT NOT NULL DEFAULT 10737418240
		)
	`)
	if err != nil {
		return err
	}

	// Create auth_cred table
	_, err = db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS auth_cred (
			user_id UUID PRIMARY KEY REFERENCES users(id),
			email_hash BYTEA NOT NULL,
			email_encrypted BYTEA NOT NULL,
			password_hash TEXT,
			role TEXT NOT NULL DEFAULT 'user'
		)
	`)
	if err != nil {
		return err
	}

	// Create verify_tokens table
	_, err = db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS verify_tokens (
			id UUID PRIMARY KEY,
			user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			purpose TEXT NOT NULL,
			token_hash BYTEA NOT NULL,
			payload BYTEA,
			expires_at TIMESTAMPTZ NOT NULL,
			used_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`)
	if err != nil {
		return err
	}

	// Create index for token lookup
	_, err = db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS verify_tokens_token_hash_idx ON verify_tokens (token_hash)
	`)
	return err
}

// insertTestUserRepo inserts a test user for repository tests.
func insertTestUserRepo(ctx context.Context, db *sql.DB, id uuid.UUID, email string) error {
	// Insert into users table
	_, err := db.ExecContext(ctx, `
		INSERT INTO users (id, email, email_verified, display_name, created_at, updated_at)
		VALUES ($1, $2, $3, $4, now(), now())
	`, id, email, true, "Test User")
	if err != nil {
		return err
	}

	// Insert into auth_cred table
	_, err = db.ExecContext(ctx, `
		INSERT INTO auth_cred (user_id, email_hash, email_encrypted, role)
		VALUES ($1, $2, $3, 'user')
	`, id, []byte(email), []byte(email))
	if err != nil {
		return err
	}

	// Insert into organizations table
	orgID := uuid.New()
	_, err = db.ExecContext(ctx, `
		INSERT INTO organizations (id, storage_used_bytes, storage_quota_bytes)
		VALUES ($1, 0, 10737418240)
	`, orgID)
	if err != nil {
		return err
	}

	// Update user with org_id
	_, err = db.ExecContext(ctx, `
		UPDATE users SET org_id = $1 WHERE id = $2
	`, orgID, id)
	return err
}

// ============ Repository Tests ============

func TestRepository_CreateToken(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dbPool, cleanup := testutil.NewPostgres(t)
	defer cleanup()

	db, err := sql.Open("pgx", dbPool.Config().ConnString())
	require.NoError(t, err)
	defer db.Close()

	require.NoError(t, runMigrationsRepo(db))

	repo := NewRepository(dbPool)

	ctx := context.Background()
	userID := uuid.New()
	oldEmail := "old@example.com"

	require.NoError(t, insertTestUserRepo(ctx, db, userID, oldEmail))

	token := &Token{
		ID:        uuid.New().String(),
		UserID:    userID,
		Type:      TokenTypeEmailChange,
		Token:     "test-token",
		TokenHash: []byte("test-hash"),
		Payload:   `{"new_email":"new@example.com","old_email":"old@example.com"}`,
		Used:      false,
		ExpiresAt: time.Now().Add(24 * time.Hour),
		CreatedAt: time.Now(),
	}

	err = repo.CreateToken(ctx, token)
	assert.NoError(t, err)
}

func TestRepository_FindTokenByHash(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dbPool, cleanup := testutil.NewPostgres(t)
	defer cleanup()

	db, err := sql.Open("pgx", dbPool.Config().ConnString())
	require.NoError(t, err)
	defer db.Close()

	require.NoError(t, runMigrationsRepo(db))

	repo := NewRepository(dbPool)

	ctx := context.Background()
	userID := uuid.New()
	oldEmail := "old@example.com"

	require.NoError(t, insertTestUserRepo(ctx, db, userID, oldEmail))

	// Create token
	token := &Token{
		ID:        uuid.New().String(),
		UserID:    userID,
		Type:      TokenTypeEmailChange,
		Token:     "test-token",
		TokenHash: []byte("test-hash"),
		Payload:   `{"new_email":"new@example.com","old_email":"old@example.com"}`,
		Used:      false,
		ExpiresAt: time.Now().Add(24 * time.Hour),
		CreatedAt: time.Now(),
	}

	err = repo.CreateToken(ctx, token)
	require.NoError(t, err)

	// Find by hash
	found, err := repo.FindTokenByHash(ctx, token.TokenHash)
	assert.NoError(t, err)
	assert.Equal(t, token.ID, found.ID)
	assert.Equal(t, token.UserID, found.UserID)
	assert.Equal(t, token.Type, found.Type)
	assert.Equal(t, token.Payload, found.Payload)
	assert.False(t, found.Used)
}

func TestRepository_MarkTokenUsed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dbPool, cleanup := testutil.NewPostgres(t)
	defer cleanup()

	db, err := sql.Open("pgx", dbPool.Config().ConnString())
	require.NoError(t, err)
	defer db.Close()

	require.NoError(t, runMigrationsRepo(db))

	repo := NewRepository(dbPool)

	ctx := context.Background()
	userID := uuid.New()
	oldEmail := "old@example.com"

	require.NoError(t, insertTestUserRepo(ctx, db, userID, oldEmail))

	// Create token
	token := &Token{
		ID:        uuid.New().String(),
		UserID:    userID,
		Type:      TokenTypeEmailChange,
		Token:     "test-token",
		TokenHash: []byte("test-hash"),
		Payload:   `{"new_email":"new@example.com","old_email":"old@example.com"}`,
		Used:      false,
		ExpiresAt: time.Now().Add(24 * time.Hour),
		CreatedAt: time.Now(),
	}

	err = repo.CreateToken(ctx, token)
	require.NoError(t, err)

	// Mark as used
	err = repo.MarkTokenUsed(ctx, token.ID)
	assert.NoError(t, err)

	// Verify
	found, err := repo.FindTokenByHash(ctx, token.TokenHash)
	assert.NoError(t, err)
	assert.True(t, found.Used)
}

func TestRepository_DeleteToken(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dbPool, cleanup := testutil.NewPostgres(t)
	defer cleanup()

	db, err := sql.Open("pgx", dbPool.Config().ConnString())
	require.NoError(t, err)
	defer db.Close()

	require.NoError(t, runMigrationsRepo(db))

	repo := NewRepository(dbPool)

	ctx := context.Background()
	userID := uuid.New()
	oldEmail := "old@example.com"

	require.NoError(t, insertTestUserRepo(ctx, db, userID, oldEmail))

	// Create token
	token := &Token{
		ID:        uuid.New().String(),
		UserID:    userID,
		Type:      TokenTypeEmailChange,
		Token:     "test-token",
		TokenHash: []byte("test-hash"),
		Payload:   `{"new_email":"new@example.com","old_email":"old@example.com"}`,
		Used:      false,
		ExpiresAt: time.Now().Add(24 * time.Hour),
		CreatedAt: time.Now(),
	}

	err = repo.CreateToken(ctx, token)
	require.NoError(t, err)

	// Delete token
	err = repo.DeleteToken(ctx, token.ID)
	assert.NoError(t, err)

	// Verify token is gone
	_, err = repo.FindTokenByHash(ctx, token.TokenHash)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrTokenNotFound)
}

func TestRepository_DeleteExpiredTokens(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dbPool, cleanup := testutil.NewPostgres(t)
	defer cleanup()

	db, err := sql.Open("pgx", dbPool.Config().ConnString())
	require.NoError(t, err)
	defer db.Close()

	require.NoError(t, runMigrationsRepo(db))

	repo := NewRepository(dbPool)

	ctx := context.Background()
	userID := uuid.New()
	oldEmail := "old@example.com"

	require.NoError(t, insertTestUserRepo(ctx, db, userID, oldEmail))

	// Create expired token
	expiredToken := &Token{
		ID:        uuid.New().String(),
		UserID:    userID,
		Type:      TokenTypeEmailChange,
		Token:     "expired-token",
		TokenHash: []byte("expired-hash"),
		Payload:   `{"new_email":"new@example.com","old_email":"old@example.com"}`,
		Used:      false,
		ExpiresAt: time.Now().Add(-1 * time.Hour),
		CreatedAt: time.Now(),
	}

	err = repo.CreateToken(ctx, expiredToken)
	require.NoError(t, err)

	// Create valid token
	validToken := &Token{
		ID:        uuid.New().String(),
		UserID:    userID,
		Type:      TokenTypeEmailChange,
		Token:     "valid-token",
		TokenHash: []byte("valid-hash"),
		Payload:   `{"new_email":"new@example.com","old_email":"old@example.com"}`,
		Used:      false,
		ExpiresAt: time.Now().Add(24 * time.Hour),
		CreatedAt: time.Now(),
	}

	err = repo.CreateToken(ctx, validToken)
	require.NoError(t, err)

	// Delete expired tokens
	deleted, err := repo.DeleteExpiredTokens(ctx)
	assert.NoError(t, err)
	assert.Equal(t, int64(1), deleted)

	// Verify expired token is gone
	_, err = repo.FindTokenByHash(ctx, expiredToken.TokenHash)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrTokenNotFound)

	// Verify valid token still exists
	found, err := repo.FindTokenByHash(ctx, validToken.TokenHash)
	assert.NoError(t, err)
	assert.Equal(t, validToken.ID, found.ID)
}

func TestRepository_FindByID(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dbPool, cleanup := testutil.NewPostgres(t)
	defer cleanup()

	db, err := sql.Open("pgx", dbPool.Config().ConnString())
	require.NoError(t, err)
	defer db.Close()

	require.NoError(t, runMigrationsRepo(db))

	repo := NewRepository(dbPool)

	ctx := context.Background()
	userID := uuid.New()
	email := "test@example.com"

	require.NoError(t, insertTestUserRepo(ctx, db, userID, email))

	user, err := repo.FindByID(ctx, userID)
	assert.NoError(t, err)
	assert.Equal(t, userID.String(), user.ID)
	assert.Equal(t, email, user.Email)
	assert.True(t, user.EmailVerified)
}

func TestRepository_FindByEmail(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dbPool, cleanup := testutil.NewPostgres(t)
	defer cleanup()

	db, err := sql.Open("pgx", dbPool.Config().ConnString())
	require.NoError(t, err)
	defer db.Close()

	require.NoError(t, runMigrationsRepo(db))

	repo := NewRepository(dbPool)

	ctx := context.Background()
	userID := uuid.New()
	email := "test@example.com"

	require.NoError(t, insertTestUserRepo(ctx, db, userID, email))

	user, err := repo.FindByEmail(ctx, email)
	assert.NoError(t, err)
	assert.Equal(t, userID.String(), user.ID)
	assert.Equal(t, email, user.Email)
	assert.True(t, user.EmailVerified)
}

func TestRepository_Update(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dbPool, cleanup := testutil.NewPostgres(t)
	defer cleanup()

	db, err := sql.Open("pgx", dbPool.Config().ConnString())
	require.NoError(t, err)
	defer db.Close()

	require.NoError(t, runMigrationsRepo(db))

	repo := NewRepository(dbPool)

	ctx := context.Background()
	userID := uuid.New()
	oldEmail := "old@example.com"
	newEmail := "new@example.com"

	require.NoError(t, insertTestUserRepo(ctx, db, userID, oldEmail))

	// Get user
	user, err := repo.FindByID(ctx, userID)
	require.NoError(t, err)

	// Update email
	user.Email = newEmail
	user.EmailVerified = false

	err = repo.Update(ctx, user)
	assert.NoError(t, err)

	// Verify
	updated, err := repo.FindByID(ctx, userID)
	assert.NoError(t, err)
	assert.Equal(t, newEmail, updated.Email)
	assert.False(t, updated.EmailVerified)
}
