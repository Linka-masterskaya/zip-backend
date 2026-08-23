package settings

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/google/uuid"
)

type settingsService interface {
	Get(context.Context) (json.RawMessage, error)
	Put(context.Context, json.RawMessage) (json.RawMessage, error)
	ListTemplates(context.Context) ([]Template, error)
	CreateTemplate(context.Context, string, json.RawMessage) (*Template, error)
	DeleteTemplate(context.Context, uuid.UUID) error
}

type Handler struct {
	service settingsService
}

func NewHandler(service settingsService) *Handler {
	return &Handler{service: service}
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) error {
	body, err := h.service.Get(r.Context())
	if err != nil {
		return err
	}
	return writeRawJSON(w, http.StatusOK, body)
}

func (h *Handler) Put(w http.ResponseWriter, r *http.Request) error {
	body, err := readRawJSON(w, r, MaxRequestSize)
	if err != nil {
		return err
	}
	body, err = h.service.Put(r.Context(), body)
	if err != nil {
		return err
	}
	return writeRawJSON(w, http.StatusOK, body)
}

func (h *Handler) ListTemplates(w http.ResponseWriter, r *http.Request) error {
	items, err := h.service.ListTemplates(r.Context())
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, items)
}

func (h *Handler) CreateTemplate(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, MaxRequestSize)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var input CreateTemplateRequest
	if err := decoder.Decode(&input); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			return apperr.ErrPayloadTooLarge
		}
		return apperr.ErrBadRequest
	}
	if err := ensureEOF(decoder); err != nil {
		return apperr.ErrBadRequest
	}
	item, err := h.service.CreateTemplate(r.Context(), input.Name, input.Body)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, item)
}

func (h *Handler) DeleteTemplate(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.ErrBadRequest.WithMessage("invalid id")
	}
	if err := h.service.DeleteTemplate(r.Context(), id); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func readRawJSON(w http.ResponseWriter, r *http.Request, limit int64) (json.RawMessage, error) {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			return nil, apperr.ErrPayloadTooLarge
		}
		return nil, apperr.ErrBadRequest
	}
	if len(body) == 0 || !json.Valid(body) {
		return nil, apperr.ErrBadRequest
	}
	return json.RawMessage(body), nil
}

func ensureEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("request body must contain a single JSON value")
	}
	return err
}

func writeRawJSON(w http.ResponseWriter, status int, payload json.RawMessage) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if _, err := w.Write(payload); err != nil {
		return err
	}
	if _, err := w.Write([]byte("\n")); err != nil {
		return err
	}

	return nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(payload)
}
