package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/domain"
	"go.uber.org/mock/gomock"
	"golang.org/x/crypto/bcrypt"
)

type passwordResetMailerFake struct {
	calls int

	to       string
	template domain.Template
	data     domain.EmailData

	err error
}

func (f *passwordResetMailerFake) Send(
	_ context.Context,
	to string,
	template domain.Template,
	data domain.EmailData,
) error {
	f.calls++
	f.to = to
	f.template = template
	f.data = data

	return f.err
}

type passwordResetCryptoFake struct {
	hash []byte

	gotHashInput []byte
}

func (f *passwordResetCryptoFake) Hash(data []byte) []byte {
	f.gotHashInput = append([]byte(nil), data...)
	return f.hash
}

func (f *passwordResetCryptoFake) Decrypt(_ []byte) ([]byte, error) {
	return nil, nil
}

func testPasswordResetConfig() Config {
	cfg := testAuthConfig()
	cfg.ResetPasswordTokenTTL = time.Hour
	cfg.BcryptCost = bcrypt.DefaultCost
	return cfg
}

func TestAuthService_ForgotPassword_InvalidEmail(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockauthRepoIface(ctrl)

	svc := NewAuthService(
		repo,
		&fakeCache{},
		&passwordResetMailerFake{},
		testPasswordResetConfig(),
		&passwordResetCryptoFake{hash: []byte("email-hash")},
	)

	err := svc.ForgotPassword(context.Background(), "not-an-email")
	var appErr *apperr.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("err = %v, want app error", err)
	}
	if appErr.Code != apperr.ErrBadRequest.Code {
		t.Fatalf("code = %s, want %s", appErr.Code, apperr.ErrBadRequest.Code)
	}
}

func TestAuthService_ForgotPassword_UserNotFoundIsSuccess(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockauthRepoIface(ctrl)

	repo.EXPECT().
		GetUserByEmailHash(gomock.Any(), []byte("email-hash")).
		Return(nil, apperr.ErrUserNotFound)

	mailer := &passwordResetMailerFake{}
	crypto := &passwordResetCryptoFake{hash: []byte("email-hash")}

	svc := NewAuthService(
		repo,
		&fakeCache{},
		mailer,
		testPasswordResetConfig(),
		crypto,
	)

	err := svc.ForgotPassword(context.Background(), " USER@example.com ")
	if err != nil {
		t.Fatalf("ForgotPassword: %v", err)
	}
	if string(crypto.gotHashInput) != "user@example.com" {
		t.Fatalf("hash input = %q, want user@example.com", crypto.gotHashInput)
	}
	if mailer.calls != 0 {
		t.Fatalf("mailer calls = %d, want 0", mailer.calls)
	}
}

func TestAuthService_ForgotPassword_SendsResetEmail(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockauthRepoIface(ctrl)

	cfg := testPasswordResetConfig()
	repo.EXPECT().
		GetUserByEmailHash(gomock.Any(), []byte("email-hash")).
		Return(&User{ID: "user-id"}, nil)
	repo.EXPECT().
		CreatePasswordResetToken(gomock.Any(), "user-id", cfg.ResetPasswordTokenTTL).
		Return("reset-token", nil)

	mailer := &passwordResetMailerFake{}
	svc := NewAuthService(
		repo,
		&fakeCache{},
		mailer,
		cfg,
		&passwordResetCryptoFake{hash: []byte("email-hash")},
	)

	err := svc.ForgotPassword(context.Background(), "user@example.com")
	if err != nil {
		t.Fatalf("ForgotPassword: %v", err)
	}
	if mailer.calls != 1 {
		t.Fatalf("mailer calls = %d, want 1", mailer.calls)
	}
	if mailer.to != "user@example.com" {
		t.Fatalf("mailer to = %q, want user@example.com", mailer.to)
	}
	if mailer.template != domain.PasswordReset {
		t.Fatalf("template = %q, want %q", mailer.template, domain.PasswordReset)
	}
	if mailer.data.Token != "reset-token" {
		t.Fatalf("token = %q, want reset-token", mailer.data.Token)
	}
	if mailer.data.Email != "user@example.com" {
		t.Fatalf("email = %q, want user@example.com", mailer.data.Email)
	}
}

func TestAuthService_ForgotPassword_MailerErrorIsSuccess(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockauthRepoIface(ctrl)

	cfg := testPasswordResetConfig()
	repo.EXPECT().
		GetUserByEmailHash(gomock.Any(), []byte("email-hash")).
		Return(&User{ID: "user-id"}, nil)
	repo.EXPECT().
		CreatePasswordResetToken(gomock.Any(), "user-id", cfg.ResetPasswordTokenTTL).
		Return("reset-token", nil)

	svc := NewAuthService(
		repo,
		&fakeCache{},
		&passwordResetMailerFake{err: errors.New("smtp failed")},
		cfg,
		&passwordResetCryptoFake{hash: []byte("email-hash")},
	)

	err := svc.ForgotPassword(context.Background(), "user@example.com")
	if err != nil {
		t.Fatalf("ForgotPassword: %v", err)
	}
}

func TestAuthService_ResetPassword_InvalidInput(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockauthRepoIface(ctrl)

	svc := NewAuthService(
		repo,
		&fakeCache{},
		nil,
		testPasswordResetConfig(),
		&passwordResetCryptoFake{hash: []byte("email-hash")},
	)

	if err := svc.ResetPassword(context.Background(), "", "NewPassword123"); !errors.Is(err, apperr.ErrInvalidResetToken) {
		t.Fatalf("empty token err = %v, want %v", err, apperr.ErrInvalidResetToken)
	}

	if err := svc.ResetPassword(context.Background(), "token", "short"); err == nil {
		t.Fatal("short password err is nil")
	} else {
		var appErr *apperr.AppError
		if !errors.As(err, &appErr) {
			t.Fatalf("short password err = %v, want app error", err)
		}
		if appErr.Code != apperr.ErrBadRequest.Code {
			t.Fatalf("code = %s, want %s", appErr.Code, apperr.ErrBadRequest.Code)
		}
	}
}

func TestAuthService_ResetPassword_InvalidToken(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockauthRepoIface(ctrl)

	repo.EXPECT().
		ResetPasswordByToken(gomock.Any(), "bad-token", gomock.Any()).
		Return("", apperr.ErrInvalidResetToken)

	svc := NewAuthService(
		repo,
		&fakeCache{},
		nil,
		testPasswordResetConfig(),
		&passwordResetCryptoFake{hash: []byte("email-hash")},
	)

	err := svc.ResetPassword(context.Background(), "bad-token", "NewPassword123")
	if !errors.Is(err, apperr.ErrInvalidResetToken) {
		t.Fatalf("err = %v, want %v", err, apperr.ErrInvalidResetToken)
	}
}

func TestAuthService_ResetPassword_UpdatesPasswordAndRevokesSessions(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockauthRepoIface(ctrl)

	var gotPasswordHash string
	repo.EXPECT().
		ResetPasswordByToken(gomock.Any(), "reset-token", gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, passwordHash string) (string, error) {
			gotPasswordHash = passwordHash
			return "user-id", nil
		})

	cacheStore := &fakeCache{}
	svc := NewAuthService(
		repo,
		cacheStore,
		nil,
		testPasswordResetConfig(),
		&passwordResetCryptoFake{hash: []byte("email-hash")},
	)

	err := svc.ResetPassword(context.Background(), "reset-token", "NewPassword123")
	if err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(gotPasswordHash), []byte("NewPassword123")); err != nil {
		t.Fatalf("password hash does not match new password: %v", err)
	}
	if cacheStore.revokeCalls == 0 {
		t.Fatal("sessions revoke was not called")
	}
	if cacheStore.revokedUserID != "user-id" {
		t.Fatalf("revoked user id = %q, want user-id", cacheStore.revokedUserID)
	}
}

func TestAuthService_ResetPassword_LogsRevokerError(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockauthRepoIface(ctrl)

	repo.EXPECT().
		ResetPasswordByToken(gomock.Any(), "reset-token", gomock.Any()).
		Return("user-id", nil)

	revokerErr := errors.New("revoke failed")
	cacheStore := &fakeCache{revokeErr: revokerErr}
	svc := NewAuthService(
		repo,
		cacheStore,
		nil,
		testPasswordResetConfig(),
		&passwordResetCryptoFake{hash: []byte("email-hash")},
	)

	err := svc.ResetPassword(context.Background(), "reset-token", "NewPassword123")
	if err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}
	if cacheStore.revokeCalls == 0 {
		t.Fatal("sessions revoke was not called")
	}
	if cacheStore.revokedUserID != "user-id" {
		t.Fatalf("revoked user id = %q, want user-id", cacheStore.revokedUserID)
	}
}
