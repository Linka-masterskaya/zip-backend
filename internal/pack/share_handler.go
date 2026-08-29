package pack

import (
	"context"
	"net/http"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/google/uuid"
)

type shareService interface {
	Share(context.Context, uuid.UUID, ShareInput) (*ShareResult, error)
}

// ShareHandler exposes POST /api/v1/packs/{id}/share.
type ShareHandler struct {
	service shareService
}

func NewShareHandler(service shareService) *ShareHandler {
	return &ShareHandler{service: service}
}

type sharePackRequest struct {
	TargetType ShareTargetType `json:"target_type"`
	TargetID   uuid.UUID       `json:"target_id"`
}

func (h *ShareHandler) SharePack(w http.ResponseWriter, r *http.Request) error {
	packID, err := pathUUID(r)
	if err != nil {
		return err
	}

	var req sharePackRequest
	if err = decodeJSON(r, &req); err != nil || req.TargetID == uuid.Nil {
		return apperr.ErrBadRequest
	}

	result, err := h.service.Share(r.Context(), packID, ShareInput(req))
	if err != nil {
		return err
	}
	if result == nil {
		return apperr.ErrInternal.WithMessage("share result is empty")
	}
	if result.Pack != nil {
		return writeJSON(w, http.StatusCreated, result.Pack)
	}
	if result.Accepted {
		w.WriteHeader(http.StatusAccepted)
		return nil
	}
	return apperr.ErrInternal.WithMessage("share result is invalid")
}
