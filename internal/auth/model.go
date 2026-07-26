package auth

import (
	"time"

	"github.com/google/uuid"
)

type CreateUserParams struct {
	ID             uuid.UUID
	OrganizationID *uuid.UUID
	Name           string
	EmailVerified  bool
}

type CreateAuthCredParams struct {
	UserID         uuid.UUID
	EmailHash      []byte
	EmailEncrypted []byte
	PasswordHash   string
	Role           string
}

type CreateVerifyTokenParams struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	TokenHash []byte
	ExpiresAt time.Time
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
