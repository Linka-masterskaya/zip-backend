package auth

import (
	"time"

	"github.com/google/uuid"
)

type CreateUserParams struct {
	ID             uuid.UUID
	OrganizationID *uuid.UUID
	Name           string
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
