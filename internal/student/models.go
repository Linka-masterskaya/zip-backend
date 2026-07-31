// Package student implements the defectologist's student registry.
package student

import (
	"time"

	"github.com/google/uuid"
)

type Student struct {
	ID            uuid.UUID  `json:"id"`
	Email         string     `json:"email"`
	EmailVerified bool       `json:"email_verified"`
	Name          string     `json:"name"`
	Age           *int       `json:"age"`
	Status        string     `json:"status"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	DeletedAt     *time.Time `json:"-"`
}

type storedStudent struct {
	ID             uuid.UUID
	EmailEncrypted []byte
	EmailVerified  bool
	Name           string
	Age            *int
	Status         string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	DeletedAt      *time.Time
}

type CreateInput struct {
	Email  string
	Name   string
	Age    *int
	Status string
}

type UpdateInput struct {
	Email  *string
	Name   *string
	Age    *int
	Status *string
}

type storedUpdate struct {
	EmailEncrypted []byte
	EmailSet       bool
	Name           *string
	Age            *int
	Status         *string
}
