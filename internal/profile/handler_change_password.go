package profile

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/reqctx"
)

type ChangePasswordReq struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
	RepeatPassword  string `json:"repeat_password"`
}
type UserHandler struct {
	userService *UserService
}

func NewUserHandler(svc *UserService) *UserHandler {
	return &UserHandler{userService: svc}
}

func (h *UserHandler) ChangePassword(w http.ResponseWriter, r *http.Request) error {
	var req ChangePasswordReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return apperr.ErrBadRequest.WithMessage("invalid JSON format")
	}
	//валидация полей
	if req.CurrentPassword == "" {
		return apperr.ErrBadRequest.WithMessage("field not filled in current_password")
	}
	if req.NewPassword == "" {
		return apperr.ErrBadRequest.WithMessage("field not filled in new_password")
	}
	if req.RepeatPassword == "" {
		return apperr.ErrBadRequest.WithMessage("field not filled in repeat_password")
	}
	//Получаем ID текущего пользователя из контекста после аутентификации
	userID, ok := reqctx.GetUserID(r.Context())
	if !ok {
		return apperr.ErrUnauthorized
	}
	err := h.userService.ChangePassword(r.Context(), userID, req.NewPassword, req.CurrentPassword, req.RepeatPassword)
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
		return nil
	case errors.Is(err, ErrPasswordLen), errors.Is(err, ErrPasswordTooLong), errors.Is(err, ErrOverlap), errors.Is(err, ErrOldPassword):
		return apperr.ErrBadRequest.WithMessage(err.Error())
	case errors.Is(err, sql.ErrNoRows):
		return apperr.ErrNotFound.WithMessage("user not found")
	default:
		return apperr.ErrInternal.WithError(err)
	}
}
