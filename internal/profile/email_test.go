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
)

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

// ============ Test Helpers ============

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
		VALUES ($1, $2, $3, 'defectologist')
	`, id, emailHash, emailEncrypted)
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

func countTokens(ctx context.Context, db *sql.DB, userID uuid.UUID) (int, error) {
	var count int
	err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM verify_tokens
		WHERE user_id = $1 AND purpose = 'email_change'
	`, userID.String()).Scan(&count)
	return count, err
}

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

func getPayloadFromToken(token *Token) (*EmailChangePayload, error) {
	var payload EmailChangePayload
	if err := json.Unmarshal([]byte(token.Payload), &payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

func setupTestData(ctx context.Context, t *testing.T, userID uuid.UUID, email string) func() {
	db := getTestDB()
	crypto := getTestCrypto()

	err := insertTempUser(ctx, db, userID, email, crypto)
	require.NoError(t, err)

	return func() {
		_, _ = db.ExecContext(ctx, "DELETE FROM verify_tokens WHERE user_id = $1", userID.String())
		_, _ = db.ExecContext(ctx, "DELETE FROM auth_cred WHERE user_id = $1", userID.String())
		_, _ = db.ExecContext(ctx, "DELETE FROM users WHERE id = $1", userID.String())
	}
}

func (f *fakeRevoker) DisableUserSessions(ctx context.Context, userID string) error {
	return f.RevokeAllSessions(ctx, userID)
}

func (f *fakeRevoker) EnableUserSessions(_ context.Context, _ string) error {
	return nil
}

// ============ Integration Tests ============

func TestGenerateEmailChangeToken_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	db := getTestDB()
	ctx := getTestContext()

	repo := NewRepository(getTestPool())
	mailer := &testMailer{}
	storage := &testStorage{}
	crypto := getTestCrypto()
	emailCfg := EmailConfig{
		EmailChangeTTL: 24 * time.Hour,
		EmailVerifyTTL: 24 * time.Hour,
	}
	sessions := &fakeRevoker{}
	service := NewService(repo, storage, mailer, crypto, sessions, emailCfg)

	userID := uuid.New()
	oldEmail := "old@example.com"
	newEmail := "new@example.com"

	cleanup := setupTestData(ctx, t, userID, oldEmail)
	defer cleanup()

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

func TestSendEmailChangeConfirmation_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := getTestContext()

	repo := NewRepository(getTestPool())
	mailer := &testMailer{}
	storage := &testStorage{}
	crypto := getTestCrypto()
	emailCfg := EmailConfig{
		EmailChangeTTL: 24 * time.Hour,
		EmailVerifyTTL: 24 * time.Hour,
	}
	sessions := &fakeRevoker{}
	service := NewService(repo, storage, mailer, crypto, sessions, emailCfg)

	userID := uuid.New()
	oldEmail := "old@example.com"
	newEmail := "new@example.com"

	cleanup := setupTestData(ctx, t, userID, oldEmail)
	defer cleanup()

	token, err := service.GenerateEmailChangeToken(ctx, userID, newEmail)
	require.NoError(t, err)

	err = service.SendEmailChangeConfirmation(ctx, userID, token)
	assert.NoError(t, err)
}

func TestEmailChangeFlow_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	db := getTestDB()
	ctx := getTestContext()

	repo := NewRepository(getTestPool())
	mailer := &testMailer{}
	storage := &testStorage{}
	crypto := getTestCrypto()
	emailCfg := EmailConfig{
		EmailChangeTTL: 24 * time.Hour,
		EmailVerifyTTL: 24 * time.Hour,
	}
	sessions := &fakeRevoker{}
	service := NewService(repo, storage, mailer, crypto, sessions, emailCfg)

	userID := uuid.New()
	oldEmail := "old@example.com"
	newEmail := "new@example.com"

	cleanup := setupTestData(ctx, t, userID, oldEmail)
	defer cleanup()

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

func TestConfirmEmailChange_Integration_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := getTestContext()

	repo := NewRepository(getTestPool())
	mailer := &testMailer{}
	storage := &testStorage{}
	crypto := getTestCrypto()
	emailCfg := EmailConfig{
		EmailChangeTTL: 24 * time.Hour,
		EmailVerifyTTL: 24 * time.Hour,
	}
	sessions := &fakeRevoker{}
	service := NewService(repo, storage, mailer, crypto, sessions, emailCfg)

	userID := uuid.New()
	oldEmail := "old@example.com"
	newEmail := "new@example.com"

	cleanup := setupTestData(ctx, t, userID, oldEmail)
	defer cleanup()

	token, err := service.GenerateEmailChangeToken(ctx, userID, newEmail)
	require.NoError(t, err)

	err = service.ConfirmEmailChange(ctx, token.Token)
	require.NoError(t, err)

	user, err := repo.FindByID(ctx, userID)
	require.NoError(t, err)
	assert.NotEmpty(t, user.EmailEncrypted)
	assert.False(t, user.EmailVerified)

	tokenAfter, err := getTokenByID(ctx, getTestDB(), token.ID)
	require.NoError(t, err)
	assert.True(t, tokenAfter.Used)
}

func TestEmailChangeFlow_Integration_EmailAlreadyTaken(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := getTestContext()

	repo := NewRepository(getTestPool())
	mailer := &testMailer{}
	storage := &testStorage{}
	crypto := getTestCrypto()
	emailCfg := EmailConfig{
		EmailChangeTTL: 24 * time.Hour,
		EmailVerifyTTL: 24 * time.Hour,
	}
	sessions := &fakeRevoker{}
	service := NewService(repo, storage, mailer, crypto, sessions, emailCfg)

	userID1 := uuid.New()
	userID2 := uuid.New()
	oldEmail := "old@example.com"
	takenEmail := "taken@example.com"

	cleanup1 := setupTestData(ctx, t, userID1, oldEmail)
	defer cleanup1()

	cleanup2 := setupTestData(ctx, t, userID2, takenEmail)
	defer cleanup2()

	_, err := service.GenerateEmailChangeToken(ctx, userID1, takenEmail)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrEmailAlreadyUsed)
}

// internal/profile/email_test.go
// Добавить в конец файла после всех существующих тестов

func TestEmailChangeFlow_Integration_SoftDeletedEmailReserved(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	db := getTestDB()
	pool := getTestPool()
	ctx := getTestContext()
	crypto := getTestCrypto()

	repo := NewRepository(pool)
	mailer := &testMailer{}
	storage := &testStorage{}
	emailCfg := EmailConfig{
		EmailChangeTTL: 24 * time.Hour,
		EmailVerifyTTL: 24 * time.Hour,
	}
	sessions := &fakeRevoker{}
	service := NewService(repo, storage, mailer, crypto, sessions, emailCfg)

	userID := uuid.New()
	deletedUserID := uuid.New()
	oldEmail := "owner@example.com"
	reservedEmail := "deleted@example.com"

	// Создаем пользователей
	cleanup1 := setupTestData(ctx, t, userID, oldEmail)
	defer cleanup1()

	cleanup2 := setupTestData(ctx, t, deletedUserID, reservedEmail)
	defer cleanup2()

	// Помечаем второго пользователя как удаленного
	_, err := db.ExecContext(ctx, `UPDATE users SET deleted_at = now() WHERE id = $1`, deletedUserID)
	require.NoError(t, err)

	// Пытаемся сменить email на email удаленного пользователя
	_, err = service.GenerateEmailChangeToken(ctx, userID, reservedEmail)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEmailAlreadyUsed)

	// Проверяем, что токен не был создан
	count, err := countTokens(ctx, db, userID)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestConfirmEmailChange_Integration_SoftDeletedEmailBecomesReserved(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	db := getTestDB()
	pool := getTestPool()
	ctx := getTestContext()
	crypto := getTestCrypto()

	repo := NewRepository(pool)
	mailer := &testMailer{}
	storage := &testStorage{}
	emailCfg := EmailConfig{
		EmailChangeTTL: 24 * time.Hour,
		EmailVerifyTTL: 24 * time.Hour,
	}
	sessions := &fakeRevoker{}
	service := NewService(repo, storage, mailer, crypto, sessions, emailCfg)

	userID := uuid.New()
	oldEmail := "owner@example.com"
	newEmail := "later-reserved@example.com"

	// Создаем пользователя
	cleanup1 := setupTestData(ctx, t, userID, oldEmail)
	defer cleanup1()

	// Генерируем токен для смены email
	token, err := service.GenerateEmailChangeToken(ctx, userID, newEmail)
	require.NoError(t, err)

	// Создаем второго пользователя с этим email
	deletedUserID := uuid.New()
	cleanup2 := setupTestData(ctx, t, deletedUserID, newEmail)
	defer cleanup2()

	// Помечаем второго пользователя как удаленного
	_, err = db.ExecContext(ctx, `UPDATE users SET deleted_at = now() WHERE id = $1`, deletedUserID)
	require.NoError(t, err)

	// Пытаемся подтвердить смену email (теперь email занят удаленным пользователем)
	err = service.ConfirmEmailChange(ctx, token.Token)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEmailAlreadyUsed)

	// Проверяем, что токен не был использован
	storedToken, err := getTokenByID(ctx, db, token.ID)
	require.NoError(t, err)
	assert.False(t, storedToken.Used)
}

// TestEmailChangeFlow_Integration_SameEmail tests trying to change to same email.
func TestEmailChangeFlow_Integration_SameEmail(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := getTestContext()

	repo := NewRepository(getTestPool())
	mailer := &testMailer{}
	storage := &testStorage{}
	crypto := getTestCrypto()
	emailCfg := EmailConfig{
		EmailChangeTTL: 24 * time.Hour,
		EmailVerifyTTL: 24 * time.Hour,
	}
	sessions := &fakeRevoker{}
	service := NewService(repo, storage, mailer, crypto, sessions, emailCfg)

	userID := uuid.New()
	email := "test@example.com"

	cleanup := setupTestData(ctx, t, userID, email)
	defer cleanup()

	_, err := service.GenerateEmailChangeToken(ctx, userID, email)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrEmailSameAsCurrent)
}

func TestConfirmEmailChange_Integration_TokenExpired(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := getTestContext()

	repo := NewRepository(getTestPool())
	mailer := &testMailer{}
	storage := &testStorage{}
	crypto := getTestCrypto()
	emailCfg := EmailConfig{
		EmailChangeTTL: -1 * time.Hour,
		EmailVerifyTTL: 24 * time.Hour,
	}
	sessions := &fakeRevoker{}
	service := NewService(repo, storage, mailer, crypto, sessions, emailCfg)

	userID := uuid.New()
	oldEmail := "old@example.com"
	newEmail := "new@example.com"

	cleanup := setupTestData(ctx, t, userID, oldEmail)
	defer cleanup()

	token, err := service.GenerateEmailChangeToken(ctx, userID, newEmail)
	require.NoError(t, err)

	err = service.ConfirmEmailChange(ctx, token.Token)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrTokenExpired)

	user, err := repo.FindByID(ctx, userID)
	require.NoError(t, err)
	assert.NotEmpty(t, user.EmailEncrypted)
}

func TestConfirmEmailChange_Integration_TokenAlreadyUsed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := getTestContext()

	repo := NewRepository(getTestPool())
	mailer := &testMailer{}
	storage := &testStorage{}
	crypto := getTestCrypto()
	emailCfg := EmailConfig{
		EmailChangeTTL: 24 * time.Hour,
		EmailVerifyTTL: 24 * time.Hour,
	}
	sessions := &fakeRevoker{}
	service := NewService(repo, storage, mailer, crypto, sessions, emailCfg)

	userID := uuid.New()
	oldEmail := "old@example.com"
	newEmail := "new@example.com"

	cleanup := setupTestData(ctx, t, userID, oldEmail)
	defer cleanup()

	token, err := service.GenerateEmailChangeToken(ctx, userID, newEmail)
	require.NoError(t, err)

	err = service.ConfirmEmailChange(ctx, token.Token)
	require.NoError(t, err)

	err = service.ConfirmEmailChange(ctx, token.Token)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrTokenAlreadyUsed)
}

func TestDeleteExpiredTokens_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := getTestContext()
	db := getTestDB()

	repo := NewRepository(getTestPool())
	mailer := &testMailer{}
	storage := &testStorage{}
	crypto := getTestCrypto()
	emailCfg := EmailConfig{
		EmailChangeTTL: -1 * time.Hour,
		EmailVerifyTTL: 24 * time.Hour,
	}
	sessions := &fakeRevoker{}
	service := NewService(repo, storage, mailer, crypto, sessions, emailCfg)

	userID := uuid.New()
	oldEmail := "old@example.com"
	newEmail := "new@example.com"

	cleanup := setupTestData(ctx, t, userID, oldEmail)
	defer cleanup()

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

func TestEmailChangeFlow_Integration_InvalidEmail(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := getTestContext()

	repo := NewRepository(getTestPool())
	mailer := &testMailer{}
	storage := &testStorage{}
	crypto := getTestCrypto()
	emailCfg := EmailConfig{
		EmailChangeTTL: 24 * time.Hour,
		EmailVerifyTTL: 24 * time.Hour,
	}
	sessions := &fakeRevoker{}
	service := NewService(repo, storage, mailer, crypto, sessions, emailCfg)

	userID := uuid.New()
	oldEmail := "old@example.com"
	invalidEmail := "invalid-email"

	cleanup := setupTestData(ctx, t, userID, oldEmail)
	defer cleanup()

	_, err := service.GenerateEmailChangeToken(ctx, userID, invalidEmail)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrEmailInvalid)
}

func TestEmailChangeFlow_Integration_UserNotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := getTestContext()

	repo := NewRepository(getTestPool())
	mailer := &testMailer{}
	storage := &testStorage{}
	crypto := getTestCrypto()
	emailCfg := EmailConfig{
		EmailChangeTTL: 24 * time.Hour,
		EmailVerifyTTL: 24 * time.Hour,
	}
	sessions := &fakeRevoker{}
	service := NewService(repo, storage, mailer, crypto, sessions, emailCfg)

	userID := uuid.New()
	newEmail := "new@example.com"

	_, err := service.GenerateEmailChangeToken(ctx, userID, newEmail)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "user not found")
}
