// Package pack contains pack CRUD business logic, HTTP handlers, and persistence.
package pack

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Pack describes a persisted Linka pack.
type Pack struct {
	ID         uuid.UUID       `json:"id"`
	OrgID      uuid.UUID       `json:"org_id"`
	OwnerID    uuid.UUID       `json:"owner_id"`
	FolderID   uuid.UUID       `json:"folder_id"`
	Title      string          `json:"title"`
	Status     string          `json:"status"`
	AgeMin     *int            `json:"age_min,omitempty"`
	AgeMax     *int            `json:"age_max,omitempty"`
	Difficulty *string         `json:"difficulty,omitempty"`
	Goals      []string        `json:"goals"`
	Notes      *string         `json:"notes,omitempty"`
	Config     json.RawMessage `json:"config"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

// CreateInput contains fields accepted when a pack is created.
type CreateInput struct {
	Title    string
	FolderID uuid.UUID
	Config   json.RawMessage
}

// NullablePatch distinguishes an omitted PATCH field from an explicit null.
type NullablePatch[T any] struct {
	Set   bool
	Value *T
}

// FilterMetadataPatch contains optional list-filter metadata changes.
type FilterMetadataPatch struct {
	AgeMin     NullablePatch[int]
	AgeMax     NullablePatch[int]
	Difficulty NullablePatch[string]
	Goals      *[]string
}

// UpdateInput contains fields accepted by PATCH /packs/{id}.
type UpdateInput struct {
	Title          *string
	FolderID       *uuid.UUID
	FilterMetadata *FilterMetadataPatch
	Notes          NullablePatch[string]
}
