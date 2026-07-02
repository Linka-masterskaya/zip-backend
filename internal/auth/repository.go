package auth

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// Эта функция ищет запись в таблице auth_identities по тому, как пользователь известен внешнему сервису (Яндекс, Google, Apple)
func (r *Repository) FindIdentityByProviderUID(ctx context.Context, provider, providerUID string) (*UserIdentity, error) {
	query := `SELECT id, user_id, provider, provider_uid, created_at
              FROM auth_identities WHERE provider = $1 AND provider_uid = $2`
	var identity UserIdentity
	err := r.db.QueryRow(ctx, query, provider, providerUID).Scan(
		&identity.ID, &identity.UserID, &identity.Provider,
		&identity.ProviderUID, &identity.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &identity, nil
}

// Эта функция ищет профиль пользователя по его внутреннему UUID, который есть только в нашей системе
func (r *Repository) FindUserByID(ctx context.Context, id uuid.UUID) (*User, error) {
	query := `SELECT id, name, avatar_key, organization_id, created_at, updated_at, deleted_at
              FROM users WHERE id = $1 AND deleted_at IS NULL`
	var user User
	err := r.db.QueryRow(ctx, query, id).Scan(
		&user.ID, &user.Name, &user.AvatarKey, &user.OrgID,
		&user.CreatedAt, &user.UpdatedAt, &user.DeletedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &user, nil
}

// FindUserAuthByEmail ищет UserAuth по email (хешированному)
func (r *Repository) FindUserAuthByEmail(ctx context.Context, email string) (*UserCred, error) {
	// TODO: использовать хеш email вместо plain text
	query := `SELECT user_id, email_hash, email_encrypted, password_hash, role
              FROM auth_cred WHERE email_hash = $1`
	var cred UserCred
	err := r.db.QueryRow(ctx, query, email).Scan(
		&cred.UserID, &cred.EmailHash, &cred.EmailEncrypted,
		&cred.PasswordHash, &cred.Role,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &cred, nil
}

// FindUserAuthByUserID ищет UserAuth по user_id
func (r *Repository) FindUserAuthByUserID(ctx context.Context, userID uuid.UUID) (*UserCred, error) {
	query := `SELECT user_id, email_hash, email_encrypted, password_hash, role
              FROM auth_cred WHERE user_id = $1`
	var cred UserCred
	err := r.db.QueryRow(ctx, query, userID).Scan(
		&cred.UserID, &cred.EmailHash, &cred.EmailEncrypted,
		&cred.PasswordHash, &cred.Role,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &cred, nil
}

// CreateUser создаёт нового пользователя
func (r *Repository) CreateUser(ctx context.Context, user *User) error {
	query := `INSERT INTO users (id, name) VALUES ($1, $2)`
	_, err := r.db.Exec(ctx, query, user.ID, user.Name)
	return err
}

// CreateUserAuth создаёт UserAuth
func (r *Repository) CreateUserAuth(ctx context.Context, cred *UserCred) error {
	query := `INSERT INTO auth_cred (user_id, email_hash, email_encrypted, role)
              VALUES ($1, $2, $3, $4)`
	_, err := r.db.Exec(ctx, query, cred.UserID, cred.EmailHash, cred.EmailEncrypted, cred.Role)
	return err
}

// CreateIdentity создаёт UserIdentity
func (r *Repository) CreateIdentity(ctx context.Context, identity *UserIdentity) error {
	query := `INSERT INTO auth_identities (id, user_id, provider, provider_uid)
              VALUES ($1, $2, $3, $4)`
	_, err := r.db.Exec(ctx, query, identity.ID, identity.UserID, identity.Provider, identity.ProviderUID)
	return err
}

func (r *Repository) UpdateUser(ctx context.Context, user *User) error {
	query := `UPDATE users SET name = $1, updated_at = now() WHERE id = $2`
	_, err := r.db.Exec(ctx, query, user.Name, user.ID)
	return err
}

func (r *Repository) UpdateUserAuth(ctx context.Context, cred *UserCred) error {
	query := `UPDATE auth_cred SET role = $1 WHERE user_id = $2`
	_, err := r.db.Exec(ctx, query, cred.Role, cred.UserID)
	return err
}
