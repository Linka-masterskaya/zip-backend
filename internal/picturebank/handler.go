package picturebank

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
)

type service interface {
	Categories(context.Context) ([]Category, error)
	Search(context.Context, string) ([]Picture, error)
	Image(context.Context, string) (*Image, error)
	Import(context.Context, string) (*PictureReference, error)
	PicturesByCategory(context.Context, string) ([]Picture, error)
}

type Handler struct {
	service         service
	pictureCacheTTL time.Duration
}

func NewHandler(service service, cacheTTL ...time.Duration) *Handler {
	ttl := time.Hour
	if len(cacheTTL) > 0 && cacheTTL[0] > 0 {
		ttl = cacheTTL[0]
	}
	return &Handler{service: service, pictureCacheTTL: ttl}
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
	return writeJSON(w, toPictureResponses(result))
}

func (h *Handler) Image(w http.ResponseWriter, r *http.Request) error {
	result, err := h.service.Image(r.Context(), r.PathValue("id"))
	placeholderReason := ""
	switch {
	case errors.Is(err, ErrPictureNotFound):
		result = DeletedPicturePlaceholder()
		err = nil
		placeholderReason = "deleted"
	case errors.Is(err, ErrUnavailable):
		result = UnavailablePicturePlaceholder()
		err = nil
		placeholderReason = "unavailable"
	}
	if err != nil {
		setRetryAfter(w, err)
		return err
	}
	w.Header().Set("Content-Type", result.ContentType)
	if placeholderReason != "" {
		w.Header().Set("Cache-Control", "public, max-age=30")
		w.Header().Set("X-Picture-Placeholder", placeholderReason)
	} else {
		maxAge := int64(h.pictureCacheTTL / time.Second)
		w.Header().Set("Cache-Control", "public, max-age="+strconv.FormatInt(maxAge, 10))
	}
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

func (h *Handler) PicturesByCategory(w http.ResponseWriter, r *http.Request) error {
	categoryID := r.PathValue("categoryId")

	result, err := h.service.PicturesByCategory(r.Context(), categoryID)
	if err != nil {
		setRetryAfter(w, err)
		return err
	}

	w.Header().Set("Cache-Control", "private, max-age=60")
	return writeJSON(w, toPictureResponses(result))
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
