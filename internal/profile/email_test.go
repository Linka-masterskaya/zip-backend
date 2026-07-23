// internal/profile/email_test.go
package profile

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Linka-masterskaya/zip-backend/internal/cryptox"
	"github.com/Linka-masterskaya/zip-backend/internal/mailer"
	"github.com/Linka-masterskaya/zip-backend/internal/testutil"
)

// ============ Test Helpers ============

// runMigrations creates all necessary tables for tests.
func runMigrations(db *sql.DB) error {
	ctx := context.Background()

	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS users (
			id UUID PRIMARY KEY,
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

	_, err = db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS auth_cred (
			user_id UUID PRIMARY KEY REFERENCES users(id),
			email_hash BYTEA NOT NULL,
			email_encrypted BYTEA NOT NULL,
			password_hash TEXT,
			role TEXT NOT NULL DEFAULT 'user',
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`)
	if err != nil {
		return err
	}

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

	_, err = db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS verify_tokens_token_hash_idx ON verify_tokens (token_hash)
	`)
	return err
}

// newTestCrypto creates a real cryptox instance for tests.
func newTestCrypto(t *testing.T) *cryptox.Cryptox {
	t.Helper()
	aesKey := make([]byte, 32)
	hmacKey := make([]byte, 32)
	for i := range aesKey {
		aesKey[i] = byte(i)
		hmacKey[i] = byte(i + 32)
	}
	crypto, err := cryptox.New(aesKey, hmacKey)
	require.NoError(t, err)
	return crypto
}

// insertTempUser inserts a test user into users and auth_cred tables.
func insertTempUser(ctx context.Context, db *sql.DB, id uuid.UUID, email string, crypto *cryptox.Cryptox) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO users (id, email_verified, display_name, created_at, updated_at)
		VALUES ($1, $2, $3, now(), now())
	`, id, true, "Test User")
	if err != nil {
		return err
	}

	emailEncrypted, err := crypto.Encrypt([]byte(email))
	if err != nil {
		return err
	}

	emailHash := crypto.Hash([]byte(email))
	_, err = db.ExecContext(ctx, `
		INSERT INTO auth_cred (user_id, email_hash, email_encrypted, role)
		VALUES ($1, $2, $3, 'user')
	`, id, emailHash, emailEncrypted)
	if err != nil {
		return err
	}

	orgID := uuid.New()
	_, err = db.ExecContext(ctx, `
		INSERT INTO organizations (id, storage_used_bytes, storage_quota_bytes)
		VALUES ($1, 0, 10737418240)
	`, orgID)
	if err != nil {
		return err
	}

	_, err = db.ExecContext(ctx, `
		UPDATE users SET org_id = $1 WHERE id = $2
	`, orgID, id)
	return err
}

// countTokens counts email_change tokens for a user.
func countTokens(ctx context.Context, db *sql.DB, userID uuid.UUID) (int, error) {
	var count int
	err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM verify_tokens
		WHERE user_id = $1 AND purpose = 'email_change'
	`, userID.String()).Scan(&count)
	return count, err
}

// getTokenByID returns a token by ID.
func getTokenByID(ctx context.Context, db *sql.DB, id string) (*Token, error) {
	var token Token
	var payload []byte
	var purpose string
	var usedAt sql.NullTime
	var userIDStr string

	err := db.QueryRowContext(ctx, `
		SELECT id, user_id, purpose, payload, expires_at, used_at, created_at
		FROM verify_tokens
		WHERE id = $1
	`, id).Scan(
		&token.ID,
		&userIDStr,
		&purpose,
		&payload,
		&token.ExpiresAt,
		&usedAt,
		&token.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrTokenNotFound
		}
		return nil, err
	}

	parsedUserID, err := uuid.Parse(userIDStr)
	if err != nil {
		return nil, err
	}

	token.UserID = parsedUserID
	token.Type = TokenType(purpose)
	token.Payload = string(payload)
	token.Used = usedAt.Valid
	token.Token = ""

	return &token, nil
}

// getPayloadFromToken returns payload from token.
func getPayloadFromToken(token *Token) (*EmailChangePayload, error) {
	var payload EmailChangePayload
	if err := json.Unmarshal([]byte(token.Payload), &payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

// ============ Test Mocks ============

type testMailer struct{}

func (m *testMailer) Send(ctx context.Context, to string, tmpl mailer.Template, data mailer.EmailData) error {
	return nil
}

type testStorage struct{}

func (s *testStorage) PutObject(ctx context.Context, key string, reader io.Reader, size int64, contentType string) error {
	return nil
}

func (s *testStorage) RemoveObject(ctx context.Context, key string) error {
	return nil
}

func (s *testStorage) ObjectSize(ctx context.Context, key string) (int64, error) {
	return 0, nil
}

func (s *testStorage) PresignedURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	return "https://storage.test/" + key, nil
}

type fakeRevoker struct {
	revokeErr    error
	revokedID    string
	revokeCalled bool
}

func (f *fakeRevoker) RevokeAllSessions(ctx context.Context, userID string) error {
	f.revokeCalled = true
	f.revokedID = userID
	return f.revokeErr
}

// ============ Integration Tests ============

// TestGenerateEmailChangeToken_Success tests token generation without email.
func TestGenerateEmailChangeToken_Success(t *testing.T) {
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
	mailer := &testMailer{}
	storage := &testStorage{}
	crypto := newTestCrypto(t)
	emailCfg := EmailConfig{
		EmailChangeTTL: 24 * time.Hour,
		EmailVerifyTTL: 24 * time.Hour,
	}
	sessions := &fakeRevoker{}
	service := NewService(repo, storage, mailer, crypto, sessions, emailCfg)

	ctx := context.Background()
	userID := uuid.New()
	oldEmail := "old@example.com"
	newEmail := "new@example.com"

	require.NoError(t, insertTempUser(ctx, db, userID, oldEmail, crypto))

	token, err := service.GenerateEmailChangeToken(ctx, userID, newEmail)
	require.NoError(t, err)
	require.NotNil(t, token)
	require.NotEmpty(t, token.Token)
	require.Equal(t, TokenTypeEmailChange, token.Type)
	require.False(t, token.Used)

	count, err := countTokens(ctx, db, userID)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	payload, err := getPayloadFromToken(token)
	require.NoError(t, err)
	assert.Equal(t, newEmail, payload.NewEmail)
	assert.Equal(t, oldEmail, payload.OldEmail)
}

// TestSendEmailChangeConfirmation_Success tests sending confirmation email.
func TestSendEmailChangeConfirmation_Success(t *testing.T) {
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
	mailer := &testMailer{}
	storage := &testStorage{}
	crypto := newTestCrypto(t)
	emailCfg := EmailConfig{
		EmailChangeTTL: 24 * time.Hour,
		EmailVerifyTTL: 24 * time.Hour,
	}
	sessions := &fakeRevoker{}
	service := NewService(repo, storage, mailer, crypto, sessions, emailCfg)

	ctx := context.Background()
	userID := uuid.New()
	oldEmail := "old@example.com"
	newEmail := "new@example.com"

	require.NoError(t, insertTempUser(ctx, db, userID, oldEmail, crypto))

	token, err := service.GenerateEmailChangeToken(ctx, userID, newEmail)
	require.NoError(t, err)

	err = service.SendEmailChangeConfirmation(ctx, userID, token)
	assert.NoError(t, err)
}

// TestEmailChangeFlow_Integration tests the complete email change flow.
func TestEmailChangeFlow_Integration(t *testing.T) {
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
	mailer := &testMailer{}
	storage := &testStorage{}
	crypto := newTestCrypto(t)
	emailCfg := EmailConfig{
		EmailChangeTTL: 24 * time.Hour,
		EmailVerifyTTL: 24 * time.Hour,
	}
	sessions := &fakeRevoker{}
	service := NewService(repo, storage, mailer, crypto, sessions, emailCfg)

	ctx := context.Background()
	userID := uuid.New()
	oldEmail := "old@example.com"
	newEmail := "new@example.com"

	require.NoError(t, insertTempUser(ctx, db, userID, oldEmail, crypto))

	token, err := service.GenerateEmailChangeToken(ctx, userID, newEmail)
	require.NoError(t, err)
	require.NotEmpty(t, token.Token)

	err = service.SendEmailChangeConfirmation(ctx, userID, token)
	require.NoError(t, err)

	err = service.ConfirmEmailChange(ctx, token.Token)
	require.NoError(t, err)

	user, err := repo.FindByID(ctx, userID)
	require.NoError(t, err)
	assert.NotEmpty(t, user.EmailEncrypted)
	assert.False(t, user.EmailVerified)

	tokenAfter, err := getTokenByID(ctx, db, token.ID)
	require.NoError(t, err)
	assert.True(t, tokenAfter.Used)

	var verifyCount int
	err = db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM verify_tokens
		WHERE user_id = $1 AND purpose = 'email_verify'
	`, userID.String()).Scan(&verifyCount)
	require.NoError(t, err)
	assert.Equal(t, 1, verifyCount)
}

// TestConfirmEmailChange_Integration_Success tests confirm email change directly.
func TestConfirmEmailChange_Integration_Success(t *testing.T) {
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
	mailer := &testMailer{}
	storage := &testStorage{}
	crypto := newTestCrypto(t)
	emailCfg := EmailConfig{
		EmailChangeTTL: 24 * time.Hour,
		EmailVerifyTTL: 24 * time.Hour,
	}
	sessions := &fakeRevoker{}
	service := NewService(repo, storage, mailer, crypto, sessions, emailCfg)

	ctx := context.Background()
	userID := uuid.New()
	oldEmail := "old@example.com"
	newEmail := "new@example.com"

	require.NoError(t, insertTempUser(ctx, db, userID, oldEmail, crypto))

	token, err := service.GenerateEmailChangeToken(ctx, userID, newEmail)
	require.NoError(t, err)

	err = service.ConfirmEmailChange(ctx, token.Token)
	require.NoError(t, err)

	user, err := repo.FindByID(ctx, userID)
	require.NoError(t, err)
	assert.NotEmpty(t, user.EmailEncrypted)
	assert.False(t, user.EmailVerified)

	tokenAfter, err := getTokenByID(ctx, db, token.ID)
	require.NoError(t, err)
	assert.True(t, tokenAfter.Used)
}

// TestEmailChangeFlow_Integration_EmailAlreadyTaken tests conflict scenario.
func TestEmailChangeFlow_Integration_EmailAlreadyTaken(t *testing.T) {
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
	mailer := &testMailer{}
	storage := &testStorage{}
	crypto := newTestCrypto(t)
	emailCfg := EmailConfig{
		EmailChangeTTL: 24 * time.Hour,
		EmailVerifyTTL: 24 * time.Hour,
	}
	sessions := &fakeRevoker{}
	service := NewService(repo, storage, mailer, crypto, sessions, emailCfg)

	ctx := context.Background()
	userID1 := uuid.New()
	userID2 := uuid.New()
	oldEmail := "old@example.com"
	takenEmail := "taken@example.com"

	require.NoError(t, insertTempUser(ctx, db, userID1, oldEmail, crypto))
	require.NoError(t, insertTempUser(ctx, db, userID2, takenEmail, crypto))

	_, err = service.GenerateEmailChangeToken(ctx, userID1, takenEmail)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrEmailAlreadyUsed)
}

// TestEmailChangeFlow_Integration_SameEmail tests trying to change to same email.
func TestEmailChangeFlow_Integration_SameEmail(t *testing.T) {
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
	mailer := &testMailer{}
	storage := &testStorage{}
	crypto := newTestCrypto(t)
	emailCfg := EmailConfig{
		EmailChangeTTL: 24 * time.Hour,
		EmailVerifyTTL: 24 * time.Hour,
	}
	sessions := &fakeRevoker{}
	service := NewService(repo, storage, mailer, crypto, sessions, emailCfg)

	ctx := context.Background()
	userID := uuid.New()
	email := "test@example.com"

	require.NoError(t, insertTempUser(ctx, db, userID, email, crypto))

	_, err = service.GenerateEmailChangeToken(ctx, userID, email)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrEmailSameAsCurrent)
}

// TestConfirmEmailChange_Integration_TokenExpired tests expired token scenario.
func TestConfirmEmailChange_Integration_TokenExpired(t *testing.T) {
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
	mailer := &testMailer{}
	storage := &testStorage{}
	crypto := newTestCrypto(t)
	emailCfg := EmailConfig{
		EmailChangeTTL: -1 * time.Hour,
		EmailVerifyTTL: 24 * time.Hour,
	}
	sessions := &fakeRevoker{}
	service := NewService(repo, storage, mailer, crypto, sessions, emailCfg)

	ctx := context.Background()
	userID := uuid.New()
	oldEmail := "old@example.com"
	newEmail := "new@example.com"

	require.NoError(t, insertTempUser(ctx, db, userID, oldEmail, crypto))

	token, err := service.GenerateEmailChangeToken(ctx, userID, newEmail)
	require.NoError(t, err)

	err = service.ConfirmEmailChange(ctx, token.Token)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrTokenExpired)

	user, err := repo.FindByID(ctx, userID)
	require.NoError(t, err)
	assert.NotEmpty(t, user.EmailEncrypted)
}

// TestConfirmEmailChange_Integration_TokenAlreadyUsed tests already used token scenario.
func TestConfirmEmailChange_Integration_TokenAlreadyUsed(t *testing.T) {
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
	mailer := &testMailer{}
	storage := &testStorage{}
	crypto := newTestCrypto(t)
	emailCfg := EmailConfig{
		EmailChangeTTL: 24 * time.Hour,
		EmailVerifyTTL: 24 * time.Hour,
	}
	sessions := &fakeRevoker{}
	service := NewService(repo, storage, mailer, crypto, sessions, emailCfg)

	ctx := context.Background()
	userID := uuid.New()
	oldEmail := "old@example.com"
	newEmail := "new@example.com"

	require.NoError(t, insertTempUser(ctx, db, userID, oldEmail, crypto))

	token, err := service.GenerateEmailChangeToken(ctx, userID, newEmail)
	require.NoError(t, err)

	err = service.ConfirmEmailChange(ctx, token.Token)
	require.NoError(t, err)

	err = service.ConfirmEmailChange(ctx, token.Token)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrTokenAlreadyUsed)
}

// TestDeleteExpiredTokens_Integration tests expired tokens cleanup.
func TestDeleteExpiredTokens_Integration(t *testing.T) {
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
	mailer := &testMailer{}
	storage := &testStorage{}
	crypto := newTestCrypto(t)
	emailCfg := EmailConfig{
		EmailChangeTTL: -1 * time.Hour,
		EmailVerifyTTL: 24 * time.Hour,
	}
	sessions := &fakeRevoker{}
	service := NewService(repo, storage, mailer, crypto, sessions, emailCfg)

	ctx := context.Background()
	userID := uuid.New()
	oldEmail := "old@example.com"
	newEmail := "new@example.com"

	require.NoError(t, insertTempUser(ctx, db, userID, oldEmail, crypto))

	for i := 0; i < 3; i++ {
		_, err := service.GenerateEmailChangeToken(ctx, userID, newEmail)
		require.NoError(t, err)
	}

	count, err := countTokens(ctx, db, userID)
	require.NoError(t, err)
	assert.Equal(t, 3, count)

	deleted, err := service.DeleteExpiredTokens(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(3), deleted)

	count, err = countTokens(ctx, db, userID)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

// TestEmailChangeFlow_Integration_InvalidEmail tests invalid email format.
func TestEmailChangeFlow_Integration_InvalidEmail(t *testing.T) {
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
	mailer := &testMailer{}
	storage := &testStorage{}
	crypto := newTestCrypto(t)
	emailCfg := EmailConfig{
		EmailChangeTTL: 24 * time.Hour,
		EmailVerifyTTL: 24 * time.Hour,
	}
	sessions := &fakeRevoker{}
	service := NewService(repo, storage, mailer, crypto, sessions, emailCfg)

	ctx := context.Background()
	userID := uuid.New()
	oldEmail := "old@example.com"
	invalidEmail := "invalid-email"

	require.NoError(t, insertTempUser(ctx, db, userID, oldEmail, crypto))

	_, err = service.GenerateEmailChangeToken(ctx, userID, invalidEmail)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrEmailInvalid)
}

// TestEmailChangeFlow_Integration_UserNotFound tests user not found scenario.
func TestEmailChangeFlow_Integration_UserNotFound(t *testing.T) {
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
	mailer := &testMailer{}
	storage := &testStorage{}
	crypto := newTestCrypto(t)
	emailCfg := EmailConfig{
		EmailChangeTTL: 24 * time.Hour,
		EmailVerifyTTL: 24 * time.Hour,
	}
	sessions := &fakeRevoker{}
	service := NewService(repo, storage, mailer, crypto, sessions, emailCfg)

	ctx := context.Background()
	userID := uuid.New()
	newEmail := "new@example.com"

	_, err = service.GenerateEmailChangeToken(ctx, userID, newEmail)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "user not found")
}
