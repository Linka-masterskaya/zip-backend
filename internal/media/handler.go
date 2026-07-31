// Package media contains media upload and storage handlers.
package media

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/google/uuid"
)

const multipartOverhead = int64(64 * 1024)

type mediaService interface {
	Upload(context.Context, []byte) (*Response, error)
	Get(context.Context, uuid.UUID) (*Response, error)
	Delete(context.Context, uuid.UUID) error
}

type Handler struct {
	service mediaService
}

func NewHandler(service mediaService) *Handler {
	return &Handler{service: service}
}

func (h *Handler) Upload(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, MaxFileSize+multipartOverhead)
	file, _, err := r.FormFile("file")
	if err != nil {
		if errors.Is(err, multipart.ErrMessageTooLarge) {
			return apperr.ErrPayloadTooLarge
		}
		return apperr.ErrBadRequest.WithMessage("multipart field file is required")
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			slog.Warn("close media upload", "err", closeErr)
		}
		if r.MultipartForm != nil {
			if removeErr := r.MultipartForm.RemoveAll(); removeErr != nil {
				slog.Warn("remove media multipart form", "err", removeErr)
			}
		}
	}()
	data, err := io.ReadAll(io.LimitReader(file, MaxFileSize+1))
	if err != nil {
		return apperr.ErrBadRequest.WithMessage("cannot read media file")
	}
	if int64(len(data)) > MaxFileSize {
		return apperr.ErrPayloadTooLarge
	}
	result, err := h.service.Upload(r.Context(), data)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, result)
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil || id == uuid.Nil {
		return apperr.ErrBadRequest.WithMessage("media id must be a valid UUID")
	}
	result, err := h.service.Get(r.Context(), id)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, result)
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil || id == uuid.Nil {
		return apperr.ErrBadRequest.WithMessage("media id must be a valid UUID")
	}
	if err = h.service.Delete(r.Context(), id); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		slog.Error("encode media response", "err", err)
	}
	return nil
}
