package student

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/avatar"
	"github.com/google/uuid"
)

type studentService interface {
	Create(context.Context, CreateInput) (*Student, error)
	List(context.Context, ListInput) (*ListResult, error)
	Update(context.Context, uuid.UUID, UpdateInput) (*Student, error)
	ReplaceAvatar(context.Context, uuid.UUID, []byte, string) (*Student, error)
	Delete(context.Context, uuid.UUID) error
	ForceDelete(context.Context, uuid.UUID) error
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
		return err
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
		return err
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
	if force {
		err = h.service.ForceDelete(r.Context(), id)
	} else {
		err = h.service.Delete(r.Context(), id)
	}
	if err != nil {
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

// decode отклоняет незнакомые поля, чтобы опечатка в теле не проходила
// молча, и называет клиенту поле, на котором споткнулся.
func decode(r *http.Request, target any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return decodeError(err)
	}
	return nil
}

func decodeError(err error) error {
	const unknownFieldPrefix = "json: unknown field "
	message := err.Error()
	if !strings.HasPrefix(message, unknownFieldPrefix) {
		return apperr.ErrBadRequest
	}
	field := strings.Trim(strings.TrimPrefix(message, unknownFieldPrefix), `"`)
	// avatar_url приходит чаще прочих: ссылка presigned и только читается,
	// а поменять аватар можно двумя другими способами.
	if field == "avatar_url" {
		return apperr.ErrBadRequest.WithMessage(
			"avatar_url is read-only. upload via PUT /students/{id}/avatar " +
				"or set avatar_media_id")
	}
	return apperr.ErrBadRequest.WithMessage("unknown field " + field)
}

func respond(w http.ResponseWriter, status int, payload any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		slog.Error("encode student response", "err", err)
	}
	return nil
}

// UploadAvatar обрабатывает PUT /students/{id}/avatar.
func (h *Handler) UploadAvatar(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	data, name, err := readAvatarFile(w, r)
	if err != nil {
		return err
	}
	result, err := h.service.ReplaceAvatar(r.Context(), id, data, name)
	if err != nil {
		return err
	}
	return respond(w, http.StatusOK, avatarResponse{
		AvatarURL: result.AvatarURL, AvatarMediaID: result.AvatarMediaID,
	})
}

func readAvatarFile(w http.ResponseWriter, r *http.Request) ([]byte, string, error) {
	r.Body = http.MaxBytesReader(w, r.Body, avatar.MaxBodyBytes)
	file, header, err := r.FormFile("file")
	if err != nil {
		return nil, "", avatar.ReadError(err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			slog.Warn("close student avatar file", "err", closeErr)
		}
		if r.MultipartForm != nil {
			if removeErr := r.MultipartForm.RemoveAll(); removeErr != nil {
				slog.Warn("remove student avatar form", "err", removeErr)
			}
		}
	}()

	data, err := io.ReadAll(io.LimitReader(file, avatar.MaxSizeBytes+1))
	if err != nil {
		return nil, "", avatar.ReadError(err)
	}
	if int64(len(data)) > avatar.MaxSizeBytes {
		return nil, "", apperr.ErrPayloadTooLarge
	}
	if len(data) == 0 {
		return nil, "", apperr.ErrBadRequest.WithMessage("avatar file is empty")
	}

	name := "avatar"
	if header != nil && header.Filename != "" {
		name = filepath.Base(header.Filename)
	}
	return data, name, nil
}
