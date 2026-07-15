package profile

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
	"github.com/Linka-masterskaya/zip-backend/internal/reqctx"
	"github.com/stretchr/testify/require"
)

func newChangePasswordRequest(t *testing.T, body ChangePasswordReq, userID string) *http.Request {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/profile/change-password", bytes.NewReader(raw))
	if userID != "" {
		req = req.WithContext(reqctx.PutUserID(req.Context(), userID))
	}
	return req
}

func serve(h *UserHandler, w http.ResponseWriter, r *http.Request) {
	middleware.ErrorMiddleware(h.ChangePassword).ServeHTTP(w, r)
}

func TestHandlerChangePassword_Success(t *testing.T) {
	repo := &fakeUserRepo{user: &UserPassword{ID: "user-1", Password: hashPassword(t, "oldpassword")}}
	sessions := &fakeSessionRevoker{}
	handler := NewUserHandler(NewUserService(repo, sessions))

	req := newChangePasswordRequest(t, ChangePasswordReq{
		CurrentPassword: "oldpassword",
		NewPassword:     "newpassword",
		RepeatPassword:  "newpassword",
	}, "user-1")
	w := httptest.NewRecorder()

	serve(handler, w, req)

	require.Equal(t, http.StatusNoContent, w.Code)
	require.Equal(t, "user-1", repo.updatedID)
	require.True(t, sessions.revokeCalled)
}

func TestHandlerChangePassword_InvalidJSON(t *testing.T) {
	handler := NewUserHandler(NewUserService(&fakeUserRepo{}, &fakeSessionRevoker{}))

	req := httptest.NewRequest(http.MethodPost, "/profile/change-password", strings.NewReader("{invalid json"))
	req = req.WithContext(reqctx.PutUserID(req.Context(), "user-1"))
	w := httptest.NewRecorder()

	serve(handler, w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandlerChangePassword_MissingFields(t *testing.T) {
	tests := []struct {
		name string
		req  ChangePasswordReq
	}{
		{
			name: "missing current password",
			req:  ChangePasswordReq{NewPassword: "newpassword", RepeatPassword: "newpassword"},
		},
		{
			name: "missing new password",
			req:  ChangePasswordReq{CurrentPassword: "oldpassword", RepeatPassword: "newpassword"},
		},
		{
			name: "missing repeat password",
			req:  ChangePasswordReq{CurrentPassword: "oldpassword", NewPassword: "newpassword"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := NewUserHandler(NewUserService(&fakeUserRepo{}, &fakeSessionRevoker{}))

			req := newChangePasswordRequest(t, tt.req, "user-1")
			w := httptest.NewRecorder()

			serve(handler, w, req)

			require.Equal(t, http.StatusBadRequest, w.Code)
		})
	}
}

func TestHandlerChangePassword_Unauthorized_NoUserID(t *testing.T) {
	handler := NewUserHandler(NewUserService(&fakeUserRepo{}, &fakeSessionRevoker{}))

	req := newChangePasswordRequest(t, ChangePasswordReq{
		CurrentPassword: "oldpassword",
		NewPassword:     "newpassword",
		RepeatPassword:  "newpassword",
	}, "")
	w := httptest.NewRecorder()

	serve(handler, w, req)

	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestHandlerChangePassword_PasswordTooShort(t *testing.T) {
	repo := &fakeUserRepo{user: &UserPassword{ID: "user-1", Password: hashPassword(t, "oldpassword")}}
	sessions := &fakeSessionRevoker{}
	handler := NewUserHandler(NewUserService(repo, sessions))

	req := newChangePasswordRequest(t, ChangePasswordReq{
		CurrentPassword: "oldpassword",
		NewPassword:     "short",
		RepeatPassword:  "short",
	}, "user-1")
	w := httptest.NewRecorder()

	serve(handler, w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.False(t, sessions.revokeCalled)
}

func TestHandlerChangePassword_PasswordMismatch(t *testing.T) {
	repo := &fakeUserRepo{user: &UserPassword{ID: "user-1", Password: hashPassword(t, "oldpassword")}}
	sessions := &fakeSessionRevoker{}
	handler := NewUserHandler(NewUserService(repo, sessions))

	req := newChangePasswordRequest(t, ChangePasswordReq{
		CurrentPassword: "oldpassword",
		NewPassword:     "newpassword",
		RepeatPassword:  "somethingelse",
	}, "user-1")
	w := httptest.NewRecorder()

	serve(handler, w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.False(t, sessions.revokeCalled)
}

func TestHandlerChangePassword_WrongOldPassword(t *testing.T) {
	repo := &fakeUserRepo{user: &UserPassword{ID: "user-1", Password: hashPassword(t, "oldpassword")}}
	sessions := &fakeSessionRevoker{}
	handler := NewUserHandler(NewUserService(repo, sessions))

	req := newChangePasswordRequest(t, ChangePasswordReq{
		CurrentPassword: "wrongoldpassword",
		NewPassword:     "newpassword",
		RepeatPassword:  "newpassword",
	}, "user-1")
	w := httptest.NewRecorder()

	serve(handler, w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.False(t, sessions.revokeCalled)
}

func TestHandlerChangePassword_UserNotFound(t *testing.T) {
	repo := &fakeUserRepo{getErr: sql.ErrNoRows}
	sessions := &fakeSessionRevoker{}
	handler := NewUserHandler(NewUserService(repo, sessions))

	req := newChangePasswordRequest(t, ChangePasswordReq{
		CurrentPassword: "oldpassword",
		NewPassword:     "newpassword",
		RepeatPassword:  "newpassword",
	}, "user-1")
	w := httptest.NewRecorder()

	serve(handler, w, req)

	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandlerChangePassword_InternalError(t *testing.T) {
	repo := &fakeUserRepo{user: &UserPassword{ID: "user-1", Password: hashPassword(t, "oldpassword")}, updateErr: errors.New("db is down")}
	sessions := &fakeSessionRevoker{}
	handler := NewUserHandler(NewUserService(repo, sessions))

	req := newChangePasswordRequest(t, ChangePasswordReq{
		CurrentPassword: "oldpassword",
		NewPassword:     "newpassword",
		RepeatPassword:  "newpassword",
	}, "user-1")
	w := httptest.NewRecorder()

	serve(handler, w, req)

	require.Equal(t, http.StatusInternalServerError, w.Code)
}
