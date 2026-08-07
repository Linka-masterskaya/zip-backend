package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/cache"
	"github.com/google/uuid"
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

	GetRefreshResult    *cache.RefreshRecord
	GetRefreshErr       error
	RevokeFamilyErr     error
	IsFamilyRevokedRes  bool
	IsFamilyRevokedErr  error
	IsSessionRevokedRes bool
	IsSessionRevokedErr error
	RotateRefreshErr    error
	RotateRefreshCalled bool
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

func (f *fakeCache) GetRefresh(
	_ context.Context,
	_ string,
) (*cache.RefreshRecord, error) {
	return f.GetRefreshResult, f.GetRefreshErr
}

func (f *fakeCache) RevokeFamily(
	_ context.Context,
	_ string,
) error {
	return f.RevokeFamilyErr
}

func (f *fakeCache) IsFamilyRevoked(
	_ context.Context,
	_ string,
) (bool, error) {
	return f.IsFamilyRevokedRes, f.IsFamilyRevokedErr
}

func (f *fakeCache) IsSessionRevoked(
	_ context.Context,
	_ cache.RefreshRecord,
) (bool, error) {
	return f.IsSessionRevokedRes, f.IsSessionRevokedErr
}

func (f *fakeCache) RotateRefresh(
	_ context.Context,
	_ cache.RotateRefreshRequest,
) error {
	f.RotateRefreshCalled = true
	return f.RotateRefreshErr
}

type fakeCrypto struct {
	hash []byte
}

func (f *fakeCrypto) Hash(_ []byte) []byte {
	return f.hash
}

func (f *fakeCrypto) Encrypt(_ []byte) ([]byte, error) {
	return []byte("encrypted_data"), nil
}

func (f *fakeCrypto) Decrypt(_ []byte) ([]byte, error) {
	return []byte("test@example.com"), nil
}

type fakeRateLimiter struct {
	allowed bool
	err     error
}

func (f *fakeRateLimiter) Allow(_ context.Context, _ cache.RateLimitRequest) (bool, int64, error) {
	return f.allowed, 0, f.err
}

func testAuthConfig() Config {
	return Config{
		JWTSecret:       "01234567890123456789012345678901",
		AccessTokenTTL:  time.Minute,
		RefreshTokenTTL: time.Hour,
		BcryptCost:      bcrypt.DefaultCost,
	}
}

func ptrString(value string) *string {
	return &value
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
		Return(nil, ErrUserNotFound)

	cacheStore := &fakeCache{}
	crypto := &fakeCrypto{hash: []byte("email-hash")}

	svc := NewAuthService(
		repo,
		cacheStore,
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

// Тесты для Refresh используют MockrefreshStore, который уже имеет все необходимые методы
func TestAuthService_Refresh_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockauthRepoIface(ctrl)
	cacheStore := NewMockrefreshStore(ctrl)

	cfg := testAuthConfig()
	svc := NewAuthService(
		repo,
		cacheStore,
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
	svc := NewAuthService(repo, cacheStore, nil, testAuthConfig(), &fakeCrypto{})

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
