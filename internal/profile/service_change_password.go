package profile

import (
	"context"
	"log/slog"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/auth"
	"github.com/Linka-masterskaya/zip-backend/internal/authctx"
	"github.com/Linka-masterskaya/zip-backend/internal/logger"
	"golang.org/x/crypto/bcrypt"
)

// SessionRevoker revokes all of a user's active sessions.
type SessionRevoker interface {
	RevokeAllSessions(ctx context.Context, userID string) error
}

type ChangePasswordService struct {
	repo     ChangePasswordRepo
	sessions SessionRevoker
}

func NewChangePasswordService(repo ChangePasswordRepo, sessions SessionRevoker) *ChangePasswordService {
	return &ChangePasswordService{repo: repo, sessions: sessions}
}
func (s *ChangePasswordService) ChangePassword(ctx context.Context, newPassword, oldPassword string) error {
	id, err := authctx.UserIDFromCtx(ctx)
	if err != nil {
		return err
	}
	user, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	err = auth.ValidatePassword(newPassword)
	if err != nil {
		return err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(oldPassword)); err != nil {
		return apperr.ErrBadRequest.WithMessage("incorrect old password")
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	if err := s.repo.Update(ctx, id, string(newHash)); err != nil {
		return err
	}
	if err := s.sessions.RevokeAllSessions(ctx, id.String()); err != nil {
		slog.Error("revoke all sessions after password change failed",
			"user_id", id.String(),
			logger.Err(err),
		)
	}
	return nil
}
