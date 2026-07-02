package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
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

func NewService(repo *Repository, jwtSecret string) *Service {
	return &Service{
		repo:      repo,
		jwtSecret: jwtSecret,
	}
}

func (s *Service) UpsertUser(ctx context.Context, email, name, yandexID string) (*User, *UserCred, error) {
	// 1. Ищем есть ли учетка по yandex_id
	// 2. Нашли → обновляем user (имя)
	// 3. Если не нашли по yandex_id — ищем по email (в UserCred)
	// 4. Нашли по email → привязываем identity к существующему пользователю
	// 5. Если ничего не нашли — создаём нового пользователя
	identity, err := s.repo.FindIdentityByProviderUID(ctx, "yandex", yandexID)
	if err != nil {
		return nil, nil, fmt.Errorf("find identity by yandex_id: %w", err)
	}

	if identity != nil {

		user, err := s.repo.FindUserByID(ctx, identity.UserID)
		if err != nil {
			return nil, nil, fmt.Errorf("find user by id: %w", err)
		}
		user.Name = name
		if err := s.repo.UpdateUser(ctx, user); err != nil {
			return nil, nil, fmt.Errorf("update user: %w", err)
		}

		cred, err := s.repo.FindUserAuthByUserID(ctx, user.ID)
		if err != nil {
			return nil, nil, fmt.Errorf("find userAuth by user_id: %w", err)
		}
		return user, cred, nil
	}

	cred, err := s.repo.FindUserAuthByEmail(ctx, email)
	if err != nil {
		return nil, nil, fmt.Errorf("find userAuth by email: %w", err)
	}

	if cred != nil {

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

		cred, err = s.repo.FindUserAuthByUserID(ctx, user.ID)
		if err != nil {
			return nil, nil, fmt.Errorf("find userAuth by user_id: %w", err)
		}
		return user, cred, nil
	}

	newUser := &User{
		ID:   uuid.New(),
		Name: name,
	}
	if err := s.repo.CreateUser(ctx, newUser); err != nil {
		return nil, nil, fmt.Errorf("create user: %w", err)
	}

	newCred := &UserCred{
		UserID:         newUser.ID,
		EmailEncrypted: []byte(email), // TODO: зашифровать
		EmailHash:      []byte(email), // TODO: захешировать
		Role:           "viewer",
	}
	if err := s.repo.CreateUserAuth(ctx, newCred); err != nil {
		return nil, nil, fmt.Errorf("create userAuth: %w", err)
	}

	newIdentity := &UserIdentity{
		ID:          uuid.New(),
		UserID:      newUser.ID,
		Provider:    "yandex",
		ProviderUID: yandexID,
	}
	if err := s.repo.CreateIdentity(ctx, newIdentity); err != nil {
		return nil, nil, fmt.Errorf("create identity: %w", err)
	}

	return newUser, newCred, nil
}
