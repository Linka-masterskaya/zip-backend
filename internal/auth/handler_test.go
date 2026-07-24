// internal/auth/handler_test.go
package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
		mockSetup  func(m *MockauthServiceIface)
		wantStatus int
		wantCode   string
	}{
		{
			name: "success",
			mockSetup: func(m *MockauthServiceIface) {
				m.EXPECT().resendEmail(gomock.Any()).Return(nil)
			},
			wantStatus: http.StatusAccepted,
		},
		{
			name: "no user in context (auth middleware not in chain)",
			mockSetup: func(m *MockauthServiceIface) {
				m.EXPECT().resendEmail(gomock.Any()).Return(apperr.ErrUnauthorized)
			},
			wantStatus: http.StatusUnauthorized,
			wantCode:   "UNAUTHORIZED",
		},
		{
			name: "user not found",
			mockSetup: func(m *MockauthServiceIface) {
				m.EXPECT().resendEmail(gomock.Any()).Return(apperr.ErrUserNotFound)
			},
			wantStatus: http.StatusNotFound,
			wantCode:   "USER_NOT_FOUND",
		},
		{
			name: "mailer/decrypt/db failure",
			mockSetup: func(m *MockauthServiceIface) {
				m.EXPECT().resendEmail(gomock.Any()).Return(apperr.ErrInternal)
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
				nil,
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

			h := NewAuthHandler(mockSvc)
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

			h := NewAuthHandler(mockSvc)
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
