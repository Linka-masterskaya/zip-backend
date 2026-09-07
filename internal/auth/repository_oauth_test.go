package auth

import (
	"testing"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// TestGetUserByIdentity: связка provider + provider_uid находит пользователя
// вместе с его ролью, а удалённый аккаунт по ней больше не находится.
func TestGetUserByIdentity(t *testing.T) {
	truncateAll(t)
	ctx := testCtx(t)
	repo := NewAuthRepo(testPool)

	userID := seedUser(t, testPool)
	seedOAuthCred(t, userID, []byte("hash-1"))
	require.NoError(t, repo.CreateIdentity(ctx, CreateIdentityParams{
		ID:          uuid.New(),
		UserID:      userID,
		Provider:    providerYandex,
		ProviderUID: "yandex-1",
	}))

	user, err := repo.GetUserByIdentity(ctx, providerYandex, "yandex-1")
	require.NoError(t, err)
	require.Equal(t, userID.String(), user.ID)
	require.Equal(t, RoleDefectologist, user.Role)
	require.Nil(t, user.PasswordHash, "у аккаунта из Яндекса пароля нет")

	_, err = repo.GetUserByIdentity(ctx, providerYandex, "unknown")
	require.ErrorIs(t, err, apperr.ErrUserNotFound)

	_, err = testPool.Exec(ctx, `UPDATE users SET deleted_at = now() WHERE id = $1`, userID)
	require.NoError(t, err)
	_, err = repo.GetUserByIdentity(ctx, providerYandex, "yandex-1")
	require.ErrorIs(t, err, apperr.ErrUserNotFound, "удалённый аккаунт не находится")
}

// TestCreateIdentityIsUniquePerProvider: один аккаунт провайдера нельзя
// привязать к двум пользователям — иначе вход отдавал бы разные учётки.
func TestCreateIdentityIsUniquePerProvider(t *testing.T) {
	truncateAll(t)
	ctx := testCtx(t)
	repo := NewAuthRepo(testPool)

	first := seedUser(t, testPool)
	seedOAuthCred(t, first, []byte("hash-1"))
	second := seedUser(t, testPool)
	seedOAuthCred(t, second, []byte("hash-2"))

	require.NoError(t, repo.CreateIdentity(ctx, CreateIdentityParams{
		ID: uuid.New(), UserID: first, Provider: providerYandex, ProviderUID: "yandex-1",
	}))

	err := repo.CreateIdentity(ctx, CreateIdentityParams{
		ID: uuid.New(), UserID: second, Provider: providerYandex, ProviderUID: "yandex-1",
	})
	require.Error(t, err)
}

// seedOAuthCred отличается от seedAuthCred тем, что кладёт разные
// email_hash: на колонке уникальный индекс, а тесту нужны два аккаунта.
func seedOAuthCred(t *testing.T, userID uuid.UUID, emailHash []byte) {
	t.Helper()
	_, err := testPool.Exec(testCtx(t), `
		INSERT INTO auth_cred (user_id, email_hash, email_encrypted, password_hash, role)
		VALUES ($1, $2, '\x00', NULL, 'defectologist')`, userID, emailHash)
	require.NoError(t, err)
}
