package tts

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/google/uuid"
)

type ttsService interface {
	CreateAudio(context.Context, TTSDataRequest) (string, error)
	GetJob(context.Context, uuid.UUID) (string, string, error)
	GetVoices(context.Context) ([]Voice, error)
}

type Handler struct {
	service     ttsService
	maxBodySize int64
}

func NewHandler(service ttsService, maxBodySize int64) *Handler {
	return &Handler{service: service, maxBodySize: maxBodySize}
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, h.maxBodySize)

	var ttsData TTSDataRequest
	if err := decode(r, &ttsData); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			return apperr.ErrPayloadTooLarge
		}
		return apperr.ErrBadRequest
	}

	jobID, err := h.service.CreateAudio(r.Context(), ttsData)
	if err != nil {
		return err
	}

	return writeJSON(w, http.StatusOK, TTSResponse{JobID: jobID})
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) error {
	jobID, err := pathID(r)
	if err != nil {
		return err
	}
	status, mediaID, err := h.service.GetJob(r.Context(), jobID)
	if err != nil {
		return err
	}

	payload := &TTSJobResponse{
		Status: status,
	}
	if status == StatusSucceeded {
		payload.MediaID = &mediaID
	}

	return writeJSON(w, http.StatusOK, payload)
}

func (h *Handler) Voices(w http.ResponseWriter, r *http.Request) error {
	voicesRaw, err := h.service.GetVoices(r.Context())
	if err != nil {
		return err
	}

	voices := make([]VoiceResponse, len(voicesRaw))
	for i, v := range voicesRaw {
		voices[i].ID = v.ID
		voices[i].Name = v.Name + " (" + v.LangCode + ")"
	}

	return writeJSON(w, http.StatusOK, voices)
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
		slog.Error("failed to encode response", "err", err)
	}
	return nil
}

func pathID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return uuid.Nil, apperr.ErrBadRequest.WithMessage("invalid id")
	}
	return id, nil
}
