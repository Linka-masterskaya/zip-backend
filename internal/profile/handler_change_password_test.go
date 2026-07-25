package profile

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
	"github.com/stretchr/testify/require"
)

func newChangePasswordRequest(t *testing.T, body ChangePasswordReq, ctx context.Context) *http.Request {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)

	return httptest.NewRequestWithContext(ctx, http.MethodPost, "/profile/change-password", bytes.NewReader(raw))
}

func serve(h *ChangePasswordHandler, w http.ResponseWriter, r *http.Request) {
	middleware.ErrorMiddleware(h.ChangePassword).ServeHTTP(w, r)
}

func TestHandlerChangePassword_Success(t *testing.T) {
	repo := &fakeChangePasswordRepo{user: &UserPassword{ID: testUserID, Password: hashPassword(t, "oldpassword")}}
	sessions := &fakeSessionRevoker{}
	handler := NewChangePasswordHandler(NewChangePasswordService(repo, sessions))

	req := newChangePasswordRequest(t, ChangePasswordReq{
		CurrentPassword: "oldpassword",
		NewPassword:     "newpassword",
	}, ctxWithUser(testUserID))
	w := httptest.NewRecorder()

	serve(handler, w, req)

	require.Equal(t, http.StatusNoContent, w.Code)
	require.Equal(t, testUserID, repo.updatedID)
	require.True(t, sessions.revokeCalled)
}

func TestHandlerChangePassword_InvalidJSON(t *testing.T) {
	handler := NewChangePasswordHandler(NewChangePasswordService(&fakeChangePasswordRepo{}, &fakeSessionRevoker{}))

	req := httptest.NewRequestWithContext(ctxWithUser(testUserID), http.MethodPost, "/profile/change-password", strings.NewReader("{invalid json"))
	w := httptest.NewRecorder()

	serve(handler, w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandlerChangePassword_Unauthorized_NoUserID(t *testing.T) {
	handler := NewChangePasswordHandler(NewChangePasswordService(&fakeChangePasswordRepo{}, &fakeSessionRevoker{}))

	req := newChangePasswordRequest(t, ChangePasswordReq{
		CurrentPassword: "oldpassword",
		NewPassword:     "newpassword",
	}, context.Background())
	w := httptest.NewRecorder()

	serve(handler, w, req)

	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestHandlerChangePassword_PasswordTooShort(t *testing.T) {
	repo := &fakeChangePasswordRepo{user: &UserPassword{ID: testUserID, Password: hashPassword(t, "oldpassword")}}
	sessions := &fakeSessionRevoker{}
	handler := NewChangePasswordHandler(NewChangePasswordService(repo, sessions))

	req := newChangePasswordRequest(t, ChangePasswordReq{
		CurrentPassword: "oldpassword",
		NewPassword:     "short",
	}, ctxWithUser(testUserID))
	w := httptest.NewRecorder()

	serve(handler, w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.False(t, sessions.revokeCalled)
}

func TestHandlerChangePassword_WrongOldPassword(t *testing.T) {
	repo := &fakeChangePasswordRepo{user: &UserPassword{ID: testUserID, Password: hashPassword(t, "oldpassword")}}
	sessions := &fakeSessionRevoker{}
	handler := NewChangePasswordHandler(NewChangePasswordService(repo, sessions))

	req := newChangePasswordRequest(t, ChangePasswordReq{
		CurrentPassword: "wrongoldpassword",
		NewPassword:     "newpassword",
	}, ctxWithUser(testUserID))
	w := httptest.NewRecorder()

	serve(handler, w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.False(t, sessions.revokeCalled)
}

func TestHandlerChangePassword_UserNotFound(t *testing.T) {
	repo := &fakeChangePasswordRepo{getErr: sql.ErrNoRows}
	sessions := &fakeSessionRevoker{}
	handler := NewChangePasswordHandler(NewChangePasswordService(repo, sessions))

	req := newChangePasswordRequest(t, ChangePasswordReq{
		CurrentPassword: "oldpassword",
		NewPassword:     "newpassword",
	}, ctxWithUser(testUserID))
	w := httptest.NewRecorder()

	serve(handler, w, req)

	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandlerChangePassword_InternalError(t *testing.T) {
	repo := &fakeChangePasswordRepo{user: &UserPassword{ID: testUserID, Password: hashPassword(t, "oldpassword")}, updateErr: errors.New("db is down")}
	sessions := &fakeSessionRevoker{}
	handler := NewChangePasswordHandler(NewChangePasswordService(repo, sessions))

	req := newChangePasswordRequest(t, ChangePasswordReq{
		CurrentPassword: "oldpassword",
		NewPassword:     "newpassword",
	}, ctxWithUser(testUserID))
	w := httptest.NewRecorder()

	serve(handler, w, req)

	require.Equal(t, http.StatusInternalServerError, w.Code)
}
