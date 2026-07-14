package profile

import (
	"context"
	"errors"

	"golang.org/x/crypto/bcrypt"
)

var ErrPasswordLen = errors.New("password length is less than 8")
var ErrPasswordTooLong = errors.New("password length is more than 72 bytes")
var ErrOverlap = errors.New("the new password does not match the duplicate one")
var ErrOldPassword = errors.New("incorrect old password")

// SessionRevoker revokes all of a user's active sessions
type SessionRevoker interface {
	RevokeAllSessions(ctx context.Context, userID string) error
}

type UserService struct {
	repo     UserRepo
	sessions SessionRevoker
}

func NewUserService(repo UserRepo, sessions SessionRevoker) *UserService {
	return &UserService{repo: repo, sessions: sessions}
}
func (s *UserService) ChangePassword(ctx context.Context, id, newPassword, oldPassword, repeadPassword string) error {
	if len(newPassword) < 8 {
		return ErrPasswordLen
	}
	if len(newPassword) > 72 {
		return ErrPasswordTooLong
	}
	if newPassword != repeadPassword {
		return ErrOverlap
	}
	user, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(oldPassword)); err != nil {
		return ErrOldPassword
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	if err := s.repo.Update(ctx, id, string(newHash)); err != nil {
		return err
	}
	if err := s.sessions.RevokeAllSessions(ctx, id); err != nil {
		return err
	}
	return nil
}
