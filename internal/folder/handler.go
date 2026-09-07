package folder

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/httpquery"
	"github.com/google/uuid"
)

type folderService interface {
	Create(context.Context, CreateInput) (*Folder, error)
	List(context.Context, ListInput) ([]Folder, error)
	Rename(context.Context, uuid.UUID, string) (*Folder, error)
	Move(context.Context, uuid.UUID, *uuid.UUID) (*Folder, error)
	Delete(context.Context, uuid.UUID) error
	Contents(context.Context, ContentsInput) (*ContentsPage, error)
}

type Handler struct {
	service folderService
}

func NewHandler(service folderService) *Handler {
	return &Handler{service: service}
}

type createRequest struct {
	ParentID  *uuid.UUID `json:"parent_id"`
	Section   string     `json:"section"`
	Kind      string     `json:"kind"`
	StudentID *uuid.UUID `json:"student_id"`
	Name      string     `json:"name"`
}

type renameRequest struct {
	Name string `json:"name"`
}

type moveRequest struct {
	ParentID *uuid.UUID `json:"parent_id"`
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) error {
	var req createRequest
	if err := decode(r, &req); err != nil {
		return apperr.ErrBadRequest
	}
	result, err := h.service.Create(r.Context(), CreateInput(req))
	if err != nil {
		return err
	}
	return respond(w, http.StatusCreated, result)
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) error {
	parentID, err := httpquery.OptionalUUID(r, "parent_id")
	if err != nil {
		return err
	}
	limit, offset, err := pagination(r)
	if err != nil {
		return err
	}
	result, err := h.service.List(r.Context(), ListInput{
		Section: r.URL.Query().Get("section"), ParentID: parentID,
		Limit: limit, Offset: offset,
	})
	if err != nil {
		return err
	}
	return respond(w, http.StatusOK, result)
}

func (h *Handler) Contents(w http.ResponseWriter, r *http.Request) error {
	limit, offset, err := pagination(r)
	if err != nil {
		return err
	}
	parentID, err := httpquery.OptionalUUID(r, "parent_id")
	if err != nil {
		return err
	}
	age, err := httpquery.OptionalInt(r, "age")
	if err != nil {
		return err
	}
	ageFrom, err := httpquery.OptionalInt(r, "age_from")
	if err != nil {
		return err
	}
	ageTo, err := httpquery.OptionalInt(r, "age_to")
	if err != nil {
		return err
	}
	result, err := h.service.Contents(r.Context(), ContentsInput{
		Section:  r.PathValue("section"),
		ParentID: parentID,
		Limit:    limit, Offset: offset,
		Sort: r.URL.Query().Get("sort"), Order: r.URL.Query().Get("order"),
		Query: r.URL.Query().Get("query"), Type: r.URL.Query().Get("type"),
		Age: age, AgeFrom: ageFrom, AgeTo: ageTo,
		Difficulty: r.URL.Query().Get("difficulty"),
	})
	if err != nil {
		return err
	}
	return respond(w, http.StatusOK, result)
}

func (h *Handler) Rename(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var req renameRequest
	if err = decode(r, &req); err != nil {
		return apperr.ErrBadRequest
	}
	result, err := h.service.Rename(r.Context(), id, req.Name)
	if err != nil {
		return err
	}
	return respond(w, http.StatusOK, result)
}

func (h *Handler) Move(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var req moveRequest
	if err = decode(r, &req); err != nil {
		return apperr.ErrBadRequest
	}
	result, err := h.service.Move(r.Context(), id, req.ParentID)
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
	if err = h.service.Delete(r.Context(), id); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func pagination(r *http.Request) (int, int, error) {
	limit, err := httpquery.Int(r, "limit")
	if err != nil {
		return 0, 0, err
	}
	offset, err := httpquery.Int(r, "offset")
	return limit, offset, err
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
		slog.Error("encode folder response", "err", err)
	}
	return nil
}
