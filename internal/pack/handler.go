package pack

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/google/uuid"
)

type packService interface {
	Create(context.Context, string, uuid.UUID) (*Pack, error)
	Get(context.Context, uuid.UUID) (*Pack, error)
	List(context.Context, uuid.UUID, ListInput) ([]*Pack, error)
	Update(context.Context, uuid.UUID, UpdateInput) (*Pack, error)
	Delete(context.Context, uuid.UUID) error
	Move(context.Context, uuid.UUID, uuid.UUID) (*Pack, error)
}

// Handler contains pack HTTP handlers.
type Handler struct {
	service packService
}

// NewHandler creates a pack HTTP handler.
func NewHandler(service packService) *Handler {
	return &Handler{service: service}
}

type nullableJSONField[T any] struct {
	Set   bool
	Value *T
}

func (f *nullableJSONField[T]) UnmarshalJSON(data []byte) error {
	f.Set = true
	f.Value = nil
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil
	}

	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	f.Value = &value
	return nil
}

func (f nullableJSONField[T]) patch() NullablePatch[T] {
	return NullablePatch[T](f)
}

type createPackRequest struct {
	Title    string    `json:"title"`
	FolderID uuid.UUID `json:"folder_id"`
}

type updatePackRequest struct {
	Title      *string                   `json:"title"`
	FolderID   *uuid.UUID                `json:"folder_id"`
	AgeMin     nullableJSONField[int]    `json:"age_min"`
	AgeMax     nullableJSONField[int]    `json:"age_max"`
	Difficulty nullableJSONField[string] `json:"difficulty"`
	Goals      *[]string                 `json:"goals"`
	Notes      nullableJSONField[string] `json:"notes"`
}

type movePackRequest struct {
	FolderID uuid.UUID `json:"folder_id"`
}

// CreatePack handles POST /api/v1/packs.
func (h *Handler) CreatePack(w http.ResponseWriter, r *http.Request) error {
	var req createPackRequest
	if err := decodeJSON(r, &req); err != nil || req.FolderID == uuid.Nil {
		return apperr.ErrBadRequest
	}

	result, err := h.service.Create(r.Context(), req.Title, req.FolderID)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, result)
}

// GetPack handles GET /api/v1/packs/{id}.
func (h *Handler) GetPack(w http.ResponseWriter, r *http.Request) error {
	packID, err := pathUUID(r)
	if err != nil {
		return err
	}
	result, err := h.service.Get(r.Context(), packID)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, result)
}

// ListPacks handles GET /api/v1/packs?folder_id=&limit=&offset=.
func (h *Handler) ListPacks(w http.ResponseWriter, r *http.Request) error {
	folderID, err := uuid.Parse(r.URL.Query().Get("folder_id"))
	if err != nil || folderID == uuid.Nil {
		return apperr.ErrBadRequest.WithMessage("folder_id must be a valid UUID")
	}
	input, err := listInputFromRequest(r)
	if err != nil {
		return err
	}
	result, err := h.service.List(r.Context(), folderID, input)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, result)
}

// UpdatePack handles PATCH /api/v1/packs/{id}.
func (h *Handler) UpdatePack(w http.ResponseWriter, r *http.Request) error {
	packID, err := pathUUID(r)
	if err != nil {
		return err
	}
	var req updatePackRequest
	if err = decodeJSON(r, &req); err != nil {
		return apperr.ErrBadRequest
	}
	result, err := h.service.Update(r.Context(), packID, req.updateInput())
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, result)
}

// DeletePack handles DELETE /api/v1/packs/{id}.
func (h *Handler) DeletePack(w http.ResponseWriter, r *http.Request) error {
	packID, err := pathUUID(r)
	if err != nil {
		return err
	}
	if err = h.service.Delete(r.Context(), packID); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// MovePack handles POST /api/v1/packs/{id}/move.
func (h *Handler) MovePack(w http.ResponseWriter, r *http.Request) error {
	packID, err := pathUUID(r)
	if err != nil {
		return err
	}
	var req movePackRequest
	if err = decodeJSON(r, &req); err != nil || req.FolderID == uuid.Nil {
		return apperr.ErrBadRequest
	}
	result, err := h.service.Move(r.Context(), packID, req.FolderID)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, result)
}

func (r updatePackRequest) updateInput() UpdateInput {
	input := UpdateInput{Title: r.Title, FolderID: r.FolderID, Notes: r.Notes.patch()}
	if r.hasFilterMetadata() {
		input.FilterMetadata = &FilterMetadataPatch{
			AgeMin:     r.AgeMin.patch(),
			AgeMax:     r.AgeMax.patch(),
			Difficulty: r.Difficulty.patch(),
			Goals:      r.Goals,
		}
	}
	return input
}

func (r updatePackRequest) hasFilterMetadata() bool {
	return r.AgeMin.Set || r.AgeMax.Set || r.Difficulty.Set || r.Goals != nil
}

func listInputFromRequest(r *http.Request) (ListInput, error) {
	input := ListInput{}
	limit, err := optionalQueryInt(r, "limit")
	if err != nil {
		return ListInput{}, err
	}
	if r.URL.Query().Has("limit") && limit == 0 {
		return ListInput{}, apperr.ErrBadRequest.WithMessage("limit must be between 1 and 100")
	}
	offset, err := optionalQueryInt(r, "offset")
	if err != nil {
		return ListInput{}, err
	}
	input.Limit = limit
	input.Offset = offset
	return validateListInput(input)
}

func optionalQueryInt(r *http.Request, name string) (int, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, apperr.ErrBadRequest.WithMessage(name + " must be an integer")
	}
	return value, nil
}

func decodeJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func pathUUID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil || id == uuid.Nil {
		return uuid.Nil, apperr.ErrBadRequest.WithMessage("pack id must be a valid UUID")
	}
	return id, nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		slog.Error("failed to encode pack response", "err", err)
	}
	return nil
}
