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

type shareTaskService interface {
	GetTask(context.Context, uuid.UUID) (*ShareTask, error)
}

// ShareHandler exposes pack share delivery endpoints.
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
	if err = decodeJSON(r, &req); err != nil {
		return apperr.ErrBadRequest.WithMessage("invalid share request body")
	}
	if req.TargetID == uuid.Nil {
		return apperr.ErrBadRequest.WithMessage("target_id must be a valid UUID")
	}

	//nolint:staticcheck // Keep explicit field mapping so request/API changes cannot silently alter service input.
	input := ShareInput{TargetType: req.TargetType, TargetID: req.TargetID}
	result, err := h.service.Share(r.Context(), packID, input)
	if err != nil {
		return err
	}
	if result == nil {
		return apperr.ErrInternal.WithMessage("share result is empty")
	}
	if result.Pack != nil {
		return writeJSON(w, http.StatusCreated, result.Pack)
	}
	if result.Task != nil {
		return writeJSON(w, http.StatusAccepted, result.Task)
	}
	return apperr.ErrInternal.WithMessage("share result is invalid")
}

func (h *ShareHandler) GetShareTask(w http.ResponseWriter, r *http.Request) error {
	taskID, err := pathUUID(r)
	if err != nil {
		return err
	}
	taskService, ok := h.service.(shareTaskService)
	if !ok {
		return apperr.ErrInternal.WithMessage("share task service is not configured")
	}
	task, err := taskService.GetTask(r.Context(), taskID)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, task)
}
