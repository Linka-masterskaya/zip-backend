package profile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/authctx"
	"github.com/Linka-masterskaya/zip-backend/internal/logger"
	"github.com/Linka-masterskaya/zip-backend/internal/reqctx"
	"github.com/google/uuid"
)

// Avatar constants.
const (
	MaxAvatarSizeBytes      int64 = 2 * 1024 * 1024
	avatarMultipartOverhead int64 = 64 * 1024
	maxAvatarBodyBytes      int64 = MaxAvatarSizeBytes + avatarMultipartOverhead
)

// Handler handles HTTP requests for profile operations.
type Handler struct {
	service ProfService
}

// ProfService defines all profile operations available through the HTTP handler.
type ProfService interface {
	GetProfile(ctx context.Context, userID uuid.UUID) (*Response, error)
	ReplaceAvatar(ctx context.Context, userID string, reader io.Reader, size int64, mimeType string) (string, error)
	DeleteAvatar(ctx context.Context, userID string) error

	RequestEmailChange(ctx context.Context, userID uuid.UUID, newEmail string) error
	ConfirmEmailChange(ctx context.Context, tokenStr string) error
}

// NewHandler creates a new Handler instance.
func NewHandler(service ProfService) *Handler {
	return &Handler{service: service}
}

type avatarResponse struct {
	AvatarURL string `json:"avatar_url"`
}

// GetProfile takes userID from the context and passes it to the service.
func (h *Handler) GetProfile(w http.ResponseWriter, r *http.Request) error {
	userID, err := authctx.UserIDFromCtx(r.Context())
	if err != nil {
		return err
	}

	profile, err := h.service.GetProfile(r.Context(), userID)
	if err != nil {
		return err
	}

	body, err := json.Marshal(profile)
	if err != nil {
		slog.Error("marshal profile response failed", logger.Err(err))
		return apperr.ErrInternal
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	if _, err := w.Write(body); err != nil {
		slog.Error("write profile response failed", logger.Err(err))
	}

	return nil
}

// UploadAvatar handles PUT /profile/me/avatar.
func (h *Handler) UploadAvatar(w http.ResponseWriter, r *http.Request) error {
	userID, ok := reqctx.GetUserID(r.Context())
	if !ok {
		return apperr.ErrUnauthorized
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxAvatarBodyBytes)
	file, _, err := r.FormFile("file")
	if err != nil {
		return avatarReadError(err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			slog.Warn("close avatar multipart file", "err", err)
		}
		if r.MultipartForm != nil {
			if err := r.MultipartForm.RemoveAll(); err != nil {
				slog.Warn("remove avatar multipart form", "err", err)
			}
		}
	}()

	data, err := io.ReadAll(io.LimitReader(file, MaxAvatarSizeBytes+1))
	if err != nil {
		return avatarReadError(err)
	}
	if int64(len(data)) > MaxAvatarSizeBytes {
		return apperr.ErrPayloadTooLarge
	}
	if len(data) == 0 {
		return apperr.ErrBadRequest.WithMessage("avatar file is empty")
	}

	mimeType := detectAvatarMIME(data)
	if mimeType == "" {
		return apperr.ErrBadRequest.WithMessage("avatar must be png, jpeg, or webp image")
	}

	avatarURL, err := h.service.ReplaceAvatar(
		r.Context(),
		userID,
		bytes.NewReader(data),
		int64(len(data)),
		mimeType,
	)
	if err != nil {
		return err
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(avatarResponse{AvatarURL: avatarURL}); err != nil {
		slog.Error("encode avatar response", "err", err)
	}

	return nil
}

// DeleteAvatar handles DELETE /profile/me/avatar.
func (h *Handler) DeleteAvatar(w http.ResponseWriter, r *http.Request) error {
	userID, ok := reqctx.GetUserID(r.Context())
	if !ok {
		return apperr.ErrUnauthorized
	}

	if err := h.service.DeleteAvatar(r.Context(), userID); err != nil {
		return err
	}

	w.WriteHeader(http.StatusNoContent)
	return nil
}

// RequestEmailChange handles POST /profile/me/email.
func (h *Handler) RequestEmailChange(w http.ResponseWriter, r *http.Request) error {
	userID, err := authctx.UserIDFromCtx(r.Context())
	if err != nil {
		return err
	}

	var req EmailChangeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return apperr.ErrBadRequest.WithMessage("Invalid request body")
	}

	if req.NewEmail == "" {
		return apperr.ErrBadRequest.WithMessage("new_email is required")
	}

	err = h.service.RequestEmailChange(r.Context(), userID, req.NewEmail)
	if err != nil {
		switch {
		case errors.Is(err, ErrEmailInvalid):
			return apperr.ErrBadRequest.WithMessage(err.Error())
		case errors.Is(err, ErrEmailAlreadyUsed):
			return apperr.ErrConflict.WithMessage("Email already in use by another user")
		case errors.Is(err, ErrEmailSameAsCurrent):
			return apperr.ErrBadRequest.WithMessage("New email is the same as current email")
		case errors.Is(err, ErrUserNotFound):
			return apperr.ErrUnauthorized
		default:
			return apperr.ErrInternal
		}
	}

	w.WriteHeader(http.StatusAccepted)
	return nil
}

// ConfirmEmailChange handles POST /profile/me/email/confirm.
func (h *Handler) ConfirmEmailChange(w http.ResponseWriter, r *http.Request) error {
	var req EmailConfirmRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return apperr.ErrBadRequest.WithMessage("Invalid request body")
	}

	if req.Token == "" {
		return apperr.ErrBadRequest.WithMessage("token is required")
	}

	err := h.service.ConfirmEmailChange(r.Context(), req.Token)
	if err != nil {
		switch {
		case errors.Is(err, ErrTokenNotFound):
			return apperr.ErrVerifyTokenInvalid
		case errors.Is(err, ErrTokenInvalid):
			return apperr.ErrVerifyTokenInvalid.WithMessage("Invalid token format")
		case errors.Is(err, ErrTokenExpired):
			return apperr.ErrVerifyTokenInvalid.WithMessage("Token has expired")
		case errors.Is(err, ErrTokenAlreadyUsed):
			return apperr.ErrVerifyTokenInvalid.WithMessage("Token has already been used")
		case errors.Is(err, ErrEmailAlreadyUsed):
			return apperr.ErrConflict.WithMessage("Email already in use by another user")
		case errors.Is(err, ErrEmailAlreadyChanged):
			return apperr.ErrConflict.WithMessage("Email has already been changed")
		case errors.Is(err, ErrUserNotFound):
			return apperr.ErrVerifyTokenInvalid
		default:
			return apperr.ErrInternal
		}
	}

	w.WriteHeader(http.StatusNoContent)
	return nil
}

func avatarReadError(err error) error {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) || strings.Contains(err.Error(), "request body too large") {
		return apperr.ErrPayloadTooLarge
	}
	return apperr.ErrBadRequest
}

func detectAvatarMIME(data []byte) string {
	mimeType := http.DetectContentType(data)
	if mimeType == "image/png" || mimeType == "image/jpeg" || mimeType == "image/webp" {
		return mimeType
	}
	return ""
}
