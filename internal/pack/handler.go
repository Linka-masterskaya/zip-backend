package pack

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/pkg/linka"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

type createPackRequest struct {
	Name   string          `json:"name"`
	Config json.RawMessage `json:"config,omitempty"`
}

func (h *Handler) CreatePack(w http.ResponseWriter, r *http.Request) error {
	var req createPackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return apperr.ErrBadRequest
	}

	if req.Name == "" {
		return apperr.ErrBadRequest
	}

	if len(req.Config) > 0 {
		if err := linka.ValidateConfig(r.Context(), req.Config); err != nil {
			slog.Error("invalid pack config on create", "err", err)
			return apperr.ErrBadRequest
		}
	}

	res, err := h.service.Create(r.Context(), req.Name)
	if err != nil {
		return err
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(res); err != nil {
		slog.Error("failed to encode response", "err", err)
	}
	return nil
}

func (h *Handler) GetPack(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	if id == "" {
		return apperr.ErrBadRequest
	}

	res, err := h.service.Get(r.Context(), id)
	if err != nil {
		return err
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(res); err != nil {
		slog.Error("failed to encode response", "err", err)
	}
	return nil
}

func (h *Handler) ListPacks(w http.ResponseWriter, r *http.Request) error {
	res, err := h.service.List(r.Context())
	if err != nil {
		return err
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(res); err != nil {
		slog.Error("failed to encode response", "err", err)
	}
	return nil
}

type updatePackRequest struct {
	Config json.RawMessage `json:"config,omitempty"`
}

func (h *Handler) UpdatePack(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	if id == "" {
		return apperr.ErrBadRequest
	}

	var req updatePackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return apperr.ErrBadRequest
	}

	if len(req.Config) > 0 {
		if err := linka.ValidateConfig(r.Context(), req.Config); err != nil {
			slog.Error("invalid pack config on update", "err", err)
			return apperr.ErrBadRequest
		}
	}

	res, err := h.service.Update(r.Context(), id)
	if err != nil {
		return err
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(res); err != nil {
		slog.Error("failed to encode response", "err", err)
	}
	return nil
}
