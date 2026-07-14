package profile

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

type fakeUserRepo struct {
	user        *UserPassword
	getErr      error
	updateErr   error
	updatedID   string
	updatedHash string
}

func (f *fakeUserRepo) Get(ctx context.Context, id string) (*UserPassword, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.user, nil
}

func (f *fakeUserRepo) Update(ctx context.Context, id string, newHash string) error {
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

func TestUserService_ChangePassword_Success(t *testing.T) {
	repo := &fakeUserRepo{user: &UserPassword{ID: "user-1", Password: hashPassword(t, "oldpassword")}}
	sessions := &fakeSessionRevoker{}
	svc := NewUserService(repo, sessions)

	err := svc.ChangePassword(context.Background(), "user-1", "newpassword", "oldpassword", "newpassword")

	require.NoError(t, err)
	require.Equal(t, "user-1", repo.updatedID)
	require.NoError(t, bcrypt.CompareHashAndPassword([]byte(repo.updatedHash), []byte("newpassword")))
	require.True(t, sessions.revokeCalled)
	require.Equal(t, "user-1", sessions.revokedID)
}

func TestUserService_ChangePassword_TooShort(t *testing.T) {
	repo := &fakeUserRepo{user: &UserPassword{ID: "user-1", Password: hashPassword(t, "oldpassword")}}
	sessions := &fakeSessionRevoker{}
	svc := NewUserService(repo, sessions)

	err := svc.ChangePassword(context.Background(), "user-1", "short", "oldpassword", "short")

	require.ErrorIs(t, err, ErrPasswordLen)
	require.False(t, sessions.revokeCalled)
	require.Empty(t, repo.updatedID)
}

func TestUserService_ChangePassword_Mismatch(t *testing.T) {
	repo := &fakeUserRepo{user: &UserPassword{ID: "user-1", Password: hashPassword(t, "oldpassword")}}
	sessions := &fakeSessionRevoker{}
	svc := NewUserService(repo, sessions)

	err := svc.ChangePassword(context.Background(), "user-1", "newpassword", "oldpassword", "somethingelse")

	require.ErrorIs(t, err, ErrOverlap)
	require.False(t, sessions.revokeCalled)
	require.Empty(t, repo.updatedID)
}

func TestUserService_ChangePassword_GetError(t *testing.T) {
	wantErr := errors.New("user not found")
	repo := &fakeUserRepo{getErr: wantErr}
	sessions := &fakeSessionRevoker{}
	svc := NewUserService(repo, sessions)

	err := svc.ChangePassword(context.Background(), "user-1", "newpassword", "oldpassword", "newpassword")

	require.ErrorIs(t, err, wantErr)
	require.False(t, sessions.revokeCalled)
}

func TestUserService_ChangePassword_WrongOldPassword(t *testing.T) {
	repo := &fakeUserRepo{user: &UserPassword{ID: "user-1", Password: hashPassword(t, "oldpassword")}}
	sessions := &fakeSessionRevoker{}
	svc := NewUserService(repo, sessions)

	err := svc.ChangePassword(context.Background(), "user-1", "newpassword", "wrongoldpassword", "newpassword")

	require.ErrorIs(t, err, ErrOldPassword)
	require.False(t, sessions.revokeCalled)
	require.Empty(t, repo.updatedID)
}

func TestUserService_ChangePassword_UpdateError(t *testing.T) {
	wantErr := errors.New("update failed")
	repo := &fakeUserRepo{user: &UserPassword{ID: "user-1", Password: hashPassword(t, "oldpassword")}, updateErr: wantErr}
	sessions := &fakeSessionRevoker{}
	svc := NewUserService(repo, sessions)

	err := svc.ChangePassword(context.Background(), "user-1", "newpassword", "oldpassword", "newpassword")

	require.ErrorIs(t, err, wantErr)
	require.False(t, sessions.revokeCalled)
}

func TestUserService_ChangePassword_RevokeSessionsError(t *testing.T) {
	wantErr := errors.New("revoke failed")
	repo := &fakeUserRepo{user: &UserPassword{ID: "user-1", Password: hashPassword(t, "oldpassword")}}
	sessions := &fakeSessionRevoker{revokeErr: wantErr}
	svc := NewUserService(repo, sessions)

	err := svc.ChangePassword(context.Background(), "user-1", "newpassword", "oldpassword", "newpassword")

	require.ErrorIs(t, err, wantErr)
	require.Equal(t, "user-1", repo.updatedID)
}
