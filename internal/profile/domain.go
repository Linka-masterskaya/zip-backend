package profile

import "github.com/google/uuid"

type UserPassword struct {
	ID       uuid.UUID
	Password string
}
