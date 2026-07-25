package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	pgx "github.com/jackc/pgx/v5"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/cache"
	"github.com/Linka-masterskaya/zip-backend/internal/config"
	"github.com/Linka-masterskaya/zip-backend/internal/mailer"
	"go.uber.org/mock/gomock"
	"golang.org/x/crypto/bcrypt"
)

type fakeCache struct {
	called bool

	jti string
	rec cache.RefreshRecord
	ttl time.Duration

	err error

	revokeCalls   int
	revokedUserID string
	revokeErr     error
}

func (f *fakeCache) StoreRefresh(
	_ context.Context,
	jti string,
	rec cache.RefreshRecord,
	ttl time.Duration,
) error {
	f.called = true
	f.jti = jti
	f.rec = rec
	f.ttl = ttl

	return f.err
}

func (f *fakeCache) RevokeAllSessions(_ context.Context, userID string) error {
	f.revokeCalls++
	f.revokedUserID = userID

	return f.revokeErr
}

type fakeCrypto struct {
	hash []byte
}

func (f *fakeCrypto) Hash(_ []byte) []byte {
	return f.hash
}

func (f *fakeCrypto) Decrypt(_ []byte) ([]byte, error) {
	return nil, nil
}

type fakeRateLimiter struct {
	allowed bool
	err     error
}

func (f *fakeRateLimiter) Allow(_ context.Context, _ cache.RateLimitRequest) (bool, int64, error) {
	return f.allowed, 0, f.err
}

type fakeTx struct {
	pgx.Tx
}

func (f *fakeTx) Commit(_ context.Context) error   { return nil }
func (f *fakeTx) Rollback(_ context.Context) error { return nil }

func TestAuthService_Login_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockauthRepoIface(ctrl)

	password := "correct-password"
	passwordHash, err := bcrypt.GenerateFromPassword(
		[]byte(password),
		bcrypt.DefaultCost,
	)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	repo.EXPECT().
		GetUserByEmailHash(gomock.Any(), []byte("email-hash")).
		Return(&User{
			ID:            "user-id",
			OrgID:         ptrString("org-id"),
			PasswordHash:  ptrString(string(passwordHash)),
			Role:          "defectologist",
			EmailVerified: true,
		}, nil)

	cacheStore := &fakeCache{}
	crypto := &fakeCrypto{hash: []byte("email-hash")}

	svc := NewAuthService(
		repo,
		cacheStore,
		&fakeRateLimiter{allowed: true},
		nil,
		testAuthConfig(),
		crypto,
	)

	result, err := svc.Login(
		context.Background(),
		" USER@example.com ",
		password,
	)
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	if result.AccessToken == "" {
		t.Fatal("access token is empty")
	}
	if result.RefreshToken == "" {
		t.Fatal("refresh token is empty")
	}
	if !cacheStore.called {
		t.Fatal("refresh token was not stored")
	}
	if cacheStore.rec.Status != "active" {
		t.Fatalf(
			"refresh status = %q, want active",
			cacheStore.rec.Status,
		)
	}
	if cacheStore.rec.UserID != "user-id" {
		t.Fatalf(
			"refresh user id = %q, want user-id",
			cacheStore.rec.UserID,
		)
	}
	if cacheStore.ttl != time.Hour {
		t.Fatalf(
			"ttl = %v, want %v",
			cacheStore.ttl,
			time.Hour,
		)
	}
}

func TestAuthService_Login_WrongPassword(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockauthRepoIface(ctrl)

	passwordHash, err := bcrypt.GenerateFromPassword(
		[]byte("correct-password"),
		bcrypt.DefaultCost,
	)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	repo.EXPECT().
		GetUserByEmailHash(gomock.Any(), gomock.Any()).
		Return(&User{
			ID:            "user-id",
			OrgID:         ptrString("org-id"),
			PasswordHash:  ptrString(string(passwordHash)),
			Role:          "defectologist",
			EmailVerified: true,
		}, nil)

	cacheStore := &fakeCache{}
	crypto := &fakeCrypto{hash: []byte("email-hash")}

	svc := NewAuthService(
		repo,
		cacheStore,
		&fakeRateLimiter{allowed: true},
		nil,
		testAuthConfig(),
		crypto,
	)

	_, err = svc.Login(
		context.Background(),
		"user@example.com",
		"wrong-password",
	)
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf(
			"err = %v, want %v",
			err,
			ErrInvalidCredentials,
		)
	}
	if cacheStore.called {
		t.Fatal("refresh token should not be stored")
	}
}

func TestAuthService_Login_UserNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockauthRepoIface(ctrl)

	repo.EXPECT().
		GetUserByEmailHash(gomock.Any(), gomock.Any()).
		Return(nil, apperr.ErrUserNotFound)

	cacheStore := &fakeCache{}
	crypto := &fakeCrypto{hash: []byte("email-hash")}

	svc := NewAuthService(
		repo,
		cacheStore,
		&fakeRateLimiter{allowed: true},
		nil,
		testAuthConfig(),
		crypto,
	)

	_, err := svc.Login(
		context.Background(),
		"missing@example.com",
		"password",
	)
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf(
			"err = %v, want %v",
			err,
			ErrInvalidCredentials,
		)
	}
	if cacheStore.called {
		t.Fatal("refresh token should not be stored")
	}
}

func TestAuthService_Login_EmailNotVerified(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockauthRepoIface(ctrl)

	password := "correct-password"
	passwordHash, err := bcrypt.GenerateFromPassword(
		[]byte(password),
		bcrypt.DefaultCost,
	)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}

	repo.EXPECT().
		GetUserByEmailHash(gomock.Any(), gomock.Any()).
		Return(&User{
			ID:            "user-id",
			OrgID:         ptrString("org-id"),
			PasswordHash:  ptrString(string(passwordHash)),
			Role:          "defectologist",
			EmailVerified: false,
		}, nil)

	cacheStore := &fakeCache{}
	crypto := &fakeCrypto{hash: []byte("email-hash")}

	cfg := testAuthConfig()
	cfg.RequireEmailVerification = true

	svc := NewAuthService(
		repo,
		cacheStore,
		&fakeRateLimiter{allowed: true},
		nil,
		cfg,
		crypto,
	)

	_, err = svc.Login(
		context.Background(),
		"user@example.com",
		password,
	)
	if !errors.Is(err, ErrEmailNotVerified) {
		t.Fatalf(
			"err = %v, want %v",
			err,
			ErrEmailNotVerified,
		)
	}
	if cacheStore.called {
		t.Fatal("refresh token should not be stored")
	}
}

func testResendConfig() Config {
	cfg := testAuthConfig()
	cfg.VerifyEmailTokenTTL = 24 * time.Hour
	cfg.FrontendURL = "https://example.com"
	cfg.RateLimit = config.RateLimitConfig{
		Resend: config.RateLimitRule{
			Scope:  "resend",
			Limit:  5,
			Window: time.Hour,
		},
	}
	return cfg
}

func TestAuthService_ResendEmail_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockauthRepoIface(ctrl)

	userID := uuid.Must(uuid.NewV7())

	repo.EXPECT().
		GetUserByEmailHash(gomock.Any(), []byte("email-hash")).
		Return(&User{
			ID:            userID.String(),
			EmailVerified: false,
		}, nil)

	repo.EXPECT().
		rotateEmailTokens(gomock.Any(), gomock.Any(), userID, gomock.Any(), gomock.Any()).
		Return(nil)

	ml := &passwordResetMailerFake{}
	crypto := &passwordResetCryptoFake{hash: []byte("email-hash")}

	svc := NewAuthService(
		repo,
		&fakeCache{},
		&fakeRateLimiter{allowed: true},
		ml,
		testResendConfig(),
		crypto,
	)

	err := svc.resendEmail(context.Background(), "user@example.com")
	if err != nil {
		t.Fatalf("resendEmail: %v", err)
	}

	if ml.calls != 1 {
		t.Fatalf("mailer calls = %d, want 1", ml.calls)
	}
	if ml.to != "user@example.com" {
		t.Fatalf("mailer to = %q, want user@example.com", ml.to)
	}
	if ml.template != mailer.EmailVerify {
		t.Fatalf("mailer template = %v, want EmailVerify", ml.template)
	}
}

func TestAuthService_ResendEmail_RateLimited(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockauthRepoIface(ctrl)

	crypto := &passwordResetCryptoFake{hash: []byte("email-hash")}

	svc := NewAuthService(
		repo,
		&fakeCache{},
		&fakeRateLimiter{allowed: false},
		&passwordResetMailerFake{},
		testResendConfig(),
		crypto,
	)

	err := svc.resendEmail(context.Background(), "user@example.com")
	if !errors.Is(err, apperr.ErrTooManyRequests) {
		t.Fatalf("err = %v, want ErrTooManyRequests", err)
	}
}

func TestAuthService_ResendEmail_UserNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockauthRepoIface(ctrl)

	repo.EXPECT().
		GetUserByEmailHash(gomock.Any(), []byte("email-hash")).
		Return(nil, apperr.ErrUserNotFound)

	ml := &passwordResetMailerFake{}
	crypto := &passwordResetCryptoFake{hash: []byte("email-hash")}

	svc := NewAuthService(
		repo,
		&fakeCache{},
		&fakeRateLimiter{allowed: true},
		ml,
		testResendConfig(),
		crypto,
	)

	err := svc.resendEmail(context.Background(), "unknown@example.com")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if ml.calls != 0 {
		t.Fatal("mailer should not be called")
	}
}

func TestAuthService_ResendEmail_AlreadyVerified(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockauthRepoIface(ctrl)

	repo.EXPECT().
		GetUserByEmailHash(gomock.Any(), []byte("email-hash")).
		Return(&User{
			ID:            uuid.Must(uuid.NewV7()).String(),
			EmailVerified: true,
		}, nil)

	ml := &passwordResetMailerFake{}
	crypto := &passwordResetCryptoFake{hash: []byte("email-hash")}

	svc := NewAuthService(
		repo,
		&fakeCache{},
		&fakeRateLimiter{allowed: true},
		ml,
		testResendConfig(),
		crypto,
	)

	err := svc.resendEmail(context.Background(), "verified@example.com")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if ml.calls != 0 {
		t.Fatal("mailer should not be called for verified email")
	}
}

func testAuthConfig() Config {
	return Config{
		JWTSecret:       "01234567890123456789012345678901",
		AccessTokenTTL:  time.Minute,
		RefreshTokenTTL: time.Hour,
	}
}

func ptrString(value string) *string {
	return &value
}

func TestAuthService_VerifyEmail_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockauthRepoIface(ctrl)
	txRepo := NewMockauthRepoIface(ctrl)

	tx := &fakeTx{}
	userID := uuid.Must(uuid.NewV7())

	repo.EXPECT().
		beginTx(gomock.Any()).
		Return(tx, nil)

	repo.EXPECT().
		withTx(tx).
		Return(txRepo)

	txRepo.EXPECT().
		useEmailVerifyToken(gomock.Any(), gomock.Any()).
		Return(userID, uuid.Nil, nil)

	txRepo.EXPECT().
		verifyUser(gomock.Any(), userID).
		Return(nil)

	svc := NewAuthService(
		repo,
		&fakeCache{},
		&fakeRateLimiter{allowed: true},
		&passwordResetMailerFake{},
		testAuthConfig(),
		&fakeCrypto{},
	)

	err := svc.verifyEmail(context.Background(), "MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDEy")
	if err != nil {
		t.Fatalf("verifyEmail: %v", err)
	}
}

func TestAuthService_VerifyEmail_InvalidBase64(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockauthRepoIface(ctrl)

	svc := NewAuthService(
		repo,
		&fakeCache{},
		&fakeRateLimiter{allowed: true},
		&passwordResetMailerFake{},
		testAuthConfig(),
		&fakeCrypto{},
	)

	err := svc.verifyEmail(context.Background(), "!!!invalid-base64!!!")
	if !errors.Is(err, apperr.ErrVerifyTokenInvalid) {
		t.Fatalf("err = %v, want ErrVerifyTokenInvalid", err)
	}
}

func TestAuthService_VerifyEmail_TokenNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockauthRepoIface(ctrl)
	txRepo := NewMockauthRepoIface(ctrl)

	tx := &fakeTx{}

	repo.EXPECT().
		beginTx(gomock.Any()).
		Return(tx, nil)

	repo.EXPECT().
		withTx(tx).
		Return(txRepo)

	txRepo.EXPECT().
		useEmailVerifyToken(gomock.Any(), gomock.Any()).
		Return(uuid.Nil, uuid.Nil, apperr.ErrVerifyTokenInvalid)

	svc := NewAuthService(
		repo,
		&fakeCache{},
		&fakeRateLimiter{allowed: true},
		&passwordResetMailerFake{},
		testAuthConfig(),
		&fakeCrypto{},
	)

	err := svc.verifyEmail(context.Background(), "MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDEy")
	if !errors.Is(err, apperr.ErrVerifyTokenInvalid) {
		t.Fatalf("err = %v, want ErrVerifyTokenInvalid", err)
	}
}

func TestAuthService_VerifyEmail_Student(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockauthRepoIface(ctrl)
	txRepo := NewMockauthRepoIface(ctrl)

	tx := &fakeTx{}
	studentID := uuid.Must(uuid.NewV7())

	repo.EXPECT().
		beginTx(gomock.Any()).
		Return(tx, nil)

	repo.EXPECT().
		withTx(tx).
		Return(txRepo)

	txRepo.EXPECT().
		useEmailVerifyToken(gomock.Any(), gomock.Any()).
		Return(uuid.Nil, studentID, nil)

	txRepo.EXPECT().
		verifyStudent(gomock.Any(), studentID).
		Return(nil)

	svc := NewAuthService(
		repo,
		&fakeCache{},
		&fakeRateLimiter{allowed: true},
		&passwordResetMailerFake{},
		testAuthConfig(),
		&fakeCrypto{},
	)

	err := svc.verifyEmail(context.Background(), "MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDEy")
	if err != nil {
		t.Fatalf("verifyEmail: %v", err)
	}
}
