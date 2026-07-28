package profile

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/authctx"
)

// mockService implements the ProfileService interface for testing.
type mockService struct {
	getProfileFn         func(ctx context.Context, userID uuid.UUID) (*Response, error)
	replaceAvatarFn      func(ctx context.Context, userID string, reader io.Reader, size int64, mimeType string) (string, error)
	deleteAvatarFn       func(ctx context.Context, userID string) error
	requestEmailChangeFn func(ctx context.Context, userID uuid.UUID, newEmail string) error
	confirmEmailChangeFn func(ctx context.Context, tokenStr string) error
}

func (m *mockService) GetProfile(ctx context.Context, userID uuid.UUID) (*Response, error) {
	if m.getProfileFn != nil {
		return m.getProfileFn(ctx, userID)
	}
	return nil, apperr.ErrInternal
}

func (m *mockService) ReplaceAvatar(ctx context.Context, userID string, reader io.Reader, size int64, mimeType string) (string, error) {
	if m.replaceAvatarFn != nil {
		return m.replaceAvatarFn(ctx, userID, reader, size, mimeType)
	}
	return "", nil
}

func (m *mockService) DeleteAvatar(ctx context.Context, userID string) error {
	if m.deleteAvatarFn != nil {
		return m.deleteAvatarFn(ctx, userID)
	}
	return nil
}

func (m *mockService) RequestEmailChange(ctx context.Context, userID uuid.UUID, newEmail string) error {
	if m.requestEmailChangeFn != nil {
		return m.requestEmailChangeFn(ctx, userID, newEmail)
	}
	return nil
}

func (m *mockService) ConfirmEmailChange(ctx context.Context, tokenStr string) error {
	if m.confirmEmailChangeFn != nil {
		return m.confirmEmailChangeFn(ctx, tokenStr)
	}
	return nil
}

func TestHandler_GetProfile(t *testing.T) {
	testUserID := uuid.New()

	tests := []struct {
		name               string
		setupContext       func(ctx context.Context) context.Context
		mockServiceFn      func(ctx context.Context, userID uuid.UUID) (*Response, error)
		expectedStatusCode int
		expectedEmail      string
		checkNullFields    bool
	}{
		{
			name: "успешный запрос",
			setupContext: func(ctx context.Context) context.Context {
				return authctx.SetUserIDToCtx(ctx, testUserID)
			},
			mockServiceFn: func(ctx context.Context, userID uuid.UUID) (*Response, error) {
				return &Response{
					ID:            userID.String(),
					Email:         "test1234@example.com",
					DisplayName:   nil,
					AvatarURL:     nil,
					Role:          "defectologist",
					EmailVerified: false,
					OrgID:         nil,
					CreatedAt:     time.Now(),
				}, nil
			},
			expectedStatusCode: http.StatusOK,
			expectedEmail:      "test1234@example.com",
			checkNullFields:    true,
		},
		{
			name: "ошибка: пользователь не авторизован",
			setupContext: func(ctx context.Context) context.Context {
				return ctx
			},
			mockServiceFn:      nil,
			expectedStatusCode: http.StatusUnauthorized,
		},
		{
			name: "ошибка: пользователь не найден",
			setupContext: func(ctx context.Context) context.Context {
				return authctx.SetUserIDToCtx(ctx, testUserID)
			},
			mockServiceFn: func(ctx context.Context, userID uuid.UUID) (*Response, error) {
				return nil, apperr.ErrUnauthorized
			},
			expectedStatusCode: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &mockService{getProfileFn: tt.mockServiceFn}
			handler := NewHandler(svc)

			ctx := tt.setupContext(context.Background())
			req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/v1/profile/me", nil)

			rr := httptest.NewRecorder()

			err := handler.GetProfile(rr, req)
			if err != nil {
				var appErr *apperr.AppError
				if errors.As(err, &appErr) {
					rr.Code = appErr.HTTPStatus
				} else {
					rr.Code = http.StatusInternalServerError
				}
			}

			assert.Equal(t, tt.expectedStatusCode, rr.Code)

			if tt.expectedStatusCode == http.StatusOK {
				var profile Response
				err := json.Unmarshal(rr.Body.Bytes(), &profile)
				require.NoError(t, err)

				assert.Equal(t, testUserID.String(), profile.ID)
				assert.Equal(t, tt.expectedEmail, profile.Email)

				if tt.checkNullFields {
					assert.Nil(t, profile.DisplayName)
					assert.Nil(t, profile.AvatarURL)
					assert.Nil(t, profile.OrgID)
				}
			}
		})
	}
}
