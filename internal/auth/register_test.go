package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/mailer"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// registerMailerFake records verify-email send attempts without using SMTP.
type registerMailerFake struct {
	calls    int
	to       string
	template mailer.Template
	data     mailer.EmailData
	err      error
}

func (f *registerMailerFake) Send(
	_ context.Context,
	to string,
	template mailer.Template,
	data mailer.EmailData,
) error {
	f.calls++
	f.to = to
	f.template = template
	f.data = data

	return f.err
}

type registerCryptoFake struct{}

func (registerCryptoFake) Hash(data []byte) []byte {
	hash := sha256.Sum256(data)
	return hash[:]
}

func (registerCryptoFake) Encrypt(plaintext []byte) ([]byte, error) {
	return append([]byte("encrypted:"), plaintext...), nil
}

func (registerCryptoFake) Decrypt(ciphertext []byte) ([]byte, error) {
	return ciphertext, nil
}

// registerCounts stores row counts in tables touched by registration.
type registerCounts struct {
	users         int
	organizations int
	authCred      int
	verifyTokens  int
}

func newRegisterTestService(mailerFake *registerMailerFake) *authService {
	cfg := testAuthConfig()
	cfg.VerifyEmailTokenTTL = time.Hour
	cfg.BcryptCost = 12

	return NewAuthService(
		NewAuthRepo(testPool),
		&fakeCache{},
		&fakeRateLimiter{allowed: true},
		mailerFake,
		cfg,
		registerCryptoFake{},
	)
}

func registerCtx(t *testing.T) context.Context {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	return ctx
}

// getRegisterCounts returns row counts after registration scenarios.
func getRegisterCounts(t *testing.T, ctx context.Context) registerCounts {
	t.Helper()

	var counts registerCounts
	err := testPool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM users),
			(SELECT count(*) FROM organizations),
			(SELECT count(*) FROM auth_cred),
			(SELECT count(*) FROM verify_tokens)
	`).Scan(
		&counts.users,
		&counts.organizations,
		&counts.authCred,
		&counts.verifyTokens,
	)
	require.NoError(t, err)

	return counts
}

func TestAuthService_Register_IntegrationSuccess(t *testing.T) {
	truncateAll(t)
	ctx := registerCtx(t)

	mailerFake := &registerMailerFake{}
	svc := newRegisterTestService(mailerFake)

	err := svc.Register(ctx, RegisterRequest{
		Email:    " Test2026@example.com ",
		Password: "strongpass123",
	})
	require.NoError(t, err)

	counts := getRegisterCounts(t, ctx)
	assert.Equal(t, 1, counts.users)
	assert.Equal(t, 1, counts.organizations)
	assert.Equal(t, 1, counts.authCred)
	assert.Equal(t, 1, counts.verifyTokens)

	var userID uuid.UUID
	var orgID uuid.UUID
	var emailVerified bool

	err = testPool.QueryRow(ctx, `
		SELECT id, org_id, email_verified
		FROM users
	`).Scan(&userID, &orgID, &emailVerified)
	require.NoError(t, err)

	assert.False(t, emailVerified)

	var orgName string
	err = testPool.QueryRow(ctx, `
		SELECT name
		FROM organizations
		WHERE id = $1
	`, orgID).Scan(&orgName)
	require.NoError(t, err)

	assert.Equal(t, "Personal organization", orgName)

	var emailHash []byte
	var emailEncrypted []byte
	var passwordHash string
	var role string

	err = testPool.QueryRow(ctx, `
		SELECT email_hash, email_encrypted, password_hash, role
		FROM auth_cred
		WHERE user_id = $1
	`, userID).Scan(&emailHash, &emailEncrypted, &passwordHash, &role)
	require.NoError(t, err)

	expectedEmail := "test2026@example.com"
	expectedEmailHash := registerCryptoFake{}.Hash([]byte(expectedEmail))

	assert.Equal(t, expectedEmailHash, emailHash)
	assert.Equal(t, []byte("encrypted:"+expectedEmail), emailEncrypted)
	assert.NotEqual(t, "strongpass123", passwordHash)
	assert.NoError(t, bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte("strongpass123")))
	assert.Equal(t, RoleDefectologist, role)
	cost, err := bcrypt.Cost([]byte(passwordHash))
	require.NoError(t, err)
	assert.Equal(t, 12, cost)

	var tokenUserID uuid.UUID
	var purpose string
	var tokenHash []byte
	var expiresAt time.Time
	var usedAt *time.Time

	err = testPool.QueryRow(ctx, `
		SELECT user_id, purpose, token_hash, expires_at, used_at
		FROM verify_tokens
	`).Scan(&tokenUserID, &purpose, &tokenHash, &expiresAt, &usedAt)
	require.NoError(t, err)

	assert.Equal(t, userID, tokenUserID)
	assert.Equal(t, PurposeEmailVerify, purpose)
	assert.NotEmpty(t, tokenHash)
	assert.True(t, expiresAt.After(time.Now()))
	assert.Nil(t, usedAt)

	require.Equal(t, 1, mailerFake.calls)
	assert.Equal(t, expectedEmail, mailerFake.to)
	assert.Equal(t, mailer.EmailVerify, mailerFake.template)
	assert.Equal(t, expectedEmail, mailerFake.data.Email)
	assert.NotEmpty(t, mailerFake.data.Token)

	rawToken, err := base64.RawURLEncoding.DecodeString(mailerFake.data.Token)
	require.NoError(t, err)

	expectedTokenHash := sha256.Sum256(rawToken)
	assert.Equal(t, expectedTokenHash[:], tokenHash)
}

func TestAuthService_Register_IntegrationDuplicateEmail(t *testing.T) {
	truncateAll(t)
	ctx := registerCtx(t)

	mailerFake := &registerMailerFake{}
	svc := newRegisterTestService(mailerFake)

	err := svc.Register(ctx, RegisterRequest{
		Email:    "duplicate@example.com",
		Password: "strongpass123",
	})
	require.NoError(t, err)

	err = svc.Register(ctx, RegisterRequest{
		Email:    " DUPLICATE@example.com ",
		Password: "anotherStrongPassword123",
	})
	require.Error(t, err)

	var appErr *apperr.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, apperr.ErrConflict.Code, appErr.Code)

	counts := getRegisterCounts(t, ctx)
	assert.Equal(t, 1, counts.users)
	assert.Equal(t, 1, counts.organizations)
	assert.Equal(t, 1, counts.authCred)
	assert.Equal(t, 1, counts.verifyTokens)
	assert.Equal(t, 1, mailerFake.calls)
}

func TestAuthService_Register_MailerErrorIsSuccess(t *testing.T) {
	truncateAll(t)
	ctx := registerCtx(t)

	mailerFake := &registerMailerFake{err: errors.New("smtp unavailable")}
	svc := newRegisterTestService(mailerFake)

	err := svc.Register(ctx, RegisterRequest{
		Email:    "mail-error@example.com",
		Password: "strongpass123",
	})
	require.NoError(t, err)

	counts := getRegisterCounts(t, ctx)
	assert.Equal(t, 1, counts.users)
	assert.Equal(t, 1, counts.organizations)
	assert.Equal(t, 1, counts.authCred)
	assert.Equal(t, 1, counts.verifyTokens)
	assert.Equal(t, 1, mailerFake.calls)
}
