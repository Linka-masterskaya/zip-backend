package auth

import (
	"context"
	"testing"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// TestLoginWithYandex_ExistingIdentity: вход через Яндекс выдаёт такую же
// сессию, как обычный логин. Версия сессии в токене — та, что вернул кэш при
// сохранении refresh: без неё middleware отдаёт 401 после любого отзыва.
func TestLoginWithYandex_ExistingIdentity(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockauthRepoIface(ctrl)

	repo.EXPECT().
		GetUserByIdentity(gomock.Any(), "yandex", "yandex-uid").
		Return(&User{
			ID:            "user-id",
			OrgID:         ptrString("org-id"),
			Role:          "defectologist",
			EmailVerified: true,
		}, nil)

	cacheStore := &fakeCache{sessionVersion: 7}
	svc := NewAuthService(
		repo,
		cacheStore,
		&fakeRateLimiter{allowed: true},
		nil,
		testAuthConfig(),
		&fakeCrypto{hash: []byte("email-hash")},
	)

	result, err := svc.LoginWithYandex(context.Background(), YandexProfile{
		ID:    "yandex-uid",
		Email: "user@example.com",
		Name:  "Аня",
	})
	require.NoError(t, err)
	require.NotEmpty(t, result.RefreshToken)

	parsed, err := jwt.ParseWithClaims(
		result.AccessToken,
		&AccessClaims{},
		func(*jwt.Token) (any, error) { return []byte(testAuthConfig().JWTSecret), nil },
	)
	require.NoError(t, err)
	claims, ok := parsed.Claims.(*AccessClaims)
	require.True(t, ok)
	require.Equal(t, int64(7), claims.SessionVersion)

	require.True(t, cacheStore.called, "refresh не сохранён")
	require.Equal(t, "user-id", cacheStore.rec.UserID)
}

// TestLoginWithYandex_CreatesUser: незнакомый Яндекс-аккаунт со свободным
// email заводит пользователя, его учётку и связку с провайдером одной
// транзакцией, после чего выдаётся обычная сессия.
func TestLoginWithYandex_CreatesUser(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockauthRepoIface(ctrl)
	txRepo := NewMockauthRepoIface(ctrl)
	tx := &rollbackSpyTx{}

	repo.EXPECT().
		GetUserByIdentity(gomock.Any(), "yandex", "yandex-uid").
		Return(nil, apperr.ErrUserNotFound)
	repo.EXPECT().
		GetUserByEmailHashForRegistration(gomock.Any(), []byte("email-hash")).
		Return(nil, apperr.ErrUserNotFound)
	repo.EXPECT().beginTx(gomock.Any()).Return(tx, nil)
	repo.EXPECT().withTx(tx).Return(txRepo)

	txRepo.EXPECT().CreateOrganization(gomock.Any(), gomock.Any()).Return(nil)

	var createdUserID uuid.UUID
	txRepo.EXPECT().
		CreateUser(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, params CreateUserParams) error {
			createdUserID = params.ID
			require.Equal(t, "Аня", params.Name)
			return nil
		})
	txRepo.EXPECT().
		CreateAuthCred(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, params CreateAuthCredParams) error {
			require.Equal(t, createdUserID, params.UserID)
			require.Empty(t, params.PasswordHash, "у аккаунта из Яндекса пароля нет")
			require.Equal(t, RoleDefectologist, params.Role)
			return nil
		})
	txRepo.EXPECT().
		CreateIdentity(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, params CreateIdentityParams) error {
			require.Equal(t, createdUserID, params.UserID)
			require.Equal(t, "yandex", params.Provider)
			require.Equal(t, "yandex-uid", params.ProviderUID)
			return nil
		})

	repo.EXPECT().
		GetUserByID(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, id uuid.UUID) (*User, error) {
			require.Equal(t, createdUserID, id)
			return &User{ID: id.String(), Role: RoleDefectologist, EmailVerified: true}, nil
		})

	cacheStore := &fakeCache{sessionVersion: 3}
	svc := NewAuthService(
		repo,
		cacheStore,
		&fakeRateLimiter{allowed: true},
		nil,
		testAuthConfig(),
		&fakeCrypto{hash: []byte("email-hash")},
	)

	result, err := svc.LoginWithYandex(context.Background(), YandexProfile{
		ID:    "yandex-uid",
		Email: "new@example.com",
		Name:  "Аня",
	})
	require.NoError(t, err)
	require.NotEmpty(t, result.AccessToken)
	require.Equal(t, 1, tx.commitCalls, "транзакция должна быть зафиксирована")
}

// TestLoginWithYandex_EmailAlreadyRegistered: на этот email уже есть
// локальный аккаунт. Привязать к нему Яндекс молча нельзя — это захват
// чужого аккаунта через заведение почты на стороне провайдера.
func TestLoginWithYandex_EmailAlreadyRegistered(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockauthRepoIface(ctrl)

	repo.EXPECT().
		GetUserByIdentity(gomock.Any(), "yandex", "yandex-uid").
		Return(nil, apperr.ErrUserNotFound)
	repo.EXPECT().
		GetUserByEmailHashForRegistration(gomock.Any(), []byte("email-hash")).
		Return(&User{ID: "existing-user", Role: RoleDefectologist}, nil)

	cacheStore := &fakeCache{sessionVersion: 1}
	svc := NewAuthService(
		repo,
		cacheStore,
		&fakeRateLimiter{allowed: true},
		nil,
		testAuthConfig(),
		&fakeCrypto{hash: []byte("email-hash")},
	)

	_, err := svc.LoginWithYandex(context.Background(), YandexProfile{
		ID:    "yandex-uid",
		Email: "taken@example.com",
		Name:  "Аня",
	})
	require.ErrorIs(t, err, ErrEmailAlreadyRegistered)
	require.False(t, cacheStore.called, "сессия не должна выдаваться")
}
