package profile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

func TestHandler_UpdateProfile_Success(t *testing.T) {
	userID := uuid.New()
	displayName := "Анна Мария"
	createdAt := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	expected := &Response{
		ID:            userID.String(),
		Email:         "anna@example.com",
		DisplayName:   &displayName,
		Role:          "defectologist",
		EmailVerified: true,
		CreatedAt:     createdAt,
	}

	serviceCalled := false
	service := &mockService{
		updateDisplayNameFn: func(_ context.Context, gotUserID uuid.UUID, gotDisplayName string) (*Response, error) {
			serviceCalled = true
			assert.Equal(t, userID, gotUserID)
			assert.Equal(t, displayName, gotDisplayName)
			return expected, nil
		},
	}
	handler := NewHandler(service)

	ctx := authctx.SetUserIDToCtx(context.Background(), userID)
	request := httptest.NewRequestWithContext(
		ctx,
		http.MethodPatch,
		"/api/v1/profile/me",
		bytes.NewBufferString(`{"display_name":"Анна Мария"}`),
	)
	responseRecorder := httptest.NewRecorder()

	err := handler.UpdateProfile(responseRecorder, request)

	require.NoError(t, err)
	assert.True(t, serviceCalled)
	assert.Equal(t, http.StatusOK, responseRecorder.Code)
	assert.Equal(t, "application/json", responseRecorder.Header().Get("Content-Type"))

	var actual Response
	require.NoError(t, json.Unmarshal(responseRecorder.Body.Bytes(), &actual))
	assert.Equal(t, expected.ID, actual.ID)
	assert.Equal(t, expected.Email, actual.Email)
	assert.Equal(t, expected.DisplayName, actual.DisplayName)
	assert.Equal(t, expected.Role, actual.Role)
	assert.Equal(t, expected.EmailVerified, actual.EmailVerified)
	assert.Equal(t, expected.CreatedAt, actual.CreatedAt)
}

func TestHandler_UpdateProfile_Unauthorized(t *testing.T) {
	serviceCalled := false
	service := &mockService{
		updateDisplayNameFn: func(_ context.Context, _ uuid.UUID, _ string) (*Response, error) {
			serviceCalled = true
			return nil, nil
		},
	}
	handler := NewHandler(service)
	request := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodPatch,
		"/api/v1/profile/me",
		bytes.NewBufferString("{\"display_name\":\"Анна\"}"),
	)
	responseRecorder := httptest.NewRecorder()

	err := handler.UpdateProfile(responseRecorder, request)

	assertAppErrorStatus(t, err, http.StatusUnauthorized)
	assert.False(t, serviceCalled)
}

func TestHandler_UpdateProfile_InvalidRequest(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "invalid JSON",
			body: `{"display_name":`,
		},
		{
			name: "missing display_name",
			body: `{}`,
		},
		{
			name: "null display_name",
			body: `{"display_name":null}`,
		},
		{
			name: "unknown field",
			body: `{"display_name":"Анна","email":"new@example.com"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serviceCalled := false
			service := &mockService{
				updateDisplayNameFn: func(_ context.Context, _ uuid.UUID, _ string) (*Response, error) {
					serviceCalled = true
					return nil, nil
				},
			}
			handler := NewHandler(service)

			ctx := authctx.SetUserIDToCtx(context.Background(), uuid.New())
			request := httptest.NewRequestWithContext(
				ctx,
				http.MethodPatch,
				"/api/v1/profile/me",
				bytes.NewBufferString(tt.body),
			)
			responseRecorder := httptest.NewRecorder()

			err := handler.UpdateProfile(responseRecorder, request)

			assertAppErrorStatus(t, err, http.StatusBadRequest)
			assert.False(t, serviceCalled)
		})
	}
}

func TestHandler_UpdateProfile_ReturnsServiceError(t *testing.T) {
	expectedErr := errors.New("update profile")
	service := &mockService{
		updateDisplayNameFn: func(_ context.Context, _ uuid.UUID, _ string) (*Response, error) {
			return nil, expectedErr
		},
	}
	handler := NewHandler(service)

	ctx := authctx.SetUserIDToCtx(context.Background(), uuid.New())
	request := httptest.NewRequestWithContext(
		ctx,
		http.MethodPatch,
		"/api/v1/profile/me",
		bytes.NewBufferString(`{"display_name":"Анна"}`),
	)
	responseRecorder := httptest.NewRecorder()

	err := handler.UpdateProfile(responseRecorder, request)

	assert.ErrorIs(t, err, expectedErr)
}

func assertAppErrorStatus(t *testing.T, err error, expectedStatus int) {
	t.Helper()

	var appErr *apperr.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, expectedStatus, appErr.HTTPStatus)
}
