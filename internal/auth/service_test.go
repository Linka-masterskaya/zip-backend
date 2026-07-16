package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/cache"
	"go.uber.org/mock/gomock"
	"golang.org/x/crypto/bcrypt"
)

type fakeCache struct {
	called bool

	jti string
	rec cache.RefreshRecord
	ttl time.Duration

	err error

	getRec *cache.RefreshRecord
	getErr error

	revokedFID string
	revokeErr  error
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

func (f *fakeCache) GetRefresh(_ context.Context, jti string) (*cache.RefreshRecord, error) {
	f.jti = jti
	return f.getRec, f.getErr
}

func (f *fakeCache) RevokeFamily(_ context.Context, fid string) error {
	f.revokedFID = fid
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

func TestAuthService_Logout_RevokesFamily(t *testing.T) {
	svc := NewAuthService(nil, &fakeCache{}, nil, testAuthConfig(), nil)

	token, err := svc.generateRefreshToken(&User{ID: "user-id"}, "jti-1")
	if err != nil {
		t.Fatalf("generate refresh token: %v", err)
	}

	cacheStore := &fakeCache{getRec: &cache.RefreshRecord{FID: "fam-1", Status: "active"}}
	svc = NewAuthService(nil, cacheStore, nil, testAuthConfig(), nil)

	if err := svc.Logout(context.Background(), token); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if cacheStore.jti != "jti-1" {
		t.Errorf("looked up jti = %q, want jti-1", cacheStore.jti)
	}
	if cacheStore.revokedFID != "fam-1" {
		t.Errorf("revoked fid = %q, want fam-1", cacheStore.revokedFID)
	}
}

func TestAuthService_Logout_Idempotent(t *testing.T) {
	svc := NewAuthService(nil, &fakeCache{}, nil, testAuthConfig(), nil)
	token, err := svc.generateRefreshToken(&User{ID: "user-id"}, "jti-1")
	if err != nil {
		t.Fatalf("generate refresh token: %v", err)
	}

	tests := []struct {
		name  string
		token string
		cache *fakeCache
	}{
		{name: "empty token", token: "", cache: &fakeCache{}},
		{name: "malformed token", token: "not-a-jwt", cache: &fakeCache{}},
		{
			name:  "unknown jti",
			token: token,
			cache: &fakeCache{getErr: cache.ErrNotFound},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewAuthService(nil, tt.cache, nil, testAuthConfig(), nil)
			if err := svc.Logout(context.Background(), tt.token); err != nil {
				t.Fatalf("logout: %v", err)
			}
			if tt.cache.revokedFID != "" {
				t.Errorf("unexpected revoke of fid %q", tt.cache.revokedFID)
			}
		})
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
