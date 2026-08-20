package pack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/authctx"
	"github.com/Linka-masterskaya/zip-backend/internal/media"
	"github.com/Linka-masterskaya/zip-backend/pkg/linka"
	"github.com/google/uuid"
)

type adaptationArchiveData struct {
	Config json.RawMessage
	Title  string
}

type contentRepository interface {
	SaveConfig(context.Context, uuid.UUID, uuid.UUID, json.RawMessage, []uuid.UUID) (*Pack, error)
	ArchiveData(context.Context, uuid.UUID, uuid.UUID) (*Pack, []*media.File, error)
	AdaptationArchiveData(
		context.Context, uuid.UUID, uuid.UUID,
	) (*adaptationArchiveData, []*media.File, error)
	Assign(context.Context, uuid.UUID, uuid.UUID, []uuid.UUID) ([]Adaptation, error)
	Unassign(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error
	ListAdaptations(context.Context, uuid.UUID, uuid.UUID) ([]Adaptation, error)
	GetAdaptation(context.Context, uuid.UUID, uuid.UUID) (*Adaptation, error)
	UpdateAdaptationConfig(context.Context, uuid.UUID, uuid.UUID, json.RawMessage, []uuid.UUID) (*Adaptation, error)
	CreateVersion(context.Context, uuid.UUID, uuid.UUID) (*Version, error)
	ListVersions(context.Context, uuid.UUID, uuid.UUID, ListInput) ([]*VersionSummary, error)
	GetVersion(context.Context, uuid.UUID, uuid.UUID, int) (*Version, error)
	RestoreVersion(context.Context, uuid.UUID, uuid.UUID, int) (*RestoreResult, error)
}

func (s *ContentService) CreateVersion(ctx context.Context, packID uuid.UUID) (*Version, error) {
	userID, err := authctx.UserIDFromCtx(ctx)
	if err != nil {
		return nil, err
	}
	result, err := s.repo.CreateVersion(ctx, userID, packID)
	return result, contentError(err)
}

func (s *ContentService) ListVersions(
	ctx context.Context,
	packID uuid.UUID,
	input ListInput,
) ([]*VersionSummary, error) {
	userID, err := authctx.UserIDFromCtx(ctx)
	if err != nil {
		return nil, err
	}
	input, err = validateListInput(input)
	if err != nil {
		return nil, err
	}
	result, err := s.repo.ListVersions(ctx, userID, packID, input)
	return result, contentError(err)
}

func (s *ContentService) GetVersion(
	ctx context.Context,
	packID uuid.UUID,
	versionNumber int,
) (*Version, error) {
	if versionNumber < 1 {
		return nil, apperr.ErrBadRequest.WithMessage("version must be a positive integer")
	}
	userID, err := authctx.UserIDFromCtx(ctx)
	if err != nil {
		return nil, err
	}
	result, err := s.repo.GetVersion(ctx, userID, packID, versionNumber)
	return result, contentError(err)
}

func (s *ContentService) RestoreVersion(
	ctx context.Context,
	packID uuid.UUID,
	versionNumber int,
) (*RestoreResult, error) {
	if versionNumber < 1 {
		return nil, apperr.ErrBadRequest.WithMessage("version must be a positive integer")
	}
	userID, err := authctx.UserIDFromCtx(ctx)
	if err != nil {
		return nil, err
	}
	target, err := s.repo.GetVersion(ctx, userID, packID, versionNumber)
	if err != nil {
		return nil, contentError(err)
	}
	if _, err = validateAndMediaIDs(ctx, target.Config, false); err != nil {
		return nil, err
	}
	result, err := s.repo.RestoreVersion(ctx, userID, packID, versionNumber)
	return result, contentError(err)
}

type mediaUploader interface {
	Upload(context.Context, []byte, string) (*media.Response, error)
}

// PictureLoader resolves a Pictures Bank reference for a self-contained export.
// Resolved bytes are not persisted in local object storage. Missing pictures must return
// ErrMissingMediaReference so export can respond with HTTP 409.
type PictureLoader func(context.Context, uuid.UUID) ([]byte, string, error)

// ExportArchive is a bounded .linka stream prepared for an HTTP response.
type ExportArchive struct {
	Stream   io.ReadCloser
	Filename string
	Size     int64
}

type ContentService struct {
	repo        contentRepository
	storage     archiveStorage
	uploader    mediaUploader
	packService *Service
	pictures    PictureLoader
}

func NewContentService(
	repo contentRepository,
	storage archiveStorage,
	uploader mediaUploader,
	packService *Service,
	pictures ...PictureLoader,
) *ContentService {
	service := &ContentService{repo: repo, storage: storage, uploader: uploader, packService: packService}
	if len(pictures) > 0 {
		service.pictures = pictures[0]
	}
	return service
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

// ListAdaptations returns all student-specific snapshots for an owned pack.
func (s *ContentService) ListAdaptations(
	ctx context.Context,
	packID uuid.UUID,
) ([]Adaptation, error) {
	userID, err := authctx.UserIDFromCtx(ctx)
	if err != nil {
		return nil, err
	}
	result, err := s.repo.ListAdaptations(ctx, userID, packID)
	return result, contentError(err)
}

// GetAdaptation returns one student-specific snapshot accessible to the current user.
func (s *ContentService) GetAdaptation(
	ctx context.Context,
	adaptationID uuid.UUID,
) (*Adaptation, error) {
	userID, err := authctx.UserIDFromCtx(ctx)
	if err != nil {
		return nil, err
	}
	result, err := s.repo.GetAdaptation(ctx, userID, adaptationID)
	return result, contentError(err)
}

func (s *ContentService) UpdateAdaptationConfig(
	ctx context.Context,
	adaptationID uuid.UUID,
	config json.RawMessage,
) (*Adaptation, error) {
	userID, err := authctx.UserIDFromCtx(ctx)
	if err != nil {
		return nil, err
	}
	mediaIDs, err := validateAndMediaIDs(ctx, config, false)
	if err != nil {
		return nil, err
	}
	result, err := s.repo.UpdateAdaptationConfig(ctx, userID, adaptationID, config, mediaIDs)
	return result, contentError(err)
}

// ExportFormat выбирает формат config.json внутри .linka.
//
// Linka Looks 3.2.10 не читает Linka Config 2.0: он молча
// нормализует чужой config в одну пустую страницу, поэтому
// конвертация обязана запрашиваться явно.
// См. docs/compatibility/linka-looks/ADR-001-linka-looks-3.2.10.md
type ExportFormat string

const (
	// ExportFormatLinka2 — родной формат бэкенда, значение по умолчанию.
	ExportFormatLinka2 ExportFormat = "linka-2"
	// ExportFormatLooks3 — формат набора Linka Looks 3.0.
	ExportFormatLooks3 ExportFormat = "looks-3"
)

// ParseExportFormat разбирает значение query-параметра format.
// Пустая строка означает формат по умолчанию.
func ParseExportFormat(raw string) (ExportFormat, error) {
	switch ExportFormat(raw) {
	case "":
		return ExportFormatLinka2, nil
	case ExportFormatLinka2:
		return ExportFormatLinka2, nil
	case ExportFormatLooks3:
		return ExportFormatLooks3, nil
	default:
		return "", apperr.ErrBadRequest.WithMessage(
			`format must be "linka-2" or "looks-3"`,
		)
	}
}

func (s *ContentService) Export(
	ctx context.Context,
	packID uuid.UUID,
	format ExportFormat,
) (*ExportArchive, error) {
	userID, err := authctx.UserIDFromCtx(ctx)
	if err != nil {
		return nil, err
	}
	packData, files, err := s.repo.ArchiveData(ctx, userID, packID)
	if err != nil {
		return nil, contentError(err)
	}
	return s.exportConfig(ctx, packData.Config, packData.Title, files, format)
}

func (s *ContentService) ExportAdaptation(
	ctx context.Context,
	adaptationID uuid.UUID,
	format ExportFormat,
) (*ExportArchive, error) {
	userID, err := authctx.UserIDFromCtx(ctx)
	if err != nil {
		return nil, err
	}
	data, files, err := s.repo.AdaptationArchiveData(ctx, userID, adaptationID)
	if err != nil {
		return nil, contentError(err)
	}
	return s.exportConfig(ctx, data.Config, data.Title+"-adaptation", files, format)
}

func (s *ContentService) exportConfig(
	ctx context.Context,
	config json.RawMessage,
	title string,
	files []*media.File,
	format ExportFormat,
) (*ExportArchive, error) {
	if _, err := validateAndMediaIDs(ctx, config, false); err != nil {
		return nil, err
	}
	stream, err := buildArchive(ctx, config, files, s.storage, format, s.pictures)
	if err != nil {
		return nil, contentError(err)
	}
	return &ExportArchive{
		Stream: stream, Filename: safeArchiveName(title), Size: stream.size,
	}, nil
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
			if element.Kind == linka.ElementKindImage && element.SourcePictureID != nil {
				element.MediaID = nil
				element.MediaURL = ""
				continue
			}
			content, ok := files[element.MediaURL]
			if !ok || element.MediaURL == "" {
				return apperr.ErrBadRequest.WithMessage("archive media reference is missing")
			}
			uploaded, uploadErr := s.uploader.Upload(ctx, content, filepath.Base(element.MediaURL))
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
				if element.Kind == linka.ElementKindImage && element.SourcePictureID != nil &&
					*element.SourcePictureID != uuid.Nil {
					continue
				}
				if allowArchiveURL && element.MediaURL != "" {
					continue
				}
				return nil, apperr.ErrBadRequest.WithMessage(
					"image elements require media_id or source_picture_id; audio elements require media_id",
				)
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
	title = strings.Map(func(symbol rune) rune {
		if symbol == '/' || symbol == '\\' {
			return '_'
		}
		if unicode.IsControl(symbol) {
			return -1
		}
		return symbol
	}, title)
	title = strings.TrimSpace(title)
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
	case errors.Is(err, ErrVersionNotFound):
		return apperr.ErrNotFound.WithMessage("pack version not found")
	case errors.Is(err, ErrAdaptationNotFound):
		return apperr.ErrNotFound.WithMessage("pack adaptation not found")
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
	case errors.Is(err, ErrMissingMediaReference):
		return apperr.ErrConflict.WithMessage("archive media reference is missing")
	case errors.Is(err, linka.ErrLooksUnsupportedBlock),
		errors.Is(err, linka.ErrLooksUnrepresentableMatching),
		errors.Is(err, linka.ErrLooksMissingMediaPath):
		// Набор валиден, но не выражается в формате Linka Looks:
		// это конфликт состояния набора с запрошенным форматом.
		return apperr.ErrConflict.WithMessage(err.Error())
	default:
		return err
	}
}
