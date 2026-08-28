// Package folder implements the folder tree and mixed folder contents API.
package folder

import (
	"time"

	"github.com/google/uuid"
)

const (
	SectionLibrary  = "library"
	SectionMy       = "my"
	SectionStudents = "students"

	KindFolder  = "folder"
	KindStudent = "student"
)

type Folder struct {
	ID        uuid.UUID  `json:"id"`
	OrgID     uuid.UUID  `json:"org_id"`
	OwnerID   uuid.UUID  `json:"owner_id"`
	ParentID  *uuid.UUID `json:"parent_id"`
	Section   string     `json:"section"`
	Kind      string     `json:"kind"`
	StudentID *uuid.UUID `json:"student_id"`
	Name      string     `json:"name"`
	Depth     int        `json:"depth"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

type CreateInput struct {
	ParentID  *uuid.UUID
	Section   string
	Kind      string
	StudentID *uuid.UUID
	Name      string
}

type ListInput struct {
	Section  string
	ParentID *uuid.UUID
	Limit    int
	Offset   int
}

type ContentsInput struct {
	// Section is required. ParentID is nil for the root of the section:
	// "my", "library" and "students" are menu sections, not folders, so the
	// root has no folder id to address it by.
	Section  string
	ParentID *uuid.UUID
	Limit    int
	Offset   int
	Sort     string
	Order    string
	// Query ищет по названию папки или набора, Type оставляет только папки
	// или только наборы. Age и Difficulty — свойства наборов, поэтому сами
	// по себе отсекают папки.
	Query      string
	Type       string
	Age        *int
	Difficulty string
}

type ContentItem struct {
	Type      string     `json:"type"`
	ID        uuid.UUID  `json:"id"`
	Name      string     `json:"name"`
	Kind      *string    `json:"kind,omitempty"`
	StudentID *uuid.UUID `json:"student_id,omitempty"`
	Published bool       `json:"published,omitempty"`
	UpdatedAt time.Time  `json:"updated_at"`
}

type ContentsPage struct {
	Items  []ContentItem `json:"items"`
	Limit  int           `json:"limit"`
	Offset int           `json:"offset"`
	Total  int           `json:"total"`
}
