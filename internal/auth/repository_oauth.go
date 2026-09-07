package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// providerYandex — единственный поддерживаемый провайдер; тем же значением
// ограничен CHECK в auth_identities.
const providerYandex = "yandex"

type CreateIdentityParams struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	Provider    string
	ProviderUID string
}

func (r *authRepo) CreateIdentity(ctx context.Context, params CreateIdentityParams) error {
	query := `
		INSERT INTO auth_identities (id, user_id, provider, provider_uid)
		VALUES ($1, $2, $3, $4)
	`

	_, err := r.db.Exec(ctx, query, params.ID, params.UserID, params.Provider, params.ProviderUID)
	if err != nil {
		return fmt.Errorf("authRepo.CreateIdentity: %w", err)
	}
	return nil
}

func (r *authRepo) GetUserByIdentity(
	ctx context.Context,
	provider, providerUID string,
) (*User, error) {
	var user User

	query := `
		SELECT
			u.id,
			u.org_id,
			ac.password_hash,
			ac.role,
			u.email_verified
		FROM auth_identities ai
		JOIN users u ON u.id = ai.user_id
		JOIN auth_cred ac ON ac.user_id = u.id
		WHERE ai.provider = $1
			AND ai.provider_uid = $2
			AND u.deleted_at IS NULL
	`

	err := r.db.QueryRow(ctx, query, provider, providerUID).Scan(
		&user.ID,
		&user.OrgID,
		&user.PasswordHash,
		&user.Role,
		&user.EmailVerified,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("authRepo.GetUserByIdentity: %w", err)
	}

	return &user, nil
}
