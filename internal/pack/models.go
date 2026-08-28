// Package pack contains pack CRUD business logic, HTTP handlers, and persistence.
package pack

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Pack describes a persisted Linka pack.
type Pack struct {
	ID              uuid.UUID       `json:"id"`
	OrgID           uuid.UUID       `json:"org_id"`
	OwnerID         uuid.UUID       `json:"owner_id"`
	FolderID        uuid.UUID       `json:"folder_id"`
	LibraryFolderID *uuid.UUID      `json:"library_folder_id,omitempty"`
	PublishedAt     *time.Time      `json:"published_at,omitempty"`
	Title           string          `json:"title"`
	Status          string          `json:"status"`
	AgeMin          *int            `json:"age_min,omitempty"`
	AgeMax          *int            `json:"age_max,omitempty"`
	Difficulty      *string         `json:"difficulty,omitempty"`
	Goals           []string        `json:"goals"`
	Notes           string          `json:"notes"`
	Config          json.RawMessage `json:"config"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

// Adaptation is a snapshot of a pack assigned to one student.
type Adaptation struct {
	ID        uuid.UUID       `json:"id"`
	PackID    uuid.UUID       `json:"pack_id"`
	StudentID uuid.UUID       `json:"student_id"`
	Config    json.RawMessage `json:"config"`
	CreatedBy uuid.UUID       `json:"created_by"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// Version is an immutable snapshot of a pack configuration.
type Version struct {
	ID        uuid.UUID       `json:"id"`
	PackID    uuid.UUID       `json:"pack_id"`
	Version   int             `json:"version"`
	Config    json.RawMessage `json:"config"`
	CreatedBy uuid.UUID       `json:"created_by"`
	CreatedAt time.Time       `json:"created_at"`
}

// CreateInput contains fields accepted when a pack is created.
type CreateInput struct {
	Title    string
	FolderID uuid.UUID
	Config   json.RawMessage
}

// DuplicateInput contains optional destination settings for a pack copy.
type DuplicateInput struct {
	FolderID *uuid.UUID
}

// ListItem describes one pack placement returned by the global pack list.
type ListItem struct {
	ID              uuid.UUID       `json:"id"`
	OrgID           uuid.UUID       `json:"org_id"`
	OwnerID         uuid.UUID       `json:"owner_id"`
	FolderID        uuid.UUID       `json:"folder_id"`
	LibraryFolderID *uuid.UUID      `json:"library_folder_id,omitempty"`
	PublishedAt     *time.Time      `json:"published_at,omitempty"`
	Title           string          `json:"title"`
	Status          string          `json:"status"`
	AgeMin          *int            `json:"age_min,omitempty"`
	AgeMax          *int            `json:"age_max,omitempty"`
	Difficulty      *string         `json:"difficulty,omitempty"`
	Goals           []string        `json:"goals"`
	Notes           string          `json:"notes"`
	Config          json.RawMessage `json:"config"`
	IsFavorite      bool            `json:"is_favorite"`
	Section         string          `json:"section"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

type ListPage struct {
	Items  []*ListItem `json:"items"`
	Limit  int         `json:"limit"`
	Offset int         `json:"offset"`
	Total  int         `json:"total"`
}

// ListInput contains filters and offset pagination parameters for placement listing.
type ListInput struct {
	Query      string
	Age        *int
	Difficulty string
	Section    string
	Limit      int
	Offset     int
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
