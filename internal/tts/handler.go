package tts

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
)

type ttsService interface {
	CreateAudio(context.Context, TTSData) (string, error)
}

type Handler struct {
	service ttsService
}

func NewHandler(service ttsService) *Handler {
	return &Handler{service: service}
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) error {
	var ttsData TTSData
	if err := decode(r, ttsData); err != nil {
		return apperr.ErrBadRequest
	}

	jobID, err := h.service.CreateAudio(r.Context(), ttsData)
	if err != nil {
		return err
	}

	return writeJSON(w, http.StatusOK, jobID)
}

func decode(r *http.Request, target any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func writeJSON(w http.ResponseWriter, status int, payload any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		slog.Error("failed to encode pack response", "err", err)
	}
	return nil
}
