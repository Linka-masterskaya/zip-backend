package profile

import (
	"context"
	"errors"
	"testing"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/authctx"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

type fakeChangePasswordRepo struct {
	user        *UserPassword
	getErr      error
	updateErr   error
	updatedID   uuid.UUID
	updatedHash string
}

func (f *fakeChangePasswordRepo) Get(ctx context.Context, id uuid.UUID) (*UserPassword, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.user, nil
}

func (f *fakeChangePasswordRepo) Update(ctx context.Context, id uuid.UUID, newHash string) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	f.updatedID = id
	f.updatedHash = newHash
	return nil
}

type fakeSessionRevoker struct {
	revokeErr    error
	revokedID    string
	revokeCalled bool
}

func (f *fakeSessionRevoker) RevokeAllSessions(ctx context.Context, userID string) error {
	f.revokeCalled = true
	f.revokedID = userID
	return f.revokeErr
}

func hashPassword(t *testing.T, password string) string {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	require.NoError(t, err)
	return string(hash)
}

var testUserID = uuid.MustParse("11111111-1111-1111-1111-111111111111")

func ctxWithUser(id uuid.UUID) context.Context {
	return authctx.SetUserIDToCtx(context.Background(), id)
}

func TestChangePassword_Success(t *testing.T) {
	repo := &fakeChangePasswordRepo{user: &UserPassword{ID: testUserID, Password: hashPassword(t, "oldpassword")}}
	sessions := &fakeSessionRevoker{}
	svc := NewChangePasswordService(repo, sessions)

	err := svc.ChangePassword(ctxWithUser(testUserID), "newpassword", "oldpassword")

	require.NoError(t, err)
	require.Equal(t, testUserID, repo.updatedID)
	require.NoError(t, bcrypt.CompareHashAndPassword([]byte(repo.updatedHash), []byte("newpassword")))
	require.True(t, sessions.revokeCalled)
	require.Equal(t, testUserID.String(), sessions.revokedID)
}

func TestChangePassword_NoUserInContext(t *testing.T) {
	repo := &fakeChangePasswordRepo{}
	sessions := &fakeSessionRevoker{}
	svc := NewChangePasswordService(repo, sessions)

	err := svc.ChangePassword(context.Background(), "newpassword", "oldpassword")

	require.ErrorIs(t, err, apperr.ErrUnauthorized)
	require.False(t, sessions.revokeCalled)
}

func TestChangePassword_TooShort(t *testing.T) {
	repo := &fakeChangePasswordRepo{user: &UserPassword{ID: testUserID, Password: hashPassword(t, "oldpassword")}}
	sessions := &fakeSessionRevoker{}
	svc := NewChangePasswordService(repo, sessions)

	err := svc.ChangePassword(ctxWithUser(testUserID), "short", "oldpassword")

	var appErr *apperr.AppError
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, apperr.ErrBadRequest.Code, appErr.Code)
	require.False(t, sessions.revokeCalled)
	require.Empty(t, repo.updatedHash)
}

func TestChangePassword_GetError(t *testing.T) {
	wantErr := errors.New("user not found")
	repo := &fakeChangePasswordRepo{getErr: wantErr}
	sessions := &fakeSessionRevoker{}
	svc := NewChangePasswordService(repo, sessions)

	err := svc.ChangePassword(ctxWithUser(testUserID), "newpassword", "oldpassword")

	require.ErrorIs(t, err, wantErr)
	require.False(t, sessions.revokeCalled)
}

func TestChangePassword_WrongOldPassword(t *testing.T) {
	repo := &fakeChangePasswordRepo{user: &UserPassword{ID: testUserID, Password: hashPassword(t, "oldpassword")}}
	sessions := &fakeSessionRevoker{}
	svc := NewChangePasswordService(repo, sessions)

	err := svc.ChangePassword(ctxWithUser(testUserID), "newpassword", "wrongoldpassword")

	var appErr *apperr.AppError
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, apperr.ErrBadRequest.Code, appErr.Code)
	require.False(t, sessions.revokeCalled)
	require.Empty(t, repo.updatedHash)
}

func TestChangePassword_UpdateError(t *testing.T) {
	wantErr := errors.New("update failed")
	repo := &fakeChangePasswordRepo{user: &UserPassword{ID: testUserID, Password: hashPassword(t, "oldpassword")}, updateErr: wantErr}
	sessions := &fakeSessionRevoker{}
	svc := NewChangePasswordService(repo, sessions)

	err := svc.ChangePassword(ctxWithUser(testUserID), "newpassword", "oldpassword")

	require.ErrorIs(t, err, wantErr)
	require.False(t, sessions.revokeCalled)
}

func TestChangePassword_RevokeSessionsErrorIsSwallowed(t *testing.T) {
	repo := &fakeChangePasswordRepo{user: &UserPassword{ID: testUserID, Password: hashPassword(t, "oldpassword")}}
	sessions := &fakeSessionRevoker{revokeErr: errors.New("revoke failed")}
	svc := NewChangePasswordService(repo, sessions)

	err := svc.ChangePassword(ctxWithUser(testUserID), "newpassword", "oldpassword")

	require.NoError(t, err)
	require.Equal(t, testUserID, repo.updatedID)
	require.True(t, sessions.revokeCalled)
}
