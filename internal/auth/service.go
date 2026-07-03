package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Linka-masterskaya/zip-backend/pkg/linka/cryptox"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Service struct {
	repo      *Repository
	crypto    *cryptox.Crypto
	jwtSecret string
}

type User struct {
	ID        uuid.UUID  `json:"id"`
	Name      string     `json:"name"`
	AvatarKey *string    `json:"avatar_key,omitempty"`
	OrgID     *uuid.UUID `json:"org_id,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
}

type UserCred struct {
	UserID         uuid.UUID `json:"user_id"`
	EmailEncrypted []byte    `json:"-"`
	EmailHash      []byte    `json:"-"`
	PasswordHash   *string   `json:"-"`
	Role           string    `json:"role"`
}

type UserIdentity struct {
	ID          uuid.UUID `json:"id"`
	UserID      uuid.UUID `json:"user_id"`
	Provider    string    `json:"provider"`
	ProviderUID string    `json:"provider_uid"`
	CreatedAt   time.Time `json:"created_at"`
}

func NewService(repo *Repository, crypto *cryptox.Crypto, jwtSecret string) *Service {
	return &Service{
		repo:      repo,
		crypto:    crypto,
		jwtSecret: jwtSecret,
	}
}

func (s *Service) UpsertUser(ctx context.Context, email, name, yandexID string) (*User, *UserCred, error) {
	user, cred, err := s.handleExistingIdentity(ctx, name, yandexID)
	if err != nil {
		return nil, nil, err
	}
	if user != nil {
		return user, cred, nil
	}
	user, cred, err = s.handleExistingEmail(ctx, email, name, yandexID)
	if err != nil {
		return nil, nil, err
	}
	if user != nil {
		return user, cred, nil
	}
	return s.createNewUser(ctx, email, name, yandexID)
}

func (s *Service) handleExistingIdentity(ctx context.Context, name, yandexID string) (*User, *UserCred, error) {
	identity, err := s.repo.FindIdentityByProviderUID(ctx, "yandex", yandexID)
	if err != nil {
		return nil, nil, fmt.Errorf("find identity by yandex_id: %w", err)
	}
	if identity == nil {
		return nil, nil, nil
	}

	user, err := s.repo.FindUserByID(ctx, identity.UserID)
	if err != nil {
		return nil, nil, fmt.Errorf("find user by id: %w", err)
	}

	user.Name = name
	if err := s.repo.UpdateUser(ctx, user); err != nil {
		return nil, nil, fmt.Errorf("update user: %w", err)
	}

	cred, err := s.repo.FindUserCredByUserID(ctx, user.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("find userCred by user_id: %w", err)
	}

	return user, cred, nil
}

func (s *Service) handleExistingEmail(ctx context.Context, email, name, yandexID string) (*User, *UserCred, error) {
	emailHash := s.crypto.Hash([]byte(email))
	cred, err := s.repo.FindUserCredByEmailHash(ctx, emailHash)
	if err != nil {
		return nil, nil, fmt.Errorf("find userCred by email_hash: %w", err)
	}
	if cred == nil {
		return nil, nil, nil
	}

	user, err := s.repo.FindUserByID(ctx, cred.UserID)
	if err != nil {
		return nil, nil, fmt.Errorf("find user by id: %w", err)
	}

	user.Name = name
	if err := s.repo.UpdateUser(ctx, user); err != nil {
		return nil, nil, fmt.Errorf("update user: %w", err)
	}

	newIdentity := &UserIdentity{
		ID:          uuid.New(),
		UserID:      user.ID,
		Provider:    "yandex",
		ProviderUID: yandexID,
	}
	if err := s.repo.CreateIdentity(ctx, newIdentity); err != nil {
		return nil, nil, fmt.Errorf("create identity: %w", err)
	}

	cred, err = s.repo.FindUserCredByUserID(ctx, user.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("find userCred by user_id: %w", err)
	}

	return user, cred, nil
}

func (s *Service) createNewUser(ctx context.Context, email, name, yandexID string) (*User, *UserCred, error) {
	tx, err := s.repo.pool.Begin(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			slog.Warn("failed to rollback transaction", "error", err)
		}
	}()
	txRepo := s.repo.withTx(tx)

	userID := uuid.New()
	if err := txRepo.CreateUser(ctx, CreateUserParams{
		ID:             userID,
		OrganizationID: uuid.Nil,
		Name:           name,
	}); err != nil {
		return nil, nil, fmt.Errorf("create user: %w", err)
	}

	emailHash := s.crypto.Hash([]byte(email))
	emailEncrypted, err := s.crypto.Encrypt([]byte(email))
	if err != nil {
		return nil, nil, fmt.Errorf("encrypt email: %w", err)
	}

	if err := txRepo.CreateAuthCred(ctx, CreateAuthCredParams{
		UserID:         userID,
		EmailHash:      emailHash,
		EmailEncrypted: emailEncrypted,
		PasswordHash:   "",
		Role:           "viewer",
	}); err != nil {
		return nil, nil, fmt.Errorf("create authCred: %w", err)
	}

	if err := txRepo.CreateIdentity(ctx, &UserIdentity{
		ID:          uuid.New(),
		UserID:      userID,
		Provider:    "yandex",
		ProviderUID: yandexID,
	}); err != nil {
		return nil, nil, fmt.Errorf("create identity: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, nil, fmt.Errorf("commit tx: %w", err)
	}

	user, err := s.repo.FindUserByID(ctx, userID)
	if err != nil {
		return nil, nil, fmt.Errorf("find created user: %w", err)
	}

	cred, err := s.repo.FindUserCredByUserID(ctx, userID)
	if err != nil {
		return nil, nil, fmt.Errorf("find created userCred: %w", err)
	}

	return user, cred, nil
}
