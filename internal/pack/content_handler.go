package pack

import (
	"bytes"
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

const archiveMultipartOverhead = int64(128 * 1024)

type contentService interface {
	SaveConfig(context.Context, uuid.UUID, json.RawMessage) (*Pack, error)
	Export(context.Context, uuid.UUID) ([]byte, string, error)
	Import(context.Context, string, uuid.UUID, []byte) (*Pack, error)
	Assign(context.Context, uuid.UUID, []uuid.UUID) ([]Adaptation, error)
	Unassign(context.Context, uuid.UUID, uuid.UUID) error
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

func (h *ContentHandler) SaveConfig(w http.ResponseWriter, r *http.Request) error {
	packID, err := pathUUID(r)
	if err != nil {
		return err
	}
	r.Body = http.MaxBytesReader(w, r.Body, int64(5*1024*1024)+1)
	config, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return apperr.ErrPayloadTooLarge
		}
		return apperr.ErrBadRequest.WithMessage("cannot read config")
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
	data, name, err := h.service.Export(r.Context(), packID)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/vnd.linka+zip")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{
		"filename": name,
	}))
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	_, err = io.Copy(w, bytes.NewReader(data))
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
