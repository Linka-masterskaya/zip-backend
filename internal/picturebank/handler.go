package picturebank

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/media"
)

type service interface {
	Categories(context.Context) ([]Category, error)
	Search(context.Context, string) ([]Picture, error)
	Image(context.Context, string) (*Image, error)
	Import(context.Context, string) (*media.Response, error)
}

type Handler struct {
	service service
}

func NewHandler(service service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) Categories(w http.ResponseWriter, r *http.Request) error {
	result, err := h.service.Categories(r.Context())
	if err != nil {
		setRetryAfter(w, err)
		return err
	}
	w.Header().Set("Cache-Control", "private, max-age=60")
	return writeJSON(w, result)
}

func (h *Handler) Search(w http.ResponseWriter, r *http.Request) error {
	result, err := h.service.Search(r.Context(), r.URL.Query().Get("query"))
	if err != nil {
		setRetryAfter(w, err)
		return err
	}
	w.Header().Set("Cache-Control", "private, max-age=60")
	return writeJSON(w, result)
}

func (h *Handler) Image(w http.ResponseWriter, r *http.Request) error {
	result, err := h.service.Image(r.Context(), r.PathValue("id"))
	if err != nil {
		setRetryAfter(w, err)
		return err
	}
	w.Header().Set("Content-Type", result.ContentType)
	w.Header().Set("Cache-Control", "public, max-age=86400, immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, err = io.Copy(w, bytes.NewReader(result.Data))
	return err
}

func (h *Handler) Import(w http.ResponseWriter, r *http.Request) error {
	result, err := h.service.Import(r.Context(), r.PathValue("id"))
	if err != nil {
		setRetryAfter(w, err)
		return err
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	return json.NewEncoder(w).Encode(result)
}

func setRetryAfter(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrRateLimited) {
		w.Header().Set("Retry-After", "1")
	}
}

func writeJSON(w http.ResponseWriter, value any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		return apperr.ErrInternal.WithError(err)
	}
	return nil
}
