package profile

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type updateDisplayNameRepoMock struct {
	RepoInterface
	updateDisplayNameFn func(ctx context.Context, userID uuid.UUID, displayName string) error
	getUserProfileFn    func(ctx context.Context, userID uuid.UUID) (*UserProfile, error)
}

func (m *updateDisplayNameRepoMock) UpdateDisplayName(
	ctx context.Context,
	userID uuid.UUID,
	displayName string,
) error {
	if m.updateDisplayNameFn == nil {
		panic("unexpected call to UpdateDisplayName")
	}
	return m.updateDisplayNameFn(ctx, userID, displayName)
}

func (m *updateDisplayNameRepoMock) GetUserProfile(
	ctx context.Context,
	userID uuid.UUID,
) (*UserProfile, error) {
	if m.getUserProfileFn == nil {
		panic("unexpected call to GetUserProfile")
	}
	return m.getUserProfileFn(ctx, userID)
}

type updateDisplayNameCryptoMock struct {
	CryptoService
	decryptFn func(ciphertext []byte) ([]byte, error)
}

func (m *updateDisplayNameCryptoMock) Decrypt(ciphertext []byte) ([]byte, error) {
	if m.decryptFn == nil {
		panic("unexpected call to Decrypt")
	}
	return m.decryptFn(ciphertext)
}

func TestService_UpdateDisplayName_Success(t *testing.T) {
	userID := uuid.New()
	createdAt := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	encryptedEmail := []byte("encrypted-email")
	normalizedDisplayName := "Анна  Мария"
	callOrder := make([]string, 0, 2)

	repo := &updateDisplayNameRepoMock{
		updateDisplayNameFn: func(_ context.Context, gotUserID uuid.UUID, gotDisplayName string) error {
			callOrder = append(callOrder, "update")
			assert.Equal(t, userID, gotUserID)
			assert.Equal(t, normalizedDisplayName, gotDisplayName)
			return nil
		},
		getUserProfileFn: func(_ context.Context, gotUserID uuid.UUID) (*UserProfile, error) {
			callOrder = append(callOrder, "get")
			assert.Equal(t, userID, gotUserID)
			return &UserProfile{
				ID:             userID,
				EncryptedEmail: encryptedEmail,
				DisplayName: sql.NullString{
					String: normalizedDisplayName,
					Valid:  true,
				},
				Role:          "defectologist",
				EmailVerified: true,
				CreatedAt:     createdAt,
			}, nil
		},
	}
	crypto := &updateDisplayNameCryptoMock{
		decryptFn: func(gotCiphertext []byte) ([]byte, error) {
			assert.Equal(t, encryptedEmail, gotCiphertext)
			return []byte("anna@example.com"), nil
		},
	}
	service := NewService(repo, nil, nil, crypto, nil, EmailConfig{})

	response, err := service.UpdateDisplayName(context.Background(), userID, "  Анна  Мария  ")

	require.NoError(t, err)
	require.NotNil(t, response)
	assert.Equal(t, []string{"update", "get"}, callOrder)
	assert.Equal(t, userID.String(), response.ID)
	assert.Equal(t, "anna@example.com", response.Email)
	require.NotNil(t, response.DisplayName)
	assert.Equal(t, normalizedDisplayName, *response.DisplayName)
	assert.Equal(t, "defectologist", response.Role)
	assert.True(t, response.EmailVerified)
	assert.Equal(t, createdAt, response.CreatedAt)
}

func TestService_UpdateDisplayName_Validation(t *testing.T) {
	tests := []struct {
		name        string
		displayName string
	}{
		{
			name:        "empty",
			displayName: "",
		},
		{
			name:        "spaces only",
			displayName: "   ",
		},
		{
			name:        "contains digits",
			displayName: "Анна1",
		},
		{
			name:        "contains punctuation",
			displayName: "Анна-Мария",
		},
		{
			name:        "longer than 100 characters",
			displayName: strings.Repeat("А", maxDisplayNameRunes+1),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repositoryCalled := false
			repo := &updateDisplayNameRepoMock{
				updateDisplayNameFn: func(_ context.Context, _ uuid.UUID, _ string) error {
					repositoryCalled = true
					return nil
				},
			}
			service := NewService(repo, nil, nil, nil, nil, EmailConfig{})

			response, err := service.UpdateDisplayName(context.Background(), uuid.New(), tt.displayName)

			assert.Nil(t, response)
			assertAppErrorStatus(t, err, http.StatusBadRequest)
			assert.False(t, repositoryCalled)
		})
	}
}

func TestService_UpdateDisplayName_AllowsMaximumLength(t *testing.T) {
	displayName := strings.Repeat("А", maxDisplayNameRunes)
	userID := uuid.New()
	repo := &updateDisplayNameRepoMock{
		updateDisplayNameFn: func(_ context.Context, gotUserID uuid.UUID, gotDisplayName string) error {
			assert.Equal(t, userID, gotUserID)
			assert.Equal(t, displayName, gotDisplayName)
			return nil
		},
		getUserProfileFn: func(_ context.Context, _ uuid.UUID) (*UserProfile, error) {
			return &UserProfile{
				ID:             userID,
				EncryptedEmail: []byte("encrypted-email"),
				DisplayName: sql.NullString{
					String: displayName,
					Valid:  true,
				},
			}, nil
		},
	}
	crypto := &updateDisplayNameCryptoMock{
		decryptFn: func(_ []byte) ([]byte, error) {
			return []byte("anna@example.com"), nil
		},
	}
	service := NewService(repo, nil, nil, crypto, nil, EmailConfig{})

	response, err := service.UpdateDisplayName(context.Background(), userID, displayName)

	require.NoError(t, err)
	require.NotNil(t, response)
	require.NotNil(t, response.DisplayName)
	assert.Equal(t, displayName, *response.DisplayName)
}

func TestService_UpdateDisplayName_ReturnsRepositoryError(t *testing.T) {
	expectedErr := errors.New("update display name")
	getProfileCalled := false
	repo := &updateDisplayNameRepoMock{
		updateDisplayNameFn: func(_ context.Context, _ uuid.UUID, _ string) error {
			return expectedErr
		},
		getUserProfileFn: func(_ context.Context, _ uuid.UUID) (*UserProfile, error) {
			getProfileCalled = true
			return nil, nil
		},
	}
	service := NewService(repo, nil, nil, nil, nil, EmailConfig{})

	response, err := service.UpdateDisplayName(context.Background(), uuid.New(), "Анна")

	assert.Nil(t, response)
	assert.ErrorIs(t, err, expectedErr)
	assert.False(t, getProfileCalled)
}
