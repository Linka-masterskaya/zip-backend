package student

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/media"
	"github.com/google/uuid"
)

// Ограничения совпадают с аватаром профиля: 2 МБ на картинку плюс запас на
// границы multipart.
const (
	maxAvatarSizeBytes   int64 = 2 * 1024 * 1024
	avatarMultipartSlack int64 = 64 * 1024
	maxAvatarBodyBytes   int64 = maxAvatarSizeBytes + avatarMultipartSlack
)

// mediaUploader — часть банка медиа: аватар ученика хранится обычным файлом
// организации, поэтому загрузка идёт тем же путём, что и POST /media.
type mediaUploader interface {
	Upload(context.Context, []byte, string) (*media.Response, error)
}

// avatarResponse отвечает на PUT /students/{id}/avatar. Кроме ссылки
// возвращаем и идентификатор файла: им клиент потом снимает или меняет
// аватар через PATCH.
type avatarResponse struct {
	AvatarURL     *string    `json:"avatar_url"`
	AvatarMediaID *uuid.UUID `json:"avatar_media_id"`
}

// ReplaceAvatar кладёт картинку в банк медиа и ставит её ученику. Порядок
// важен: сначала убеждаемся, что ученик существует и наш, иначе опечатка в
// id оставила бы в банке файл, который никому не нужен.
func (s *Service) ReplaceAvatar(
	ctx context.Context,
	studentID uuid.UUID,
	data []byte,
	name string,
) (*Student, error) {
	ownerID, err := owner(ctx)
	if err != nil {
		return nil, err
	}
	if detectAvatarMIME(data) == "" {
		return nil, apperr.ErrBadRequest.WithMessage("avatar must be png, jpeg, or webp image")
	}
	owned, err := s.repo.Owned(ctx, ownerID, studentID)
	if err != nil {
		return nil, err
	}
	if !owned {
		return nil, apperr.ErrNotFound
	}
	uploaded, err := s.uploader.Upload(ctx, data, name)
	if err != nil {
		return nil, err
	}
	stored, err := s.repo.Update(ctx, ownerID, studentID, storedUpdate{
		AvatarMediaID: &uploaded.ID, AvatarMediaIDSet: true,
	})
	if err != nil {
		return nil, mapStudentError(err)
	}
	return s.decode(ctx, stored)
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
	r.Body = http.MaxBytesReader(w, r.Body, maxAvatarBodyBytes)
	file, header, err := r.FormFile("file")
	if err != nil {
		return nil, "", avatarReadError(err)
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

	data, err := io.ReadAll(io.LimitReader(file, maxAvatarSizeBytes+1))
	if err != nil {
		return nil, "", avatarReadError(err)
	}
	if int64(len(data)) > maxAvatarSizeBytes {
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

func avatarReadError(err error) error {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) || strings.Contains(err.Error(), "request body too large") {
		return apperr.ErrPayloadTooLarge
	}
	return apperr.ErrBadRequest
}

// detectAvatarMIME отсекает всё, что не картинка: банк медиа принимает и
// звук, но аватаром он быть не может.
func detectAvatarMIME(data []byte) string {
	mimeType := http.DetectContentType(data)
	if mimeType == "image/png" || mimeType == "image/jpeg" || mimeType == "image/webp" {
		return mimeType
	}
	return ""
}
