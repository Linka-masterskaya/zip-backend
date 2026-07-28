package profile

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
)

type ChangePasswordReq struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}
type ChangePasswordHandler struct {
	changePasswordService *ChangePasswordService
}

func NewChangePasswordHandler(svc *ChangePasswordService) *ChangePasswordHandler {
	return &ChangePasswordHandler{changePasswordService: svc}
}

func (h *ChangePasswordHandler) ChangePassword(w http.ResponseWriter, r *http.Request) error {
	var req ChangePasswordReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return apperr.ErrBadRequest.WithMessage("invalid JSON format")
	}
	err := h.changePasswordService.ChangePassword(r.Context(), req.NewPassword, req.CurrentPassword)

	var appErr *apperr.AppError
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
		return nil
	case errors.As(err, &appErr):
		return appErr
	case errors.Is(err, sql.ErrNoRows):
		return apperr.ErrNotFound
	default:
		return apperr.ErrInternal.WithError(err)
	}
}
