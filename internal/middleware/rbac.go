package middleware

import (
	"fmt"
	"net/http"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/authctx"
)

type UserRole string

const (
	RoleDefectologist     UserRole = "defectologist"
	RoleHeadDefectologist UserRole = "head_defectologist"
)

func RequireRole(roles ...UserRole) func(AppHandler) AppHandler {
	rolesList := make(map[UserRole]struct{}, len(roles))

	for _, role := range roles {
		rolesList[role] = struct{}{}
	}

	return func(next AppHandler) AppHandler {
		return func(w http.ResponseWriter, r *http.Request) error {
			userRole, err := authctx.RoleFromCtx(r.Context())
			if err != nil {
				return apperr.ErrInternal.WithError(fmt.Errorf("require role: no role in ctx: %w", err))
			}

			if _, allowed := rolesList[UserRole(userRole)]; !allowed {
				return apperr.ErrForbidden
			}

			return next(w, r)
		}
	}
}
