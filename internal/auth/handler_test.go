// internal/auth/handler_test.go
package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

var validToken = strings.Repeat("t", 43)

func TestAuthHandler_Login(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		mockSetup  func(m *MockauthServiceIface)
		wantStatus int
		wantCode   string
	}{
		{
			name: "success",
			body: `{"email":"user@example.com","password":"password123"}`,
			mockSetup: func(m *MockauthServiceIface) {
				m.EXPECT().
					Login(gomock.Any(), "user@example.com", "password123").
					Return(&LoginResult{AccessToken: "access-token", RefreshToken: "refresh-token"}, nil)
			},
			wantStatus: http.StatusOK,
		},
		{
			name:       "malformed json",
			body:       `{"email":`,
			mockSetup:  func(m *MockauthServiceIface) {},
			wantStatus: http.StatusBadRequest,
			wantCode:   "BAD_REQUEST",
		},
		{
			name: "invalid credentials",
			body: `{"email":"user@example.com","password":"wrong"}`,
			mockSetup: func(m *MockauthServiceIface) {
				m.EXPECT().
					Login(gomock.Any(), "user@example.com", "wrong").
					Return(nil, ErrInvalidCredentials)
			},
			wantStatus: http.StatusUnauthorized,
			wantCode:   "UNAUTHORIZED",
		},
		{
			name: "email not verified",
			body: `{"email":"user@example.com","password":"password123"}`,
			mockSetup: func(m *MockauthServiceIface) {
				m.EXPECT().
					Login(gomock.Any(), "user@example.com", "password123").
					Return(nil, ErrEmailNotVerified)
			},
			wantStatus: http.StatusForbidden,
			wantCode:   "FORBIDDEN",
		},
		{
			name: "service error",
			body: `{"email":"user@example.com","password":"password123"}`,
			mockSetup: func(m *MockauthServiceIface) {
				m.EXPECT().
					Login(gomock.Any(), "user@example.com", "password123").
					Return(nil, apperr.ErrInternal)
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

			h := NewAuthHandler(mockSvc)
			wrapped := middleware.ErrorMiddleware(h.Login)

			req := httptest.NewRequestWithContext(
				context.Background(),
				http.MethodPost,
				"/auth/login",
				bytes.NewBufferString(tt.body),
			)
			rec := httptest.NewRecorder()

			wrapped.ServeHTTP(rec, req)

			assert.Equal(t, tt.wantStatus, rec.Code)

			if tt.wantCode != "" {
				var resp middleware.JSONErrorResponse
				err := json.Unmarshal(rec.Body.Bytes(), &resp)
				require.NoError(t, err)
				assert.Equal(t, tt.wantCode, resp.Error.Code)
			}
		})
	}
}

func TestAuthHandler_VerifyEmail(t *testing.T) {
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

			h := NewAuthHandler(mockSvc)
			wrapped := middleware.ErrorMiddleware(h.VerifyEmail)

			req := httptest.NewRequestWithContext(
				context.Background(),
				http.MethodPost,
				"/auth/verify-email",
				bytes.NewBufferString(tt.body),
			)
			rec := httptest.NewRecorder()

			wrapped.ServeHTTP(rec, req)

			assert.Equal(t, tt.wantStatus, rec.Code)

			if tt.wantCode != "" {
				var resp middleware.JSONErrorResponse
				err := json.Unmarshal(rec.Body.Bytes(), &resp)
				require.NoError(t, err)
				assert.Equal(t, tt.wantCode, resp.Error.Code)
			}
		})
	}
}

func TestAuthHandler_ResendEmail(t *testing.T) {
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
			name:       "malformed json",
			body:       `{"email":`,
			mockSetup:  func(m *MockauthServiceIface) {},
			wantStatus: http.StatusBadRequest,
			wantCode:   "BAD_REQUEST",
		},
		{
			name:       "invalid email",
			body:       `{"email":"not-an-email"}`,
			mockSetup:  func(m *MockauthServiceIface) {},
			wantStatus: http.StatusBadRequest,
			wantCode:   "BAD_REQUEST",
		},
		{
			name: "user not found",
			body: `{"email":"user@example.com"}`,
			mockSetup: func(m *MockauthServiceIface) {
				m.EXPECT().resendEmail(gomock.Any(), "user@example.com").Return(apperr.ErrUserNotFound)
			},
			wantStatus: http.StatusNotFound,
			wantCode:   "USER_NOT_FOUND",
		},
		{
			name: "mailer/db failure",
			body: `{"email":"user@example.com"}`,
			mockSetup: func(m *MockauthServiceIface) {
				m.EXPECT().resendEmail(gomock.Any(), "user@example.com").Return(apperr.ErrInternal)
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

			h := NewAuthHandler(mockSvc)
			wrapped := middleware.ErrorMiddleware(h.ResendEmail)

			req := httptest.NewRequestWithContext(
				context.Background(),
				http.MethodPost,
				"/auth/verify-email/resend",
				bytes.NewBufferString(tt.body),
			)
			rec := httptest.NewRecorder()

			wrapped.ServeHTTP(rec, req)

			assert.Equal(t, tt.wantStatus, rec.Code)

			if tt.wantCode != "" {
				var resp middleware.JSONErrorResponse
				err := json.Unmarshal(rec.Body.Bytes(), &resp)
				require.NoError(t, err)
				assert.Equal(t, tt.wantCode, resp.Error.Code)
			}
		})
	}
}

func TestAuthHandler_ForgotPassword(t *testing.T) {
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
			name:       "invalid email",
			body:       `{"email":"not-an-email"}`,
			mockSetup:  func(m *MockauthServiceIface) {},
			wantStatus: http.StatusBadRequest,
			wantCode:   "BAD_REQUEST",
		},
		{
			name: "user not found (hidden)",
			body: `{"email":"user@example.com"}`,
			mockSetup: func(m *MockauthServiceIface) {
				m.EXPECT().ForgotPassword(gomock.Any(), "user@example.com").Return(nil)
			},
			wantStatus: http.StatusAccepted,
		},
		{
			name: "service error",
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

			h := NewAuthHandler(mockSvc)
			wrapped := middleware.ErrorMiddleware(h.ForgotPassword)

			req := httptest.NewRequestWithContext(
				context.Background(),
				http.MethodPost,
				"/auth/password/forgot",
				bytes.NewBufferString(tt.body),
			)
			rec := httptest.NewRecorder()

			wrapped.ServeHTTP(rec, req)

			assert.Equal(t, tt.wantStatus, rec.Code)

			if tt.wantCode != "" {
				var resp middleware.JSONErrorResponse
				err := json.Unmarshal(rec.Body.Bytes(), &resp)
				require.NoError(t, err)
				assert.Equal(t, tt.wantCode, resp.Error.Code)
			}
		})
	}
}

func TestAuthHandler_ResetPassword(t *testing.T) {
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
			name:       "weak password",
			body:       `{"token":"reset-token","new_password":"short"}`,
			mockSetup:  func(m *MockauthServiceIface) {},
			wantStatus: http.StatusBadRequest,
			wantCode:   "BAD_REQUEST",
		},
		{
			name: "invalid token",
			body: `{"token":"bad-token","new_password":"NewPassword123"}`,
			mockSetup: func(m *MockauthServiceIface) {
				m.EXPECT().ResetPassword(gomock.Any(), "bad-token", "NewPassword123").Return(apperr.ErrInvalidResetToken)
			},
			wantStatus: http.StatusBadRequest,
			wantCode:   "INVALID_RESET_TOKEN",
		},
		{
			name: "service error",
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

			h := NewAuthHandler(mockSvc)
			wrapped := middleware.ErrorMiddleware(h.ResetPassword)

			req := httptest.NewRequestWithContext(
				context.Background(),
				http.MethodPost,
				"/auth/password/reset",
				bytes.NewBufferString(tt.body),
			)
			rec := httptest.NewRecorder()

			wrapped.ServeHTTP(rec, req)

			assert.Equal(t, tt.wantStatus, rec.Code)

			if tt.wantCode != "" {
				var resp middleware.JSONErrorResponse
				err := json.Unmarshal(rec.Body.Bytes(), &resp)
				require.NoError(t, err)
				assert.Equal(t, tt.wantCode, resp.Error.Code)
			}
		})
	}
}

func TestAuthHandler_Refresh(t *testing.T) {
	tests := []struct {
		name       string
		mockSetup  func(m *MockauthServiceIface)
		wantStatus int
		wantCode   string
	}{
		{
			name: "success",
			mockSetup: func(m *MockauthServiceIface) {
				m.EXPECT().Refresh(gomock.Any(), "refresh-token").Return(&LoginResult{
					AccessToken:  "new-access-token",
					RefreshToken: "new-refresh-token",
				}, nil)
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "no cookie",
			mockSetup: func(m *MockauthServiceIface) {
				// no EXPECT - should fail before calling service
			},
			wantStatus: http.StatusUnauthorized,
			wantCode:   "UNAUTHORIZED",
		},
		{
			name: "invalid refresh token",
			mockSetup: func(m *MockauthServiceIface) {
				m.EXPECT().Refresh(gomock.Any(), "invalid-token").Return(nil, apperr.ErrJWTTokenInvalid)
			},
			wantStatus: http.StatusUnauthorized,
			wantCode:   "JWT_TOKEN_INVALID",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockSvc := NewMockauthServiceIface(ctrl)
			tt.mockSetup(mockSvc)

			h := NewAuthHandler(mockSvc)
			wrapped := middleware.ErrorMiddleware(h.Refresh)

			req := httptest.NewRequestWithContext(
				context.Background(),
				http.MethodPost,
				"/auth/refresh",
				nil,
			)
			if tt.name != "no cookie" {
				req.AddCookie(&http.Cookie{
					Name:  "refresh_token",
					Value: "refresh-token",
				})
			}
			if tt.name == "invalid refresh token" {
				req.AddCookie(&http.Cookie{
					Name:  "refresh_token",
					Value: "invalid-token",
				})
			}
			rec := httptest.NewRecorder()

			wrapped.ServeHTTP(rec, req)

			assert.Equal(t, tt.wantStatus, rec.Code)

			if tt.wantCode != "" {
				var resp middleware.JSONErrorResponse
				err := json.Unmarshal(rec.Body.Bytes(), &resp)
				require.NoError(t, err)
				assert.Equal(t, tt.wantCode, resp.Error.Code)
			}
		})
	}
}

func TestAuthHandler_Logout(t *testing.T) {
	tests := []struct {
		name       string
		mockSetup  func(m *MockauthServiceIface)
		wantStatus int
	}{
		{
			name: "success with cookie",
			mockSetup: func(m *MockauthServiceIface) {
				m.EXPECT().Logout(gomock.Any(), "refresh-token").Return(nil)
			},
			wantStatus: http.StatusNoContent,
		},
		{
			name: "success without cookie",
			mockSetup: func(m *MockauthServiceIface) {
			},
			wantStatus: http.StatusNoContent,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockSvc := NewMockauthServiceIface(ctrl)
			tt.mockSetup(mockSvc)

			h := NewAuthHandler(mockSvc)
			wrapped := middleware.ErrorMiddleware(h.Logout)

			req := httptest.NewRequestWithContext(
				context.Background(),
				http.MethodPost,
				"/auth/logout",
				nil,
			)
			if tt.name == "success with cookie" {
				req.AddCookie(&http.Cookie{
					Name:  "refresh_token",
					Value: "refresh-token",
				})
			}
			rec := httptest.NewRecorder()

			wrapped.ServeHTTP(rec, req)

			assert.Equal(t, tt.wantStatus, rec.Code)
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

			cfg := Config{
				RefreshTokenTTL: time.Hour,
				CookieSecure:    false,
				FrontendURL:     frontendURL,
			}
			h := NewAuthHandler(mockSvc, cfg)

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

			assert.Equal(t, tt.wantStatus, rec.Code)

			if tt.wantCode != "" {
				var resp middleware.JSONErrorResponse
				err := json.Unmarshal(rec.Body.Bytes(), &resp)
				require.NoError(t, err)
				assert.Equal(t, tt.wantCode, resp.Error.Code)
			}
		})
	}
}
