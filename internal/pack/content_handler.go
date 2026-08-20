package pack

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/google/uuid"
)

const (
	archiveMultipartOverhead = int64(128 * 1024)
	maxConfigSize            = int64(5 * 1024 * 1024)
)

type contentService interface {
	SaveConfig(context.Context, uuid.UUID, json.RawMessage) (*Pack, error)
	Export(context.Context, uuid.UUID, ExportFormat) (*ExportArchive, error)
	ExportAdaptation(context.Context, uuid.UUID, ExportFormat) (*ExportArchive, error)
	Import(context.Context, string, uuid.UUID, []byte) (*Pack, error)
	Assign(context.Context, uuid.UUID, []uuid.UUID) ([]Adaptation, error)
	Unassign(context.Context, uuid.UUID, uuid.UUID) error
	ListAdaptations(context.Context, uuid.UUID) ([]Adaptation, error)
	GetAdaptation(context.Context, uuid.UUID) (*Adaptation, error)
	UpdateAdaptationConfig(context.Context, uuid.UUID, json.RawMessage) (*Adaptation, error)
	CreateVersion(context.Context, uuid.UUID) (*Version, error)
	ListVersions(context.Context, uuid.UUID, ListInput) ([]*VersionSummary, error)
	GetVersion(context.Context, uuid.UUID, int) (*Version, error)
	RestoreVersion(context.Context, uuid.UUID, int) (*RestoreResult, error)
}

type ContentHandler struct {
	service contentService
}

type assignRequest struct {
	StudentIDs []uuid.UUID `json:"student_ids"`
}

func NewContentHandler(service contentService) *ContentHandler {
	return &ContentHandler{service: service}
}

func readConfig(w http.ResponseWriter, r *http.Request) (json.RawMessage, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxConfigSize+1)
	config, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return nil, apperr.ErrPayloadTooLarge
		}
		return nil, apperr.ErrBadRequest.WithMessage("cannot read config")
	}
	return config, nil
}

func (h *ContentHandler) SaveConfig(w http.ResponseWriter, r *http.Request) error {
	packID, err := pathUUID(r)
	if err != nil {
		return err
	}
	config, err := readConfig(w, r)
	if err != nil {
		return err
	}
	result, err := h.service.SaveConfig(r.Context(), packID, config)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, result)
}

func (h *ContentHandler) Export(w http.ResponseWriter, r *http.Request) error {
	packID, err := pathUUID(r)
	if err != nil {
		return err
	}
	format, err := ParseExportFormat(r.URL.Query().Get("format"))
	if err != nil {
		return err
	}
	archive, err := h.service.Export(r.Context(), packID, format)
	if err != nil {
		return err
	}
	return writeExportArchive(w, archive)
}

func (h *ContentHandler) ExportAdaptation(w http.ResponseWriter, r *http.Request) error {
	adaptationID, err := pathUUID(r)
	if err != nil {
		return err
	}
	format, err := ParseExportFormat(r.URL.Query().Get("format"))
	if err != nil {
		return err
	}
	archive, err := h.service.ExportAdaptation(r.Context(), adaptationID, format)
	if err != nil {
		return err
	}
	return writeExportArchive(w, archive)
}

func writeExportArchive(w http.ResponseWriter, archive *ExportArchive) error {
	defer func() {
		if closeErr := archive.Stream.Close(); closeErr != nil {
			slog.Warn("close exported archive", "err", closeErr)
		}
	}()
	w.Header().Set("Content-Type", "application/vnd.linka+zip")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{
		"filename": archive.Filename,
	}))
	w.Header().Set("Content-Length", strconv.FormatInt(archive.Size, 10))
	w.WriteHeader(http.StatusOK)
	_, err := io.Copy(w, archive.Stream)
	return err
}

func (h *ContentHandler) Import(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, MaxArchiveSize+archiveMultipartOverhead)
	file, _, err := r.FormFile("file")
	if err != nil {
		if errors.Is(err, multipart.ErrMessageTooLarge) {
			return apperr.ErrPayloadTooLarge
		}
		return apperr.ErrBadRequest.WithMessage("multipart field file is required")
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			slog.Warn("close archive upload", "err", closeErr)
		}
		if r.MultipartForm != nil {
			if removeErr := r.MultipartForm.RemoveAll(); removeErr != nil {
				slog.Warn("remove archive multipart form", "err", removeErr)
			}
		}
	}()
	folderID, err := uuid.Parse(r.FormValue("folder_id"))
	if err != nil || folderID == uuid.Nil {
		return apperr.ErrBadRequest.WithMessage("folder_id must be a valid UUID")
	}
	data, err := io.ReadAll(io.LimitReader(file, MaxArchiveSize+1))
	if err != nil {
		return apperr.ErrBadRequest.WithMessage("cannot read archive")
	}
	if int64(len(data)) > MaxArchiveSize {
		return apperr.ErrPayloadTooLarge
	}
	result, err := h.service.Import(r.Context(), r.FormValue("title"), folderID, data)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, result)
}

func (h *ContentHandler) Assign(w http.ResponseWriter, r *http.Request) error {
	packID, err := pathUUID(r)
	if err != nil {
		return err
	}
	var request assignRequest
	if err = decodeJSON(r, &request); err != nil {
		return apperr.ErrBadRequest
	}
	result, err := h.service.Assign(r.Context(), packID, request.StudentIDs)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, result)
}

func (h *ContentHandler) Unassign(w http.ResponseWriter, r *http.Request) error {
	packID, err := pathUUID(r)
	if err != nil {
		return err
	}
	studentID, err := uuid.Parse(r.PathValue("student_id"))
	if err != nil || studentID == uuid.Nil {
		return apperr.ErrBadRequest.WithMessage("student id must be a valid UUID")
	}
	if err = h.service.Unassign(r.Context(), packID, studentID); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h *ContentHandler) ListAdaptations(w http.ResponseWriter, r *http.Request) error {
	packID, err := pathUUID(r)
	if err != nil {
		return err
	}
	result, err := h.service.ListAdaptations(r.Context(), packID)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, result)
}

func (h *ContentHandler) GetAdaptation(w http.ResponseWriter, r *http.Request) error {
	adaptationID, err := pathUUID(r)
	if err != nil {
		return err
	}
	result, err := h.service.GetAdaptation(r.Context(), adaptationID)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, result)
}

func (h *ContentHandler) UpdateAdaptationConfig(w http.ResponseWriter, r *http.Request) error {
	adaptationID, err := pathUUID(r)
	if err != nil {
		return err
	}
	config, err := readConfig(w, r)
	if err != nil {
		return err
	}
	result, err := h.service.UpdateAdaptationConfig(r.Context(), adaptationID, config)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, result)
}

func (h *ContentHandler) CreateVersion(w http.ResponseWriter, r *http.Request) error {
	packID, err := pathUUID(r)
	if err != nil {
		return err
	}
	result, err := h.service.CreateVersion(r.Context(), packID)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, result)
}

func (h *ContentHandler) ListVersions(w http.ResponseWriter, r *http.Request) error {
	packID, err := pathUUID(r)
	if err != nil {
		return err
	}
	limit, err := optionalQueryInt(r, "limit")
	if err != nil {
		return err
	}
	offset, err := optionalQueryInt(r, "offset")
	if err != nil {
		return err
	}
	result, err := h.service.ListVersions(
		r.Context(), packID, ListInput{Limit: limit, Offset: offset},
	)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, result)
}

func (h *ContentHandler) RestoreVersion(w http.ResponseWriter, r *http.Request) error {
	packID, err := pathUUID(r)
	if err != nil {
		return err
	}
	versionNumber, err := versionPathValue(r)
	if err != nil {
		return err
	}
	result, err := h.service.RestoreVersion(r.Context(), packID, versionNumber)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, result)
}

func (h *ContentHandler) GetVersion(w http.ResponseWriter, r *http.Request) error {
	packID, err := pathUUID(r)
	if err != nil {
		return err
	}
	versionNumber, err := versionPathValue(r)
	if err != nil {
		return err
	}
	result, err := h.service.GetVersion(r.Context(), packID, versionNumber)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, result)
}

func versionPathValue(r *http.Request) (int, error) {
	versionNumber, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || versionNumber < 1 {
		return 0, apperr.ErrBadRequest.WithMessage("version must be a positive integer")
	}
	return versionNumber, nil
}
