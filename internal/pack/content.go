package pack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/authctx"
	"github.com/Linka-masterskaya/zip-backend/internal/media"
	"github.com/Linka-masterskaya/zip-backend/pkg/linka"
	"github.com/google/uuid"
)

type contentRepository interface {
	SaveConfig(context.Context, uuid.UUID, uuid.UUID, json.RawMessage, []uuid.UUID) (*Pack, error)
	ArchiveData(context.Context, uuid.UUID, uuid.UUID) (*Pack, []*media.File, error)
	Assign(context.Context, uuid.UUID, uuid.UUID, []uuid.UUID) ([]Adaptation, error)
	Unassign(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error
}

type mediaUploader interface {
	Upload(context.Context, []byte) (*media.Response, error)
}

type ContentService struct {
	repo        contentRepository
	storage     archiveStorage
	uploader    mediaUploader
	packService *Service
}

func NewContentService(
	repo contentRepository,
	storage archiveStorage,
	uploader mediaUploader,
	packService *Service,
) *ContentService {
	return &ContentService{repo: repo, storage: storage, uploader: uploader, packService: packService}
}

func (s *ContentService) SaveConfig(
	ctx context.Context,
	packID uuid.UUID,
	config json.RawMessage,
) (*Pack, error) {
	userID, err := authctx.UserIDFromCtx(ctx)
	if err != nil {
		return nil, err
	}
	mediaIDs, err := validateAndMediaIDs(ctx, config, false)
	if err != nil {
		return nil, err
	}
	result, err := s.repo.SaveConfig(ctx, userID, packID, config, mediaIDs)
	return result, contentError(err)
}

func (s *ContentService) Assign(
	ctx context.Context,
	packID uuid.UUID,
	studentIDs []uuid.UUID,
) ([]Adaptation, error) {
	userID, err := authctx.UserIDFromCtx(ctx)
	if err != nil {
		return nil, err
	}
	studentIDs, err = uniqueIDs(studentIDs)
	if err != nil {
		return nil, err
	}
	result, err := s.repo.Assign(ctx, userID, packID, studentIDs)
	return result, contentError(err)
}

func (s *ContentService) Unassign(
	ctx context.Context,
	packID, studentID uuid.UUID,
) error {
	userID, err := authctx.UserIDFromCtx(ctx)
	if err != nil {
		return err
	}
	return contentError(s.repo.Unassign(ctx, userID, packID, studentID))
}

func (s *ContentService) Export(ctx context.Context, packID uuid.UUID) ([]byte, string, error) {
	userID, err := authctx.UserIDFromCtx(ctx)
	if err != nil {
		return nil, "", err
	}
	packData, files, err := s.repo.ArchiveData(ctx, userID, packID)
	if err != nil {
		return nil, "", contentError(err)
	}
	data, err := buildArchive(ctx, packData.Config, files, s.storage)
	if err != nil {
		return nil, "", contentError(err)
	}
	return data, safeArchiveName(packData.Title), nil
}

func (s *ContentService) Import(
	ctx context.Context,
	title string,
	folderID uuid.UUID,
	archive []byte,
) (*Pack, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, apperr.ErrBadRequest.WithMessage("pack title is required")
	}
	parsed, err := parseArchive(archive)
	if err != nil {
		return nil, contentError(err)
	}
	if _, err = validateAndMediaIDs(ctx, parsed.Config, true); err != nil {
		return nil, err
	}
	var cfg linka.Config
	if err = json.Unmarshal(parsed.Config, &cfg); err != nil {
		return nil, apperr.ErrBadRequest.WithMessage("archive config is invalid")
	}
	if err = s.uploadImportedMedia(ctx, &cfg, parsed.Files); err != nil {
		return nil, err
	}
	config, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("encode imported config: %w", err)
	}
	if _, err = validateAndMediaIDs(ctx, config, false); err != nil {
		return nil, err
	}
	created, err := s.packService.Create(ctx, title, folderID)
	if err != nil {
		return nil, err
	}
	result, err := s.SaveConfig(ctx, created.ID, config)
	if err != nil {
		s.removeIncompleteImport(ctx, created.ID)
		return nil, err
	}
	return result, nil
}

func (s *ContentService) uploadImportedMedia(
	ctx context.Context,
	cfg *linka.Config,
	files map[string][]byte,
) error {
	for blockIndex := range cfg.Blocks {
		for elementIndex := range cfg.Blocks[blockIndex].Elements {
			element := &cfg.Blocks[blockIndex].Elements[elementIndex]
			if element.Kind == linka.ElementKindText {
				continue
			}
			content, ok := files[element.MediaURL]
			if !ok || element.MediaURL == "" {
				return apperr.ErrBadRequest.WithMessage("archive media reference is missing")
			}
			uploaded, uploadErr := s.uploader.Upload(ctx, content)
			if uploadErr != nil {
				return uploadErr
			}
			mediaID := uploaded.ID
			element.MediaID = &mediaID
			element.MediaURL = ""
		}
	}
	return nil
}

func (s *ContentService) removeIncompleteImport(ctx context.Context, packID uuid.UUID) {
	if err := s.packService.Delete(ctx, packID); err != nil {
		slog.Warn("remove incomplete imported pack", "pack_id", packID, "err", err)
	}
}

func validateAndMediaIDs(ctx context.Context, config json.RawMessage, allowArchiveURL bool) ([]uuid.UUID, error) {
	if err := linka.ValidateConfig(ctx, config); err != nil {
		return nil, apperr.ErrBadRequest.WithMessage("invalid Linka 2.0 config")
	}
	var cfg linka.Config
	if err := json.Unmarshal(config, &cfg); err != nil {
		return nil, apperr.ErrBadRequest.WithMessage("invalid Linka 2.0 config")
	}
	seen := make(map[uuid.UUID]struct{})
	for _, block := range cfg.Blocks {
		for _, element := range block.Elements {
			if element.Kind == linka.ElementKindText {
				continue
			}
			if element.MediaID == nil || *element.MediaID == uuid.Nil {
				if allowArchiveURL && element.MediaURL != "" {
					continue
				}
				return nil, apperr.ErrBadRequest.WithMessage("image and audio elements require media_id")
			}
			seen[*element.MediaID] = struct{}{}
		}
	}
	ids := make([]uuid.UUID, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	return ids, nil
}

func safeArchiveName(title string) string {
	replacer := strings.NewReplacer("/", "_", "\\", "_", "\r", "", "\n", "")
	title = strings.TrimSpace(replacer.Replace(title))
	if title == "" {
		title = "pack"
	}
	return title + ".linka"
}

func uniqueIDs(ids []uuid.UUID) ([]uuid.UUID, error) {
	if len(ids) == 0 || len(ids) > 100 {
		return nil, apperr.ErrBadRequest.WithMessage("student_ids must contain between 1 and 100 items")
	}
	seen := make(map[uuid.UUID]struct{}, len(ids))
	result := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if id == uuid.Nil {
			return nil, apperr.ErrBadRequest.WithMessage("student_ids must contain valid UUIDs")
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result, nil
}

func contentError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrPackNotFound):
		return apperr.ErrNotFound
	case errors.Is(err, ErrFolderNotAllowed):
		return apperr.ErrForbidden.WithMessage("folder is not accessible")
	case errors.Is(err, ErrMediaNotAllowed):
		return apperr.ErrForbidden.WithMessage("media is not accessible")
	case errors.Is(err, ErrStudentNotAllowed):
		return apperr.ErrForbidden.WithMessage("student is not accessible")
	case errors.Is(err, ErrInvalidArchive):
		return apperr.ErrBadRequest.WithMessage(err.Error())
	case errors.Is(err, ErrArchiveTooLarge):
		return apperr.ErrPayloadTooLarge
	default:
		return err
	}
}
