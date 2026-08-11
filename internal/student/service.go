package student

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/authctx"
	"github.com/google/uuid"
)

type repository interface {
	Create(context.Context, uuid.UUID, []byte, CreateInput) (*storedStudent, error)
	List(context.Context, uuid.UUID, ListInput) ([]storedStudent, int, error)
	Update(context.Context, uuid.UUID, uuid.UUID, storedUpdate) (*storedStudent, error)
	Delete(context.Context, uuid.UUID, uuid.UUID) error
}

type crypto interface {
	Encrypt([]byte) ([]byte, error)
	Decrypt([]byte) ([]byte, error)
}

type Service struct {
	repo   repository
	crypto crypto
}

func NewService(repo repository, crypto crypto) *Service {
	return &Service{repo: repo, crypto: crypto}
}

func (s *Service) Create(ctx context.Context, input CreateInput) (*Student, error) {
	ownerID, err := owner(ctx)
	if err != nil {
		return nil, err
	}
	normalizeCreate(&input)
	if err = validate(input.Email, input.Name, input.Age, input.Status); err != nil {
		return nil, err
	}
	encrypted, err := s.crypto.Encrypt([]byte(input.Email))
	if err != nil {
		return nil, fmt.Errorf("student encrypt email: %w", err)
	}
	stored, err := s.repo.Create(ctx, ownerID, encrypted, input)
	if err != nil {
		return nil, mapStudentError(err)
	}
	return s.decode(stored)
}

func (s *Service) List(ctx context.Context, input ListInput) (*ListResult, error) {
	ownerID, err := owner(ctx)
	if err != nil {
		return nil, err
	}

	if input.Limit <= 0 {
		input.Limit = 50
	}
	if input.Limit > 100 {
		input.Limit = 100
	}
	if input.Offset < 0 {
		input.Offset = 0
	}
	if !validSortBy(input.SortBy) {
		return nil, apperr.ErrBadRequest.WithMessage("invalid sort_by. allowed: name, age, status, last_lesson_at")
	}
	if input.Order != "" && strings.ToLower(input.Order) != "asc" && strings.ToLower(input.Order) != "desc" {
		return nil, apperr.ErrBadRequest.WithMessage("invalid order. allowed: asc, desc")
	}

	stored, total, err := s.repo.List(ctx, ownerID, input)
	if err != nil {
		return nil, err
	}

	result := make([]Student, 0, len(stored))
	for index := range stored {
		item, decodeErr := s.decode(&stored[index])
		if decodeErr != nil {
			return nil, decodeErr
		}
		result = append(result, *item)
	}
	return &ListResult{Items: result, Total: total}, nil
}

func (s *Service) Update(
	ctx context.Context,
	studentID uuid.UUID,
	input UpdateInput,
) (*Student, error) {
	ownerID, err := owner(ctx)
	if err != nil {
		return nil, err
	}
	if input.Email == nil && input.Name == nil && input.Age == nil && input.Status == nil {
		return nil, apperr.ErrBadRequest
	}
	storedInput, err := s.prepareUpdate(input)
	if err != nil {
		return nil, err
	}

	stored, err := s.repo.Update(ctx, ownerID, studentID, storedInput)
	if err != nil {
		return nil, mapStudentError(err)
	}
	return s.decode(stored)
}

func (s *Service) prepareUpdate(input UpdateInput) (storedUpdate, error) {
	result := storedUpdate{Name: input.Name, Age: input.Age, Status: input.Status, LastLessonAt: input.LastLessonAt}
	if input.Name != nil {
		value := strings.TrimSpace(*input.Name)
		input.Name = &value
		result.Name = &value
	}
	if input.Status != nil {
		value := strings.TrimSpace(*input.Status)
		input.Status = &value
		result.Status = &value
	}
	if input.Email != nil {
		value := strings.ToLower(strings.TrimSpace(*input.Email))
		if !validEmail(value) {
			return storedUpdate{}, apperr.ErrBadRequest.WithMessage("valid email is required")
		}
		encrypted, err := s.crypto.Encrypt([]byte(value))
		if err != nil {
			return storedUpdate{}, fmt.Errorf("student encrypt email: %w", err)
		}
		result.EmailEncrypted = encrypted
		result.EmailSet = true
	}
	if input.Name != nil && *input.Name == "" {
		return storedUpdate{}, apperr.ErrBadRequest
	}
	if input.Age != nil && (*input.Age < 0 || *input.Age > 100) {
		return storedUpdate{}, apperr.ErrBadRequest
	}
	if input.Status != nil && !validStatus(*input.Status) {
		return storedUpdate{}, apperr.ErrBadRequest
	}
	if input.LastLessonAt != nil {
		if input.LastLessonAt.After(time.Now()) {
			return storedUpdate{}, apperr.ErrBadRequest.WithMessage("last_lesson_at cannot be in the future")
		}
		result.LastLessonSet = true
	}
	return result, nil
}

func (s *Service) Delete(ctx context.Context, studentID uuid.UUID) error {
	ownerID, err := owner(ctx)
	if err != nil {
		return err
	}
	return mapStudentError(s.repo.Delete(ctx, ownerID, studentID))
}

func (s *Service) decode(stored *storedStudent) (*Student, error) {
	email, err := s.crypto.Decrypt(stored.EmailEncrypted)
	if err != nil {
		return nil, fmt.Errorf("student decrypt email: %w", err)
	}
	return &Student{
		ID: stored.ID, Email: string(email), EmailVerified: stored.EmailVerified,
		Name: stored.Name, Age: stored.Age, Status: stored.Status,
		CreatedAt: stored.CreatedAt, UpdatedAt: stored.UpdatedAt,
	}, nil
}

func owner(ctx context.Context) (uuid.UUID, error) {
	userID, err := authctx.UserIDFromCtx(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	role, err := authctx.RoleFromCtx(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	if role != "defectologist" && role != "head_defectologist" && role != "admin" {
		return uuid.Nil, apperr.ErrForbidden
	}
	return userID, nil
}

func normalizeCreate(input *CreateInput) {
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	input.Name = strings.TrimSpace(input.Name)
	input.Status = strings.TrimSpace(input.Status)
	if input.Status == "" {
		input.Status = "active"
	}
}

func validate(email, name string, age *int, status string) error {
	if !validEmail(email) || name == "" || !validStatus(status) {
		return apperr.ErrBadRequest
	}
	if age != nil && (*age < 0 || *age > 100) {
		return apperr.ErrBadRequest
	}
	return nil
}

func validEmail(value string) bool {
	address, err := mail.ParseAddress(value)
	return err == nil && address.Address == value && strings.Contains(value, "@")
}

func validStatus(value string) bool {
	return value == "active" || value == "paused" ||
		value == "archived" || value == "one_time"
}

func mapStudentError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrNotFound):
		return apperr.ErrNotFound
	case errors.Is(err, ErrHasFolder):
		return apperr.ErrConflict
	default:
		return err
	}
}

func validSortBy(field string) bool {
	switch field {
	case "", "name", "age", "status", "last_lesson_at":
		return true
	default:
		return false
	}
}
