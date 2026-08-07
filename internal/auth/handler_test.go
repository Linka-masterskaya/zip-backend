// internal/auth/handler_test.go
package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
	"go.uber.org/mock/gomock"
)

const validToken = "0123456789012345678901234567890123456789012" // 43 chars

func TestVerifyEmail(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		mockSetup  func(m *MockauthServiceIface)
		wantStatus int
		wantCode   string
	}{
		{
			name: "valid token success",
			body: `{"token":"` + validToken + `"}`,
			mockSetup: func(m *MockauthServiceIface) {
				m.EXPECT().verifyEmail(gomock.Any(), validToken).Return(nil)
			},
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "malformed json",
			body:       `{"token":`,
			mockSetup:  func(m *MockauthServiceIface) {},
			wantStatus: http.StatusBadRequest,
			wantCode:   "BAD_REQUEST",
		},
		{
			name:       "empty body",
			body:       ``,
			mockSetup:  func(m *MockauthServiceIface) {},
			wantStatus: http.StatusBadRequest,
			wantCode:   "BAD_REQUEST",
		},
		{
			name:       "token too short",
			body:       `{"token":"short"}`,
			mockSetup:  func(m *MockauthServiceIface) {},
			wantStatus: http.StatusBadRequest,
			wantCode:   "BAD_REQUEST",
		},
		{
			name:       "token too long",
			body:       `{"token":"` + validToken + `extra"}`,
			mockSetup:  func(m *MockauthServiceIface) {},
			wantStatus: http.StatusBadRequest,
			wantCode:   "BAD_REQUEST",
		},
		{
			name: "expired or invalid token",
			body: `{"token":"` + validToken + `"}`,
			mockSetup: func(m *MockauthServiceIface) {
				m.EXPECT().verifyEmail(gomock.Any(), validToken).Return(apperr.ErrVerifyTokenInvalid)
			},
			wantStatus: http.StatusBadRequest,
			wantCode:   "VERIFY_TOKEN_INVALID",
		},
		{
			name: "service internal error",
			body: `{"token":"` + validToken + `"}`,
			mockSetup: func(m *MockauthServiceIface) {
				m.EXPECT().verifyEmail(gomock.Any(), validToken).Return(apperr.ErrInternal)
			},
			wantStatus: http.StatusInternalServerError,
			wantCode:   "INTERNAL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockSvc := NewMockauthServiceIface(ctrl)
			tt.mockSetup(mockSvc)

			h := NewHandler(mockSvc)
			wrapped := middleware.ErrorMiddleware(h.VerifyEmail)

			req := httptest.NewRequestWithContext(
				context.Background(),
				http.MethodPost,
				"/auth/verify-email",
				bytes.NewBufferString(tt.body),
			)
			rec := httptest.NewRecorder()

			wrapped.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}

			if tt.wantCode != "" {
				var resp middleware.JSONErrorResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
					t.Fatalf("unmarshal: %v", err)
				}
				if resp.Error.Code != tt.wantCode {
					t.Errorf("code = %s, want %s", resp.Error.Code, tt.wantCode)
				}
			}
		})
	}
}

func TestResendEmail(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		mockSetup  func(m *MockauthServiceIface)
		wantStatus int
		wantCode   string
	}{
		{
			name: "success",
			body: `{"email":"user@example.com"}`,
			mockSetup: func(m *MockauthServiceIface) {
				m.EXPECT().resendEmail(gomock.Any(), "user@example.com").Return(nil)
			},
			wantStatus: http.StatusAccepted,
		},
		{
			name:       "invalid json",
			body:       `{`,
			mockSetup:  func(_ *MockauthServiceIface) {},
			wantStatus: http.StatusBadRequest,
			wantCode:   "BAD_REQUEST",
		},
		{
			// Несуществующий адрес не отличим от существующего: сервис
			// возвращает nil, чтобы нельзя было перебрать базу пользователей.
			name: "unknown email is indistinguishable from known",
			body: `{"email":"missing@example.com"}`,
			mockSetup: func(m *MockauthServiceIface) {
				m.EXPECT().resendEmail(gomock.Any(), "missing@example.com").Return(nil)
			},
			wantStatus: http.StatusAccepted,
		},
		{
			name: "mailer/db failure",
			body: `{"email":"user@example.com"}`,
			mockSetup: func(m *MockauthServiceIface) {
				m.EXPECT().resendEmail(gomock.Any(), gomock.Any()).Return(apperr.ErrInternal)
			},
			wantStatus: http.StatusInternalServerError,
			wantCode:   "INTERNAL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockSvc := NewMockauthServiceIface(ctrl)
			tt.mockSetup(mockSvc)

			h := NewHandler(mockSvc)
			wrapped := middleware.ErrorMiddleware(h.ResendEmail)

			req := httptest.NewRequestWithContext(
				context.Background(),
				http.MethodPost,
				"/api/v1/auth/verify-email/resend",
				bytes.NewBufferString(tt.body),
			)
			rec := httptest.NewRecorder()

			wrapped.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}

			if tt.wantCode != "" {
				var resp middleware.JSONErrorResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
					t.Fatalf("unmarshal: %v", err)
				}
				if resp.Error.Code != tt.wantCode {
					t.Errorf("code = %s, want %s", resp.Error.Code, tt.wantCode)
				}
			}
		})
	}
}

func TestForgotPassword(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		mockSetup  func(m *MockauthServiceIface)
		wantStatus int
		wantCode   string
	}{
		{
			name: "success",
			body: `{"email":"user@example.com"}`,
			mockSetup: func(m *MockauthServiceIface) {
				m.EXPECT().ForgotPassword(gomock.Any(), "user@example.com").Return(nil)
			},
			wantStatus: http.StatusAccepted,
		},
		{
			name:       "malformed json",
			body:       `{"email":`,
			mockSetup:  func(m *MockauthServiceIface) {},
			wantStatus: http.StatusBadRequest,
			wantCode:   "BAD_REQUEST",
		},
		{
			name: "invalid email",
			body: `{"email":"bad-email"}`,
			mockSetup: func(m *MockauthServiceIface) {
				m.EXPECT().ForgotPassword(gomock.Any(), "bad-email").Return(apperr.ErrBadRequest.WithMessage("invalid email"))
			},
			wantStatus: http.StatusBadRequest,
			wantCode:   "BAD_REQUEST",
		},
		{
			name: "service internal error",
			body: `{"email":"user@example.com"}`,
			mockSetup: func(m *MockauthServiceIface) {
				m.EXPECT().ForgotPassword(gomock.Any(), "user@example.com").Return(apperr.ErrInternal)
			},
			wantStatus: http.StatusInternalServerError,
			wantCode:   "INTERNAL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockSvc := NewMockauthServiceIface(ctrl)
			tt.mockSetup(mockSvc)

			h := NewHandler(mockSvc)
			wrapped := middleware.ErrorMiddleware(h.ForgotPassword)

			req := httptest.NewRequestWithContext(
				context.Background(),
				http.MethodPost,
				"/api/v1/auth/password/forgot",
				bytes.NewBufferString(tt.body),
			)
			rec := httptest.NewRecorder()

			wrapped.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}

			if tt.wantCode != "" {
				var resp middleware.JSONErrorResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
					t.Fatalf("unmarshal: %v", err)
				}
				if resp.Error.Code != tt.wantCode {
					t.Errorf("code = %s, want %s", resp.Error.Code, tt.wantCode)
				}
			}
		})
	}
}

func TestResetPassword(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		mockSetup  func(m *MockauthServiceIface)
		wantStatus int
		wantCode   string
	}{
		{
			name: "success",
			body: `{"token":"reset-token","new_password":"NewPassword123"}`,
			mockSetup: func(m *MockauthServiceIface) {
				m.EXPECT().ResetPassword(gomock.Any(), "reset-token", "NewPassword123").Return(nil)
			},
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "malformed json",
			body:       `{"token":`,
			mockSetup:  func(m *MockauthServiceIface) {},
			wantStatus: http.StatusBadRequest,
			wantCode:   "BAD_REQUEST",
		},
		{
			name: "invalid password",
			body: `{"token":"reset-token","new_password":"short"}`,
			mockSetup: func(m *MockauthServiceIface) {
				m.EXPECT().ResetPassword(gomock.Any(), "reset-token", "short").Return(apperr.ErrBadRequest.WithMessage("password must be 8-72 bytes long"))
			},
			wantStatus: http.StatusBadRequest,
			wantCode:   "BAD_REQUEST",
		},
		{
			name: "invalid reset token",
			body: `{"token":"bad-token","new_password":"NewPassword123"}`,
			mockSetup: func(m *MockauthServiceIface) {
				m.EXPECT().ResetPassword(gomock.Any(), "bad-token", "NewPassword123").Return(apperr.ErrInvalidResetToken)
			},
			wantStatus: http.StatusBadRequest,
			wantCode:   "INVALID_RESET_TOKEN",
		},
		{
			name: "service internal error",
			body: `{"token":"reset-token","new_password":"NewPassword123"}`,
			mockSetup: func(m *MockauthServiceIface) {
				m.EXPECT().ResetPassword(gomock.Any(), "reset-token", "NewPassword123").Return(apperr.ErrInternal)
			},
			wantStatus: http.StatusInternalServerError,
			wantCode:   "INTERNAL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockSvc := NewMockauthServiceIface(ctrl)
			tt.mockSetup(mockSvc)

			h := NewHandler(mockSvc)
			wrapped := middleware.ErrorMiddleware(h.ResetPassword)

			req := httptest.NewRequestWithContext(
				context.Background(),
				http.MethodPost,
				"/api/v1/auth/password/reset",
				bytes.NewBufferString(tt.body),
			)
			rec := httptest.NewRecorder()

			wrapped.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}

			if tt.wantCode != "" {
				var resp middleware.JSONErrorResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
					t.Fatalf("unmarshal: %v", err)
				}
				if resp.Error.Code != tt.wantCode {
					t.Errorf("code = %s, want %s", resp.Error.Code, tt.wantCode)
				}
			}
		})
	}
}
func TestRefresh(t *testing.T) {
	const (
		oldRefreshToken = "old-refresh-token"
		newRefreshToken = "new-refresh-token"
		newAccessToken  = "new-access-token"
	)

	tests := []struct {
		name          string
		cookie        *http.Cookie
		mockSetup     func(m *MockauthServiceIface)
		wantStatus    int
		wantCode      string
		wantAccess    string
		wantSetCookie bool
	}{
		{
			name:       "missing refresh cookie",
			mockSetup:  func(m *MockauthServiceIface) {},
			wantStatus: http.StatusUnauthorized,
			wantCode:   "UNAUTHORIZED",
		},
		{
			name: "empty refresh cookie",
			cookie: &http.Cookie{
				Name:  "refresh_token",
				Value: "",
			},
			mockSetup:  func(m *MockauthServiceIface) {},
			wantStatus: http.StatusUnauthorized,
			wantCode:   "UNAUTHORIZED",
		},
		{
			name: "service returns unauthorized",
			cookie: &http.Cookie{
				Name:  "refresh_token",
				Value: oldRefreshToken,
			},
			mockSetup: func(m *MockauthServiceIface) {
				m.EXPECT().
					Refresh(gomock.Any(), oldRefreshToken).
					Return(nil, apperr.ErrUnauthorized)
			},
			wantStatus: http.StatusUnauthorized,
			wantCode:   "UNAUTHORIZED",
		},
		{
			name: "success",
			cookie: &http.Cookie{
				Name:  "refresh_token",
				Value: oldRefreshToken,
			},
			mockSetup: func(m *MockauthServiceIface) {
				m.EXPECT().
					Refresh(gomock.Any(), oldRefreshToken).
					Return(&LoginResult{
						AccessToken:  newAccessToken,
						RefreshToken: newRefreshToken,
					}, nil)
			},
			wantStatus:    http.StatusOK,
			wantAccess:    newAccessToken,
			wantSetCookie: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockSvc := NewMockauthServiceIface(ctrl)
			tt.mockSetup(mockSvc)

			h := NewHandler(mockSvc)
			h.refreshTokenTTL = time.Hour
			h.cookieSecure = false

			wrapped := middleware.ErrorMiddleware(h.Refresh)

			req := httptest.NewRequestWithContext(
				context.Background(),
				http.MethodPost,
				"/auth/refresh",
				nil,
			)
			if tt.cookie != nil {
				req.AddCookie(tt.cookie)
			}

			rec := httptest.NewRecorder()
			wrapped.ServeHTTP(rec, req)

			assertRefreshStatusAndError(t, rec, tt.wantStatus, tt.wantCode)
			assertRefreshSuccess(
				t,
				rec,
				tt.wantAccess,
				newRefreshToken,
				tt.wantSetCookie,
			)
		})
	}
}

func TestRefresh_OriginCheck(t *testing.T) {
	const (
		oldRefreshToken = "old-refresh-token"
		newRefreshToken = "new-refresh-token"
		newAccessToken  = "new-access-token"
		frontendURL     = "https://app.example.com"
	)

	tests := []struct {
		name       string
		origin     string
		referer    string
		mockSetup  func(m *MockauthServiceIface)
		wantStatus int
		wantCode   string
	}{
		{
			name:       "foreign origin rejected",
			origin:     "https://evil.example.com",
			mockSetup:  func(m *MockauthServiceIface) {},
			wantStatus: http.StatusForbidden,
			wantCode:   "FORBIDDEN",
		},
		{
			name:       "missing origin and referer rejected",
			mockSetup:  func(m *MockauthServiceIface) {},
			wantStatus: http.StatusForbidden,
			wantCode:   "FORBIDDEN",
		},
		{
			name:   "matching origin allowed",
			origin: frontendURL,
			mockSetup: func(m *MockauthServiceIface) {
				m.EXPECT().
					Refresh(gomock.Any(), oldRefreshToken).
					Return(&LoginResult{
						AccessToken:  newAccessToken,
						RefreshToken: newRefreshToken,
					}, nil)
			},
			wantStatus: http.StatusOK,
		},
		{
			name:    "matching referer allowed when origin absent",
			referer: frontendURL + "/some/page",
			mockSetup: func(m *MockauthServiceIface) {
				m.EXPECT().
					Refresh(gomock.Any(), oldRefreshToken).
					Return(&LoginResult{
						AccessToken:  newAccessToken,
						RefreshToken: newRefreshToken,
					}, nil)
			},
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockSvc := NewMockauthServiceIface(ctrl)
			tt.mockSetup(mockSvc)

			h := NewHandler(mockSvc, Config{
				RefreshTokenTTL: time.Hour,
				CookieSecure:    false,
				FrontendURL:     frontendURL,
			})

			wrapped := middleware.ErrorMiddleware(h.Refresh)

			req := httptest.NewRequestWithContext(
				context.Background(),
				http.MethodPost,
				"/auth/refresh",
				nil,
			)
			req.AddCookie(&http.Cookie{Name: "refresh_token", Value: oldRefreshToken})
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			if tt.referer != "" {
				req.Header.Set("Referer", tt.referer)
			}

			rec := httptest.NewRecorder()
			wrapped.ServeHTTP(rec, req)

			assertRefreshStatusAndError(t, rec, tt.wantStatus, tt.wantCode)
		})
	}
}

func assertRefreshStatusAndError(
	t *testing.T,
	rec *httptest.ResponseRecorder,
	wantStatus int,
	wantCode string,
) {
	t.Helper()

	if rec.Code != wantStatus {
		t.Errorf("status = %d, want %d", rec.Code, wantStatus)
	}

	if wantCode == "" {
		return
	}

	var resp middleware.JSONErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal error response: %v", err)
	}
	if resp.Error.Code != wantCode {
		t.Errorf("code = %s, want %s", resp.Error.Code, wantCode)
	}
}

func assertRefreshSuccess(
	t *testing.T,
	rec *httptest.ResponseRecorder,
	wantAccess string,
	wantRefresh string,
	wantSetCookie bool,
) {
	t.Helper()

	if wantAccess != "" {
		var resp LoginResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal refresh response: %v", err)
		}
		if resp.AccessToken != wantAccess {
			t.Errorf("access token = %q, want %q", resp.AccessToken, wantAccess)
		}
	}

	if !wantSetCookie {
		return
	}

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies count = %d, want 1", len(cookies))
	}

	assertRefreshCookie(t, cookies[0], wantRefresh)
}

func assertRefreshCookie(t *testing.T, got *http.Cookie, wantRefresh string) {
	t.Helper()

	if got.Name != "refresh_token" {
		t.Errorf("cookie name = %q, want refresh_token", got.Name)
	}
	if got.Value != wantRefresh {
		t.Errorf("cookie value = %q, want %q", got.Value, wantRefresh)
	}
	if !got.HttpOnly {
		t.Error("refresh cookie must be HttpOnly")
	}
	if got.Path != "/api/v1/auth" {
		t.Errorf("cookie path = %q, want /api/v1/auth", got.Path)
	}
	if got.MaxAge != int(time.Hour.Seconds()) {
		t.Errorf(
			"cookie MaxAge = %d, want %d",
			got.MaxAge,
			int(time.Hour.Seconds()),
		)
	}
}
