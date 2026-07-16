package pack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/authctx"
	"github.com/Linka-masterskaya/zip-backend/internal/broker"
	"github.com/google/uuid"
)

type packRepository interface {
	Create(context.Context, uuid.UUID, CreateInput) (*Pack, error)
	Get(context.Context, uuid.UUID, uuid.UUID) (*Pack, error)
	List(context.Context, uuid.UUID, uuid.UUID) ([]*Pack, error)
	Update(context.Context, uuid.UUID, uuid.UUID, UpdateInput) (*Pack, error)
	Delete(context.Context, uuid.UUID, uuid.UUID) error
	Move(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (*Pack, error)
}

// Service contains pack business logic.
type Service struct {
	repo packRepository
}

// NewService creates a pack service.
func NewService(repo packRepository, _ *broker.Publisher) *Service {
	return &Service{repo: repo}
}

// Create creates a pack with an empty valid Linka 2.0 config.
func (s *Service) Create(ctx context.Context, title string, folderID uuid.UUID) (*Pack, error) {
	userID, err := authctx.UserIDFromCtx(ctx)
	if err != nil {
		return nil, err
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, apperr.ErrBadRequest.WithMessage("pack title is required")
	}

	config, err := emptyLinkaConfig(ctx)
	if err != nil {
		return nil, err
	}
	result, err := s.repo.Create(ctx, userID, CreateInput{Title: title, FolderID: folderID, Config: config})
	return result, packError(err)
}

// Get returns an accessible pack.
func (s *Service) Get(ctx context.Context, packID uuid.UUID) (*Pack, error) {
	userID, err := authctx.UserIDFromCtx(ctx)
	if err != nil {
		return nil, err
	}
	result, err := s.repo.Get(ctx, userID, packID)
	return result, packError(err)
}

// List returns packs from an accessible folder.
func (s *Service) List(ctx context.Context, folderID uuid.UUID) ([]*Pack, error) {
	userID, err := authctx.UserIDFromCtx(ctx)
	if err != nil {
		return nil, err
	}
	result, err := s.repo.List(ctx, userID, folderID)
	return result, packError(err)
}

// Update updates editable metadata and never changes config.
func (s *Service) Update(ctx context.Context, packID uuid.UUID, input UpdateInput) (*Pack, error) {
	userID, err := authctx.UserIDFromCtx(ctx)
	if err != nil {
		return nil, err
	}
	if err = validateUpdate(&input); err != nil {
		return nil, err
	}
	result, err := s.repo.Update(ctx, userID, packID, input)
	return result, packError(err)
}

// Delete deletes an accessible pack.
func (s *Service) Delete(ctx context.Context, packID uuid.UUID) error {
	userID, err := authctx.UserIDFromCtx(ctx)
	if err != nil {
		return err
	}
	return packError(s.repo.Delete(ctx, userID, packID))
}

// Move moves a pack to an accessible folder.
func (s *Service) Move(ctx context.Context, packID, folderID uuid.UUID) (*Pack, error) {
	userID, err := authctx.UserIDFromCtx(ctx)
	if err != nil {
		return nil, err
	}
	result, err := s.repo.Move(ctx, userID, packID, folderID)
	return result, packError(err)
}

func emptyLinkaConfig(ctx context.Context) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	type metadata struct {
		Version string `json:"version"`
	}
	type settings struct {
		Columns int `json:"columns"`
		Rows    int `json:"rows"`
	}
	config := struct {
		Metadata metadata          `json:"metadata"`
		Settings settings          `json:"settings"`
		Blocks   []json.RawMessage `json:"blocks"`
	}{
		Metadata: metadata{Version: "2.0"},
		Settings: settings{Columns: 1, Rows: 1},
		Blocks:   make([]json.RawMessage, 0),
	}
	data, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("marshal empty Linka config: %w", err)
	}
	return data, nil
}

func validateUpdate(input *UpdateInput) error {
	if input.Title == nil && input.FolderID == nil && input.FilterMetadata == nil && !input.Notes.Set {
		return apperr.ErrBadRequest.WithMessage("patch must contain editable fields")
	}
	if input.Title != nil {
		trimmed := strings.TrimSpace(*input.Title)
		if trimmed == "" {
			return apperr.ErrBadRequest.WithMessage("pack title must not be empty")
		}
		input.Title = &trimmed
	}
	if input.FilterMetadata != nil {
		return validateFilterMetadata(input.FilterMetadata)
	}
	return nil
}

func validateFilterMetadata(metadata *FilterMetadataPatch) error {
	if metadata.AgeMin.Set && metadata.AgeMin.Value != nil && *metadata.AgeMin.Value < 0 {
		return apperr.ErrBadRequest.WithMessage("age_min must not be negative")
	}
	if metadata.AgeMax.Set && metadata.AgeMax.Value != nil && *metadata.AgeMax.Value < 0 {
		return apperr.ErrBadRequest.WithMessage("age_max must not be negative")
	}
	if invalidAgeRange(metadata) {
		return apperr.ErrBadRequest.WithMessage("age_min must not exceed age_max")
	}
	if metadata.Difficulty.Set && metadata.Difficulty.Value != nil {
		difficulty := *metadata.Difficulty.Value
		if difficulty != "easy" && difficulty != "medium" && difficulty != "hard" {
			return apperr.ErrBadRequest.WithMessage("difficulty must be easy, medium, or hard")
		}
	}
	return nil
}

func invalidAgeRange(metadata *FilterMetadataPatch) bool {
	return metadata.AgeMin.Set && metadata.AgeMin.Value != nil &&
		metadata.AgeMax.Set && metadata.AgeMax.Value != nil &&
		*metadata.AgeMin.Value > *metadata.AgeMax.Value
}

func packError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrPackNotFound) {
		return apperr.ErrNotFound
	}
	if errors.Is(err, ErrFolderNotAllowed) {
		return apperr.ErrForbidden.WithMessage("folder is not accessible")
	}
	if errors.Is(err, ErrInvalidPackMetadata) {
		return apperr.ErrBadRequest.WithMessage("invalid pack metadata")
	}
	return err
}
