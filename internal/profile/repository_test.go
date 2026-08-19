package profile

import (
	"context"
	"crypto/sha256"
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

// insertTestUserRepo inserts a test user for repository tests.
func insertTestUserRepo(ctx context.Context, db *sql.DB, id uuid.UUID, email string) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO users (id, email_verified, display_name, created_at, updated_at)
		VALUES ($1, $2, $3, now(), now())
	`, id, true, "Test User")
	if err != nil {
		return err
	}

	// В репозитории мы не дешифруем email через cryptox,
	// поэтому можно сохранять как есть
	emailHash := sha256.Sum256([]byte(email))
	_, err = db.ExecContext(ctx, `
		INSERT INTO auth_cred (user_id, email_hash, email_encrypted, role)
		VALUES ($1, $2, $3, 'defectologist')
	`, id, emailHash[:], []byte(email))
	if err != nil {
		return err
	}

	orgID := uuid.New()
	_, err = db.ExecContext(ctx, `
		INSERT INTO organizations (id, name, storage_used_bytes, storage_quota_bytes)
		VALUES ($1, 'Test Org', 0, 10737418240)
	`, orgID)
	if err != nil {
		return err
	}

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

	require.NoError(t, runMigrations(db))

	repo := NewRepository(dbPool)

	ctx := context.Background()
	userID := uuid.New()
	email := "test@example.com"

	require.NoError(t, insertTestUserRepo(ctx, db, userID, email))

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

	require.NoError(t, runMigrations(db))

	repo := NewRepository(dbPool)

	ctx := context.Background()
	userID := uuid.New()
	email := "test@example.com"

	require.NoError(t, insertTestUserRepo(ctx, db, userID, email))

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

	require.NoError(t, runMigrations(db))

	repo := NewRepository(dbPool)

	ctx := context.Background()
	userID := uuid.New()
	email := "test@example.com"

	require.NoError(t, insertTestUserRepo(ctx, db, userID, email))

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

	err = repo.MarkTokenUsed(ctx, token.ID)
	assert.NoError(t, err)

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

	require.NoError(t, runMigrations(db))

	repo := NewRepository(dbPool)

	ctx := context.Background()
	userID := uuid.New()
	email := "test@example.com"

	require.NoError(t, insertTestUserRepo(ctx, db, userID, email))

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

	err = repo.DeleteToken(ctx, token.ID)
	assert.NoError(t, err)

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

	require.NoError(t, runMigrations(db))

	repo := NewRepository(dbPool)

	ctx := context.Background()
	userID := uuid.New()
	email := "test@example.com"

	require.NoError(t, insertTestUserRepo(ctx, db, userID, email))

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

	deleted, err := repo.DeleteExpiredTokens(ctx)
	assert.NoError(t, err)
	assert.Equal(t, int64(1), deleted)

	_, err = repo.FindTokenByHash(ctx, expiredToken.TokenHash)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrTokenNotFound)

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

	require.NoError(t, runMigrations(db))

	repo := NewRepository(dbPool)

	ctx := context.Background()
	userID := uuid.New()
	email := "test@example.com"

	require.NoError(t, insertTestUserRepo(ctx, db, userID, email))

	user, err := repo.FindByID(ctx, userID)
	assert.NoError(t, err)
	assert.Equal(t, userID.String(), user.ID)
	assert.NotEqual(t, email, user.Email)
	assert.True(t, user.EmailVerified)
}

func TestRepository_FindByEmailHash(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dbPool, cleanup := testutil.NewPostgres(t)
	defer cleanup()

	db, err := sql.Open("pgx", dbPool.Config().ConnString())
	require.NoError(t, err)
	defer db.Close()

	require.NoError(t, runMigrations(db))

	repo := NewRepository(dbPool)

	ctx := context.Background()
	userID := uuid.New()
	email := "test@example.com"

	require.NoError(t, insertTestUserRepo(ctx, db, userID, email))

	emailHash := sha256.Sum256([]byte(email))
	user, err := repo.FindByEmailHash(ctx, emailHash[:])
	assert.NoError(t, err)
	assert.Equal(t, userID.String(), user.ID)
	assert.NotEqual(t, email, user.Email)
	assert.True(t, user.EmailVerified)
}

func TestRepository_FindByEmailHash_IncludesSoftDeletedUser(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dbPool, cleanup := testutil.NewPostgres(t)
	defer cleanup()

	db, err := sql.Open("pgx", dbPool.Config().ConnString())
	require.NoError(t, err)
	defer db.Close()

	require.NoError(t, runMigrations(db))

	repo := NewRepository(dbPool)
	ctx := context.Background()
	userID := uuid.New()
	email := "reserved@example.com"

	require.NoError(t, insertTestUserRepo(ctx, db, userID, email))
	_, err = db.ExecContext(ctx, `UPDATE users SET deleted_at = now() WHERE id = $1`, userID)
	require.NoError(t, err)

	emailHash := sha256.Sum256([]byte(email))
	user, err := repo.FindByEmailHash(ctx, emailHash[:])
	require.NoError(t, err)
	assert.Equal(t, userID.String(), user.ID)
}

func TestRepository_UpdateEmailWithTx(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dbPool, cleanup := testutil.NewPostgres(t)
	defer cleanup()

	db, err := sql.Open("pgx", dbPool.Config().ConnString())
	require.NoError(t, err)
	defer db.Close()

	require.NoError(t, runMigrations(db))

	repo := NewRepository(dbPool)

	ctx := context.Background()
	userID := uuid.New()
	oldEmail := "old@example.com"
	newEmail := "new@example.com"

	require.NoError(t, insertTestUserRepo(ctx, db, userID, oldEmail))

	tx, err := repo.BeginTx(ctx)
	require.NoError(t, err)
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	emailEncrypted := []byte(newEmail)
	emailHash := sha256.Sum256([]byte(newEmail))

	err = repo.UpdateEmailWithTx(ctx, tx, userID, emailEncrypted, emailHash[:], false)
	assert.NoError(t, err)

	err = tx.Commit(ctx)
	assert.NoError(t, err)

	user, err := repo.FindByID(ctx, userID)
	assert.NoError(t, err)
	assert.NotEqual(t, newEmail, user.Email)
	assert.False(t, user.EmailVerified)
}

func TestRepository_UpdateEmailWithTx_DuplicateEmail(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dbPool, cleanup := testutil.NewPostgres(t)
	defer cleanup()

	db, err := sql.Open("pgx", dbPool.Config().ConnString())
	require.NoError(t, err)
	defer db.Close()

	require.NoError(t, runMigrations(db))

	repo := NewRepository(dbPool)
	ctx := context.Background()
	userID := uuid.New()
	otherUserID := uuid.New()
	oldEmail := "old@example.com"
	takenEmail := "taken@example.com"

	require.NoError(t, insertTestUserRepo(ctx, db, userID, oldEmail))
	require.NoError(t, insertTestUserRepo(ctx, db, otherUserID, takenEmail))

	tx, err := repo.BeginTx(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	emailHash := sha256.Sum256([]byte(takenEmail))
	err = repo.UpdateEmailWithTx(ctx, tx, userID, []byte(takenEmail), emailHash[:], false)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEmailAlreadyUsed)
}

func TestRepository_BeginTx(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dbPool, cleanup := testutil.NewPostgres(t)
	defer cleanup()

	db, err := sql.Open("pgx", dbPool.Config().ConnString())
	require.NoError(t, err)
	defer db.Close()

	require.NoError(t, runMigrations(db))

	repo := NewRepository(dbPool)

	ctx := context.Background()
	userID := uuid.New()
	email := "test@example.com"

	require.NoError(t, insertTestUserRepo(ctx, db, userID, email))

	tx, err := repo.BeginTx(ctx)
	assert.NoError(t, err)

	err = tx.Rollback(ctx)
	assert.NoError(t, err)
}

func TestRepository_FindByIDWithTx(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dbPool, cleanup := testutil.NewPostgres(t)
	defer cleanup()

	db, err := sql.Open("pgx", dbPool.Config().ConnString())
	require.NoError(t, err)
	defer db.Close()

	require.NoError(t, runMigrations(db))

	repo := NewRepository(dbPool)

	ctx := context.Background()
	userID := uuid.New()
	email := "test@example.com"

	require.NoError(t, insertTestUserRepo(ctx, db, userID, email))

	tx, err := repo.BeginTx(ctx)
	require.NoError(t, err)
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	user, err := repo.FindByIDWithTx(ctx, tx, userID)
	assert.NoError(t, err)
	assert.Equal(t, userID.String(), user.ID)
	assert.True(t, user.EmailVerified)

	err = tx.Commit(ctx)
	assert.NoError(t, err)
}

func TestRepository_FindByEmailHashWithTx(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dbPool, cleanup := testutil.NewPostgres(t)
	defer cleanup()

	db, err := sql.Open("pgx", dbPool.Config().ConnString())
	require.NoError(t, err)
	defer db.Close()

	require.NoError(t, runMigrations(db))

	repo := NewRepository(dbPool)

	ctx := context.Background()
	userID := uuid.New()
	email := "test@example.com"

	require.NoError(t, insertTestUserRepo(ctx, db, userID, email))

	tx, err := repo.BeginTx(ctx)
	require.NoError(t, err)
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	emailHash := sha256.Sum256([]byte(email))
	user, err := repo.FindByEmailHashWithTx(ctx, tx, emailHash[:])
	assert.NoError(t, err)
	assert.Equal(t, userID.String(), user.ID)
	assert.True(t, user.EmailVerified)

	err = tx.Commit(ctx)
	assert.NoError(t, err)
}

func TestRepository_MarkTokenUsedWithTx(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dbPool, cleanup := testutil.NewPostgres(t)
	defer cleanup()

	db, err := sql.Open("pgx", dbPool.Config().ConnString())
	require.NoError(t, err)
	defer db.Close()

	require.NoError(t, runMigrations(db))

	repo := NewRepository(dbPool)

	ctx := context.Background()
	userID := uuid.New()
	email := "test@example.com"

	require.NoError(t, insertTestUserRepo(ctx, db, userID, email))

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

	tx, err := repo.BeginTx(ctx)
	require.NoError(t, err)
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	err = repo.MarkTokenUsedWithTx(ctx, tx, token.ID)
	assert.NoError(t, err)

	err = tx.Commit(ctx)
	assert.NoError(t, err)

	found, err := repo.FindTokenByHash(ctx, token.TokenHash)
	assert.NoError(t, err)
	assert.True(t, found.Used)
}

func TestRepository_SoftDeleteUserInvalidatesOutstandingTokens(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dbPool, cleanup := testutil.NewPostgres(t)
	defer cleanup()

	db, err := sql.Open("pgx", dbPool.Config().ConnString())
	require.NoError(t, err)
	defer db.Close()

	require.NoError(t, runMigrations(db))

	repo := NewRepository(dbPool)
	ctx := context.Background()
	userID := uuid.New()
	require.NoError(t, insertTestUserRepo(ctx, db, userID, "soft-delete@example.com"))

	oldKey := "avatars/" + userID.String() + "/old.png"
	const oldSize int64 = 321
	_, err = db.ExecContext(ctx, `UPDATE users SET avatar_key = $2, avatar_size_bytes = $3 WHERE id = $1`, userID, oldKey, oldSize)
	require.NoError(t, err)

	for i, purpose := range []string{"email_verify", "password_reset", "email_change"} {
		_, err = db.ExecContext(ctx, `
			INSERT INTO verify_tokens (id, user_id, purpose, token_hash, expires_at)
			VALUES ($1, $2, $3, $4, now() + interval '1 hour')
		`, uuid.New(), userID, purpose, []byte{byte(i + 1)})
		require.NoError(t, err)
	}

	change, err := repo.SoftDeleteUser(ctx, userID.String())
	require.NoError(t, err)
	require.Equal(t, oldKey, change.OldKey)

	var deletedAt sql.NullTime
	var avatarKey sql.NullString
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT deleted_at, avatar_key FROM users WHERE id = $1`, userID,
	).Scan(&deletedAt, &avatarKey))
	require.True(t, deletedAt.Valid)
	require.False(t, avatarKey.Valid)

	var activeTokens int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT count(*) FROM verify_tokens WHERE user_id = $1 AND used_at IS NULL`, userID,
	).Scan(&activeTokens))
	require.Zero(t, activeTokens)

	var cleanupKey string
	var cleanupSize sql.NullInt64
	var completedAt sql.NullTime
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT object_key, object_size_bytes, completed_at
		FROM avatar_cleanup_jobs
		WHERE object_key = $1
	`, oldKey).Scan(&cleanupKey, &cleanupSize, &completedAt))
	require.Equal(t, oldKey, cleanupKey)
	require.True(t, cleanupSize.Valid)
	require.Equal(t, oldSize, cleanupSize.Int64)
	require.False(t, completedAt.Valid)
}

func TestRepository_CreateToken_RejectsSoftDeletedUser(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dbPool, cleanup := testutil.NewPostgres(t)
	defer cleanup()

	db, err := sql.Open("pgx", dbPool.Config().ConnString())
	require.NoError(t, err)
	defer db.Close()

	require.NoError(t, runMigrations(db))

	repo := NewRepository(dbPool)
	ctx := context.Background()
	userID := uuid.New()
	require.NoError(t, insertTestUserRepo(ctx, db, userID, "deleted-token-owner@example.com"))

	_, err = db.ExecContext(ctx, `UPDATE users SET deleted_at = now() WHERE id = $1`, userID)
	require.NoError(t, err)

	token := &Token{
		ID:        uuid.New().String(),
		UserID:    userID,
		Type:      TokenTypeEmailVerify,
		TokenHash: []byte("deleted-user-token-hash"),
		Payload:   `{"email":"deleted-token-owner@example.com"}`,
		ExpiresAt: time.Now().Add(time.Hour),
		CreatedAt: time.Now(),
	}

	err = repo.CreateToken(ctx, token)
	require.ErrorIs(t, err, ErrUserNotFound)

	var count int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM verify_tokens WHERE id = $1`, token.ID).Scan(&count))
	assert.Zero(t, count)
}

func TestRepository_CreateToken_SerializesWithUserDeletion(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dbPool, cleanup := testutil.NewPostgres(t)
	defer cleanup()

	db, err := sql.Open("pgx", dbPool.Config().ConnString())
	require.NoError(t, err)
	defer db.Close()

	require.NoError(t, runMigrations(db))

	repo := NewRepository(dbPool)
	ctx := context.Background()
	userID := uuid.New()
	require.NoError(t, insertTestUserRepo(ctx, db, userID, "token-race@example.com"))

	deleteTx, err := dbPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = deleteTx.Rollback(ctx) }()

	_, err = deleteTx.Exec(ctx, `UPDATE users SET deleted_at = now() WHERE id = $1`, userID)
	require.NoError(t, err)

	token := &Token{
		ID:        uuid.New().String(),
		UserID:    userID,
		Type:      TokenTypeEmailVerify,
		TokenHash: []byte("token-race-hash"),
		Payload:   `{"email":"token-race@example.com"}`,
		ExpiresAt: time.Now().Add(time.Hour),
		CreatedAt: time.Now(),
	}

	blockedCtx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancel()
	err = repo.CreateToken(blockedCtx, token)
	require.Error(t, err)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	require.NoError(t, deleteTx.Commit(ctx))

	err = repo.CreateToken(ctx, token)
	require.ErrorIs(t, err, ErrUserNotFound)
}

func TestRepository_FindByIDWithTx_SerializesSoftDelete(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dbPool, cleanup := testutil.NewPostgres(t)
	defer cleanup()

	db, err := sql.Open("pgx", dbPool.Config().ConnString())
	require.NoError(t, err)
	defer db.Close()

	require.NoError(t, runMigrations(db))

	repo := NewRepository(dbPool)
	ctx := context.Background()
	userID := uuid.New()
	require.NoError(t, insertTestUserRepo(ctx, db, userID, "confirm-delete-race@example.com"))

	confirmTx, err := repo.BeginTx(ctx)
	require.NoError(t, err)
	defer func() { _ = confirmTx.Rollback(ctx) }()

	_, err = repo.FindByIDWithTx(ctx, confirmTx, userID)
	require.NoError(t, err)

	blockedCtx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancel()
	_, err = repo.SoftDeleteUser(blockedCtx, userID.String())
	require.Error(t, err)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	require.NoError(t, confirmTx.Commit(ctx))

	_, err = repo.SoftDeleteUser(ctx, userID.String())
	require.NoError(t, err)
}

func TestRepository_MarkTokenUsedWithTx_RejectsAlreadyUsedToken(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dbPool, cleanup := testutil.NewPostgres(t)
	defer cleanup()

	db, err := sql.Open("pgx", dbPool.Config().ConnString())
	require.NoError(t, err)
	defer db.Close()

	require.NoError(t, runMigrations(db))

	repo := NewRepository(dbPool)
	ctx := context.Background()
	userID := uuid.New()
	require.NoError(t, insertTestUserRepo(ctx, db, userID, "used-token@example.com"))

	token := &Token{
		ID:        uuid.New().String(),
		UserID:    userID,
		Type:      TokenTypeEmailChange,
		TokenHash: []byte("used-token-hash"),
		Payload:   `{"new_email":"new@example.com","old_email":"used-token@example.com"}`,
		ExpiresAt: time.Now().Add(time.Hour),
		CreatedAt: time.Now(),
	}
	require.NoError(t, repo.CreateToken(ctx, token))

	firstTx, err := repo.BeginTx(ctx)
	require.NoError(t, err)
	require.NoError(t, repo.MarkTokenUsedWithTx(ctx, firstTx, token.ID))
	require.NoError(t, firstTx.Commit(ctx))

	secondTx, err := repo.BeginTx(ctx)
	require.NoError(t, err)
	defer func() { _ = secondTx.Rollback(ctx) }()

	err = repo.MarkTokenUsedWithTx(ctx, secondTx, token.ID)
	require.ErrorIs(t, err, ErrTokenAlreadyUsed)
}
