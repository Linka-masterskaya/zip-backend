package authctx

import (
	"context"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/google/uuid"
)

type ctxKey int

const (
	userIDKey ctxKey = iota
	userRoleKey
)

func SetUserIDToCtx(ctx context.Context, userID uuid.UUID) context.Context {
	return context.WithValue(ctx, userIDKey, userID)
}

func UserIDFromCtx(ctx context.Context) (uuid.UUID, error) {
	userID, ok := ctx.Value(userIDKey).(uuid.UUID)
	if !ok {
		return uuid.Nil, apperr.ErrUnauthorized
	}

	return userID, nil
}

func SetRoleToCtx(ctx context.Context, userRole string) context.Context {
	return context.WithValue(ctx, userRoleKey, userRole)
}

func RoleFromCtx(ctx context.Context) (string, error) {
	userRole, ok := ctx.Value(userRoleKey).(string)
	if !ok {
		return "", apperr.ErrUnauthorized
	}

	return userRole, nil
}
