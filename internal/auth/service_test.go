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

type fakeCrypto struct {
	hash []byte
}

func (f *fakeCrypto) Hash(_ []byte) []byte {
	return f.hash
}

func (f *fakeCrypto) Decrypt(_ []byte) ([]byte, error) {
	return nil, nil
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
	if cacheStore.rec.Status != "active" {
		t.Fatalf(
			"refresh status = %q, want active",
			cacheStore.rec.Status,
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
