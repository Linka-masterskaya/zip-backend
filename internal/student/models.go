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
	LastLessonAt  *time.Time `json:"last_lesson_at"`
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
	LastLessonAt   *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
	DeletedAt      *time.Time
}

type CreateInput struct {
	Email  string `json:"email"`
	Name   string `json:"name"`
	Age    *int   `json:"age"`
	Status string `json:"status"`
}

type UpdateInput struct {
	Email        *string    `json:"email"`
	Name         *string    `json:"name"`
	Age          *int       `json:"age"`
	Status       *string    `json:"status"`
	LastLessonAt *time.Time `json:"last_lesson_at"`
}

type storedUpdate struct {
	EmailEncrypted []byte
	EmailSet       bool
	Name           *string
	Age            *int
	Status         *string
	LastLessonAt   *time.Time
	LastLessonSet  bool
}

type ListInput struct {
	SortBy string
	Order  string
	Limit  int
	Offset int
}

type ListResult struct {
	Items []Student `json:"items"`
	Total int       `json:"total"`
}
