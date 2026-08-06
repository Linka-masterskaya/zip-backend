package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/cache"
	"github.com/Linka-masterskaya/zip-backend/internal/mailer"
	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
	"github.com/google/uuid"
	pgx "github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

type rollbackSpyTx struct {
	rollbackCalls int
	commitCalls   int
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

func (f *fakeCrypto) Encrypt(data []byte) ([]byte, error) {
	return data, nil
}

type fakeRateLimiter struct {
	allowed bool
	err     error
}

func (f *fakeRateLimiter) Allow(_ context.Context, _ cache.RateLimitRequest) (bool, int64, error) {
	return f.allowed, 0, f.err
}

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

func (f *fakeCache) GetRefresh(
	ctx context.Context,
	jti string,
) (*cache.RefreshRecord, error) {
	return nil, nil
}

func (f *fakeCache) RevokeFamily(
	ctx context.Context,
	fid string,
) error {
	return nil
}

func (f *fakeCache) IsFamilyRevoked(
	ctx context.Context,
	fid string,
) (bool, error) {
	return false, nil
}

func (f *fakeCache) IsSessionRevoked(
	_ context.Context,
	_ cache.RefreshRecord,
) (bool, error) {
	return false, nil
}

func (f *fakeCache) RotateRefresh(
	ctx context.Context,
	req cache.RotateRefreshRequest,
) error {
	return nil
}

func TestAuthService_Refresh_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockauthRepoIface(ctrl)
	cacheStore := NewMockrefreshStore(ctrl)

	cfg := testAuthConfig()
	svc := NewAuthService(
		repo,
		cacheStore,
		&fakeRateLimiter{allowed: true},
		nil,
		cfg,
		&fakeCrypto{},
	)

	userID := uuid.New()
	user := &User{
		ID:   userID.String(),
		Role: "defectologist",
	}

	const (
		oldJTI = "old-jti"
		fid    = "family-id"
	)

	oldRefreshToken, err := svc.generateRefreshToken(user, oldJTI)
	if err != nil {
		t.Fatalf("generate old refresh token: %v", err)
	}

	cacheStore.EXPECT().
		GetRefresh(gomock.Any(), oldJTI).
		Return(&cache.RefreshRecord{
			FID:    fid,
			Status: "active",
		}, nil)

	cacheStore.EXPECT().
		IsFamilyRevoked(gomock.Any(), fid).
		Return(false, nil)

	cacheStore.EXPECT().
		IsSessionRevoked(gomock.Any(), gomock.Any()).
		Return(false, nil)

	repo.EXPECT().
		GetUserByID(gomock.Any(), userID).
		Return(user, nil)

	cacheStore.EXPECT().
		RotateRefresh(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, req cache.RotateRefreshRequest) error {
			if req.OldJTI != oldJTI {
				t.Errorf("OldJTI = %q, want %q", req.OldJTI, oldJTI)
			}
			if req.NewJTI == "" {
				t.Error("NewJTI is empty")
			}
			if req.NewJTI == oldJTI {
				t.Error("NewJTI must differ from OldJTI")
			}
			if req.NewRecord.FID != fid {
				t.Errorf("FID = %q, want %q", req.NewRecord.FID, fid)
			}
			if req.NewRecord.Status != "active" {
				t.Errorf("status = %q, want active", req.NewRecord.Status)
			}
			if req.TTL != cfg.RefreshTokenTTL {
				t.Errorf("TTL = %v, want %v", req.TTL, cfg.RefreshTokenTTL)
			}
			return nil
		})

	result, err := svc.Refresh(context.Background(), oldRefreshToken)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}

	if result.AccessToken == "" {
		t.Fatal("access token is empty")
	}
	if result.RefreshToken == "" {
		t.Fatal("refresh token is empty")
	}
	if result.RefreshToken == oldRefreshToken {
		t.Fatal("refresh token was not rotated")
	}
}

func TestAuthService_Refresh_ReuseRevokesFamily(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockauthRepoIface(ctrl)
	cacheStore := NewMockrefreshStore(ctrl)

	svc := NewAuthService(
		repo,
		cacheStore,
		&fakeRateLimiter{allowed: true},
		nil,
		testAuthConfig(),
		&fakeCrypto{},
	)

	user := &User{
		ID:   uuid.NewString(),
		Role: "defectologist",
	}

	const (
		oldJTI = "revoked-jti"
		fid    = "family-id"
	)

	oldRefreshToken, err := svc.generateRefreshToken(user, oldJTI)
	if err != nil {
		t.Fatalf("generate old refresh token: %v", err)
	}

	cacheStore.EXPECT().
		GetRefresh(gomock.Any(), oldJTI).
		Return(&cache.RefreshRecord{
			FID:    fid,
			Status: "revoked",
		}, nil)

	cacheStore.EXPECT().
		RevokeFamily(gomock.Any(), fid).
		Return(nil)

	_, err = svc.Refresh(context.Background(), oldRefreshToken)
	if !errors.Is(err, apperr.ErrJWTTokenInvalid) {
		t.Fatalf("err = %v, want %v", err, apperr.ErrJWTTokenInvalid)
	}
}

func TestAuthService_Refresh_FamilyRevoked(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockauthRepoIface(ctrl)
	cacheStore := NewMockrefreshStore(ctrl)

	svc := NewAuthService(
		repo,
		cacheStore,
		&fakeRateLimiter{allowed: true},
		nil,
		testAuthConfig(),
		&fakeCrypto{},
	)

	user := &User{
		ID:   uuid.NewString(),
		Role: "defectologist",
	}

	const (
		oldJTI = "old-jti"
		fid    = "revoked-family"
	)

	oldRefreshToken, err := svc.generateRefreshToken(user, oldJTI)
	if err != nil {
		t.Fatalf("generate old refresh token: %v", err)
	}

	cacheStore.EXPECT().
		GetRefresh(gomock.Any(), oldJTI).
		Return(&cache.RefreshRecord{
			FID:    fid,
			Status: "active",
		}, nil)

	cacheStore.EXPECT().
		IsFamilyRevoked(gomock.Any(), fid).
		Return(true, nil)

	_, err = svc.Refresh(context.Background(), oldRefreshToken)
	if !errors.Is(err, apperr.ErrJWTTokenInvalid) {
		t.Fatalf("err = %v, want %v", err, apperr.ErrJWTTokenInvalid)
	}
}

func TestAuthService_Refresh_SessionRevoked(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockauthRepoIface(ctrl)
	cacheStore := NewMockrefreshStore(ctrl)
	svc := NewAuthService(repo, cacheStore, &fakeRateLimiter{allowed: true}, nil, testAuthConfig(), &fakeCrypto{})

	user := &User{ID: uuid.NewString(), Role: "defectologist"}
	const (
		oldJTI = "old-session-jti"
		fid    = "family-id"
	)
	oldRefreshToken, err := svc.generateRefreshToken(user, oldJTI)
	if err != nil {
		t.Fatalf("generate old refresh token: %v", err)
	}
	rec := &cache.RefreshRecord{
		FID:            fid,
		Status:         "active",
		UserID:         user.ID,
		SessionVersion: 0,
	}

	cacheStore.EXPECT().GetRefresh(gomock.Any(), oldJTI).Return(rec, nil)
	cacheStore.EXPECT().IsFamilyRevoked(gomock.Any(), fid).Return(false, nil)
	cacheStore.EXPECT().IsSessionRevoked(gomock.Any(), *rec).Return(true, nil)

	_, err = svc.Refresh(context.Background(), oldRefreshToken)
	if !errors.Is(err, apperr.ErrJWTTokenInvalid) {
		t.Fatalf("err = %v, want %v", err, apperr.ErrJWTTokenInvalid)
	}
}

func TestAuthService_Refresh_RotateError(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockauthRepoIface(ctrl)
	cacheStore := NewMockrefreshStore(ctrl)

	svc := NewAuthService(
		repo,
		cacheStore,
		&fakeRateLimiter{allowed: true},
		nil,
		testAuthConfig(),
		&fakeCrypto{},
	)

	userID := uuid.New()
	user := &User{
		ID:   userID.String(),
		Role: "defectologist",
	}

	const (
		oldJTI = "old-jti"
		fid    = "family-id"
	)

	oldRefreshToken, err := svc.generateRefreshToken(user, oldJTI)
	if err != nil {
		t.Fatalf("generate old refresh token: %v", err)
	}

	cacheStore.EXPECT().
		GetRefresh(gomock.Any(), oldJTI).
		Return(&cache.RefreshRecord{
			FID:    fid,
			Status: "active",
		}, nil)

	cacheStore.EXPECT().
		IsFamilyRevoked(gomock.Any(), fid).
		Return(false, nil)

	cacheStore.EXPECT().
		IsSessionRevoked(gomock.Any(), gomock.Any()).
		Return(false, nil)

	repo.EXPECT().
		GetUserByID(gomock.Any(), userID).
		Return(user, nil)

	cacheStore.EXPECT().
		RotateRefresh(gomock.Any(), gomock.Any()).
		Return(errors.New("redis unavailable"))

	_, err = svc.Refresh(context.Background(), oldRefreshToken)
	if err == nil {
		t.Fatal("expected rotate refresh error")
	}
}

// Register tests.
func (tx *rollbackSpyTx) Begin(ctx context.Context) (pgx.Tx, error) {
	return nil, errors.New("not implemented")
}

func (tx *rollbackSpyTx) Commit(ctx context.Context) error {
	tx.commitCalls++
	return nil
}

func (tx *rollbackSpyTx) Rollback(ctx context.Context) error {
	tx.rollbackCalls++
	return nil
}

func (tx *rollbackSpyTx) CopyFrom(ctx context.Context, tableName pgx.Identifier, columnNames []string, rowSrc pgx.CopyFromSource) (int64, error) {
	return 0, errors.New("not implemented")
}

func (tx *rollbackSpyTx) SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults {
	return nil
}

func (tx *rollbackSpyTx) LargeObjects() pgx.LargeObjects {
	return pgx.LargeObjects{}
}

func (tx *rollbackSpyTx) Prepare(ctx context.Context, name string, sql string) (*pgconn.StatementDescription, error) {
	return nil, errors.New("not implemented")
}

func (tx *rollbackSpyTx) Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("not implemented")
}

func (tx *rollbackSpyTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return nil, errors.New("not implemented")
}

func (tx *rollbackSpyTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return nil
}

func (tx *rollbackSpyTx) Conn() *pgx.Conn {
	return nil
}

func TestAuthService_Register_RollbackOnCreateUserError(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockauthRepoIface(ctrl)
	txRepo := NewMockauthRepoIface(ctrl)
	crypto := NewMockcryptoService(ctrl)

	tx := &rollbackSpyTx{}

	email := "user@example.com"
	emailHash := []byte("email-hash")
	createUserErr := errors.New("create user failed")

	crypto.EXPECT().
		Hash([]byte(email)).
		Return(emailHash)

	repo.EXPECT().
		EmailExists(gomock.Any(), emailHash).
		Return(false, nil)

	crypto.EXPECT().
		Encrypt([]byte(email)).
		Return([]byte("encrypted-email"), nil)

	repo.EXPECT().
		beginTx(gomock.Any()).
		Return(tx, nil)

	repo.EXPECT().
		withTx(tx).
		Return(txRepo)

	txRepo.EXPECT().
		CreateOrganization(gomock.Any(), gomock.Any()).
		Return(nil)

	txRepo.EXPECT().
		CreateUser(gomock.Any(), gomock.Any()).
		Return(createUserErr)

	svc := NewAuthService(
		repo,
		&fakeCache{},
		&fakeRateLimiter{allowed: true},
		&registerMailerFake{},
		testAuthConfig(),
		crypto,
	)

	err := svc.Register(context.Background(), RegisterRequest{
		Email:    " user@example.com ",
		Password: "strongpass123",
	})
	require.Error(t, err)
	assert.ErrorContains(t, err, "create user")
	assert.Equal(t, 1, tx.rollbackCalls)
	assert.Equal(t, 0, tx.commitCalls)
}

func testResendConfig() Config {
	cfg := testAuthConfig()
	cfg.VerifyEmailTokenTTL = 24 * time.Hour
	cfg.FrontendURL = "https://example.com"
	cfg.RateLimit = middleware.RateLimitPolicy{
		Scope:  "resend",
		Limit:  5,
		Window: time.Hour,
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

	ml := &passwordResetMailerFake{called: make(chan struct{})}
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

	select {
	case <-ml.called:
	case <-time.After(time.Second):
		t.Fatal("resend email was not sent")
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

	userID := uuid.Must(uuid.NewV7())

	repo.EXPECT().
		GetUserByEmailHash(gomock.Any(), []byte("email-hash")).
		Return(&User{
			ID:            userID.String(),
			EmailVerified: false,
		}, nil)

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
