package folder

import (
	"context"
	"errors"
	"strings"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/authctx"
	"github.com/Linka-masterskaya/zip-backend/internal/packfilter"
	"github.com/google/uuid"
)

type folderRepository interface {
	Create(context.Context, uuid.UUID, string, CreateInput) (*Folder, error)
	List(context.Context, uuid.UUID, ListInput) ([]Folder, error)
	Rename(context.Context, uuid.UUID, string, uuid.UUID, string) (*Folder, error)
	Move(context.Context, uuid.UUID, string, uuid.UUID, *uuid.UUID) (*Folder, error)
	Delete(context.Context, uuid.UUID, string, uuid.UUID) error
	Contents(context.Context, uuid.UUID, ContentsInput) (*ContentsPage, error)
}

type Service struct {
	repo folderRepository
}

func NewService(repo folderRepository) *Service {
	return &Service{repo: repo}
}

func (s *Service) Create(ctx context.Context, input CreateInput) (*Folder, error) {
	userID, role, err := actor(ctx)
	if err != nil {
		return nil, err
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || !validSection(input.Section) || !validKind(input.Kind) {
		return nil, apperr.ErrBadRequest
	}
	if input.Kind == KindStudent && (input.Section != SectionStudents || input.StudentID == nil) {
		return nil, apperr.ErrBadRequest
	}
	if input.Kind == KindFolder && input.StudentID != nil {
		return nil, apperr.ErrBadRequest
	}
	if input.Section == SectionLibrary && !canWriteLibrary(role) {
		return nil, apperr.ErrForbidden
	}
	result, err := s.repo.Create(ctx, userID, role, input)
	return result, mapError(err)
}

func (s *Service) List(ctx context.Context, input ListInput) ([]Folder, error) {
	userID, _, err := actor(ctx)
	if err != nil {
		return nil, err
	}
	if !validSection(input.Section) {
		return nil, apperr.ErrBadRequest.WithMessage("section is required")
	}
	input.Limit, input.Offset, err = page(input.Limit, input.Offset)
	if err != nil {
		return nil, err
	}
	result, err := s.repo.List(ctx, userID, input)
	return result, mapError(err)
}

func (s *Service) Rename(ctx context.Context, folderID uuid.UUID, name string) (*Folder, error) {
	userID, role, err := actor(ctx)
	if err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, apperr.ErrBadRequest.WithMessage("name is required")
	}
	result, err := s.repo.Rename(ctx, userID, role, folderID, name)
	return result, mapError(err)
}

func (s *Service) Move(
	ctx context.Context,
	folderID uuid.UUID,
	parentID *uuid.UUID,
) (*Folder, error) {
	userID, role, err := actor(ctx)
	if err != nil {
		return nil, err
	}
	result, err := s.repo.Move(ctx, userID, role, folderID, parentID)
	return result, mapError(err)
}

func (s *Service) Delete(ctx context.Context, folderID uuid.UUID) error {
	userID, role, err := actor(ctx)
	if err != nil {
		return err
	}
	return mapError(s.repo.Delete(ctx, userID, role, folderID))
}

func (s *Service) Contents(ctx context.Context, input ContentsInput) (*ContentsPage, error) {
	userID, _, err := actor(ctx)
	if err != nil {
		return nil, err
	}
	if !validSection(input.Section) {
		return nil, apperr.ErrBadRequest.WithMessage("section is required")
	}
	input.Limit, input.Offset, err = page(input.Limit, input.Offset)
	if err != nil {
		return nil, err
	}
	if input.Sort == "" {
		input.Sort = "name"
	}
	if input.Order == "" {
		input.Order = "asc"
	}
	if (input.Sort != "name" && input.Sort != "updated_at") ||
		(input.Order != "asc" && input.Order != "desc") {
		return nil, apperr.ErrBadRequest.WithMessage("invalid sort or order")
	}
	if err = validateContentsFilters(&input); err != nil {
		return nil, err
	}
	result, err := s.repo.Contents(ctx, userID, input)
	return result, mapError(err)
}

func actor(ctx context.Context) (uuid.UUID, string, error) {
	userID, err := authctx.UserIDFromCtx(ctx)
	if err != nil {
		return uuid.Nil, "", err
	}
	role, err := authctx.RoleFromCtx(ctx)
	if err != nil {
		return uuid.Nil, "", err
	}
	return userID, role, nil
}

func page(limit, offset int) (int, int, error) {
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 100 || offset < 0 {
		return 0, 0, apperr.ErrBadRequest.WithMessage("invalid pagination")
	}
	return limit, offset, nil
}

// validateContentsFilters приводит фильтры к виду, понятному запросу, и
// отбивает значения, которых в базе быть не может.
func validateContentsFilters(input *ContentsInput) error {
	input.Query = strings.TrimSpace(input.Query)
	input.Type = strings.TrimSpace(input.Type)
	input.Difficulty = strings.TrimSpace(input.Difficulty)

	if input.Type != "" && input.Type != "folder" && input.Type != "pack" {
		return apperr.ErrBadRequest.WithMessage("type must be folder or pack")
	}
	if err := packfilter.ValidateAge(input.Age); err != nil {
		return err
	}
	return packfilter.ValidateDifficulty(input.Difficulty)
}

func validSection(value string) bool {
	return value == SectionLibrary || value == SectionMy || value == SectionStudents
}

func validKind(value string) bool {
	return value == KindFolder || value == KindStudent
}

func canWriteLibrary(role string) bool {
	return role == "defectologist" || role == "head_defectologist" || role == "admin"
}

func mapError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrStudentInvalid):
		return apperr.ErrNotFound
	case errors.Is(err, ErrParentInvalid), errors.Is(err, ErrDepth):
		return apperr.ErrBadRequest
	case errors.Is(err, ErrCycle), errors.Is(err, ErrNotEmpty):
		return apperr.ErrConflict
	default:
		return err
	}
}
