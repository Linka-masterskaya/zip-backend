package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/logger"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// YandexProfile — то, что провайдер рассказал о пользователе. Собирается в
// хендлере из ответа login.yandex.ru, сервис его уже не разбирает.
type YandexProfile struct {
	ID    string
	Email string
	Name  string
}

// LoginWithYandex пускает пользователя по связке provider + provider_uid, а
// незнакомому заводит аккаунт.
func (au *authService) LoginWithYandex(
	ctx context.Context,
	profile YandexProfile,
) (*LoginResult, error) {
	user, err := au.repo.GetUserByIdentity(ctx, providerYandex, profile.ID)
	switch {
	case err == nil:
		return au.issueSession(ctx, user)
	case !errors.Is(err, apperr.ErrUserNotFound):
		return nil, fmt.Errorf("get user by identity: %w", err)
	}

	if err = au.checkEmailFree(ctx, profile.Email); err != nil {
		return nil, err
	}

	user, err = au.createYandexUser(ctx, profile)
	if err != nil {
		return nil, err
	}
	return au.issueSession(ctx, user)
}

// checkEmailFree отклоняет вход, если на этот email уже заведён локальный
// аккаунт. Молча привязывать к нему чужую identity нельзя: тот, кто завёл
// почту в Яндексе, получил бы доступ к существующему аккаунту.
func (au *authService) checkEmailFree(ctx context.Context, email string) error {
	emailHash := au.crp.Hash([]byte(normalizeEmail(email)))
	_, err := au.repo.GetUserByEmailHashForRegistration(ctx, emailHash)
	switch {
	case errors.Is(err, apperr.ErrUserNotFound):
		return nil
	case err != nil:
		return fmt.Errorf("check email: %w", err)
	default:
		return ErrEmailAlreadyRegistered
	}
}

// createYandexUser заводит организацию, пользователя, его учётку без пароля
// и связку с провайдером одной транзакцией.
func (au *authService) createYandexUser(
	ctx context.Context,
	profile YandexProfile,
) (*User, error) {
	email := normalizeEmail(profile.Email)
	emailHash := au.crp.Hash([]byte(email))
	emailEncrypted, err := au.crp.Encrypt([]byte(email))
	if err != nil {
		return nil, fmt.Errorf("encrypt email: %w", err)
	}

	tx, err := au.repo.beginTx(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil &&
			!errors.Is(rollbackErr, pgx.ErrTxClosed) {
			slog.Error("tx rollback failed", logger.Err(rollbackErr))
		}
	}()
	txRepo := au.repo.withTx(tx)

	orgParams := CreateOrganizationParams{ID: uuid.New(), Name: "Personal organization"}
	if err = txRepo.CreateOrganization(ctx, orgParams); err != nil {
		return nil, fmt.Errorf("create organization: %w", err)
	}

	userParams := CreateUserParams{
		ID:             uuid.New(),
		Name:           profile.Name,
		OrganizationID: orgParams.ID,
	}
	if err = txRepo.CreateUser(ctx, userParams); err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}

	if err = txRepo.CreateAuthCred(ctx, CreateAuthCredParams{
		UserID:         userParams.ID,
		EmailHash:      emailHash,
		EmailEncrypted: emailEncrypted,
		Role:           RoleDefectologist,
	}); err != nil {
		return nil, fmt.Errorf("create auth cred: %w", err)
	}

	if err = txRepo.CreateIdentity(ctx, CreateIdentityParams{
		ID:          uuid.New(),
		UserID:      userParams.ID,
		Provider:    providerYandex,
		ProviderUID: profile.ID,
	}); err != nil {
		return nil, fmt.Errorf("create identity: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}

	created, err := au.repo.GetUserByID(ctx, userParams.ID)
	if err != nil {
		return nil, fmt.Errorf("get created user: %w", err)
	}
	return created, nil
}
