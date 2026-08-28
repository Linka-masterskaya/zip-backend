package student

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/google/uuid"
)

type studentService interface {
	Create(context.Context, CreateInput) (*Student, error)
	List(context.Context, ListInput) (*ListResult, error)
	Update(context.Context, uuid.UUID, UpdateInput) (*Student, error)
	ReplaceAvatar(context.Context, uuid.UUID, []byte, string) (*Student, error)
	Delete(context.Context, uuid.UUID, bool) error
}

type Handler struct {
	service studentService
}

func NewHandler(service studentService) *Handler {
	return &Handler{service: service}
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) error {
	var input CreateInput
	if err := decode(r, &input); err != nil {
		return apperr.ErrBadRequest
	}
	result, err := h.service.Create(r.Context(), input)
	if err != nil {
		return err
	}
	return respond(w, http.StatusCreated, result)
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()

	input := ListInput{
		SortBy: q.Get("sort_by"),
		Order:  q.Get("order"),
	}

	if limitStr := q.Get("limit"); limitStr != "" {
		lim, err := strconv.Atoi(limitStr)
		if err != nil {
			return apperr.ErrBadRequest.WithMessage("invalid limit parameter")
		}
		input.Limit = lim
	}

	if offsetStr := q.Get("offset"); offsetStr != "" {
		ofs, err := strconv.Atoi(offsetStr)
		if err != nil {
			return apperr.ErrBadRequest.WithMessage("invalid offset parameter")
		}
		input.Offset = ofs
	}

	result, err := h.service.List(r.Context(), input)
	if err != nil {
		return err
	}
	return respond(w, http.StatusOK, result)
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var input UpdateInput
	if err = decode(r, &input); err != nil {
		return apperr.ErrBadRequest
	}
	result, err := h.service.Update(r.Context(), id, input)
	if err != nil {
		return err
	}
	return respond(w, http.StatusOK, result)
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	force, err := forceFlag(r)
	if err != nil {
		return err
	}
	if err = h.service.Delete(r.Context(), id, force); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// forceFlag читает ?force=true — полное удаление вместо архивации.
func forceFlag(r *http.Request) (bool, error) {
	raw := r.URL.Query().Get("force")
	if raw == "" {
		return false, nil
	}
	force, err := strconv.ParseBool(raw)
	if err != nil {
		return false, apperr.ErrBadRequest.WithMessage("force must be a boolean")
	}
	return force, nil
}

func pathID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil || id == uuid.Nil {
		return uuid.Nil, apperr.ErrBadRequest
	}
	return id, nil
}

func decode(r *http.Request, target any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func respond(w http.ResponseWriter, status int, payload any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		slog.Error("encode student response", "err", err)
	}
	return nil
}
