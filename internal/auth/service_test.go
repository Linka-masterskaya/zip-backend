package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/cache"
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

func (f *fakeCrypto) Encrypt(_ []byte) ([]byte, error) {
	return []byte("encrypted-email"), nil
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

// Register tests

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

func (tx *rollbackSpyTx) CopyFrom(
	ctx context.Context,
	tableName pgx.Identifier,
	columnNames []string,
	rowSrc pgx.CopyFromSource,
) (int64, error) {
	return 0, errors.New("not implemented")
}

func (tx *rollbackSpyTx) SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults {
	return nil
}

func (tx *rollbackSpyTx) LargeObjects() pgx.LargeObjects {
	return pgx.LargeObjects{}
}

func (tx *rollbackSpyTx) Prepare(
	ctx context.Context,
	name string,
	sql string,
) (*pgconn.StatementDescription, error) {
	return nil, errors.New("not implemented")
}

func (tx *rollbackSpyTx) Exec(
	ctx context.Context,
	sql string,
	arguments ...any,
) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("not implemented")
}

func (tx *rollbackSpyTx) Query(
	ctx context.Context,
	sql string,
	args ...any,
) (pgx.Rows, error) {
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
