package auth

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DBTX interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type RepositoryInterface interface {
	FindIdentityByProviderUID(ctx context.Context, provider, providerUID string) (*UserIdentity, error)
	FindUserByID(ctx context.Context, id uuid.UUID) (*User, error)
	FindUserCredByEmailHash(ctx context.Context, emailHash []byte) (*UserCred, error)
	FindUserCredByUserID(ctx context.Context, userID uuid.UUID) (*UserCred, error)
	CreateUser(ctx context.Context, params CreateUserParams) error
	CreateAuthCred(ctx context.Context, params CreateAuthCredParams) error
	CreateIdentity(ctx context.Context, identity *UserIdentity) error
	UpdateUser(ctx context.Context, user *User) error
}

type TxRepository interface {
	withTx(tx pgx.Tx) RepositoryInterface
	Begin(ctx context.Context) (pgx.Tx, error)
}

type Repository struct {
	db   DBTX
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{db: pool, pool: pool}
}

func (r *Repository) FindIdentityByProviderUID(ctx context.Context, provider, providerUID string) (*UserIdentity, error) {
	query := `
	SELECT id, user_id, provider, provider_uid, created_at
	FROM auth_identities
	WHERE provider = $1 AND provider_uid = $2
	`
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

func (r *Repository) FindUserByID(ctx context.Context, id uuid.UUID) (*User, error) {
	query := `
	SELECT id, display_name, avatar_key, org_id, created_at, updated_at, deleted_at
	FROM users
	WHERE id = $1 AND deleted_at IS NULL
	`
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

func (r *Repository) FindUserCredByEmailHash(ctx context.Context, emailHash []byte) (*UserCred, error) {
	query := `
	SELECT user_id, email_hash, email_encrypted, password_hash, role
	FROM auth_cred
	WHERE email_hash = $1
	`
	var cred UserCred
	err := r.db.QueryRow(ctx, query, emailHash).Scan(
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

func (r *Repository) FindUserCredByUserID(ctx context.Context, userID uuid.UUID) (*UserCred, error) {
	query := `
	SELECT user_id, email_hash, email_encrypted, password_hash, role
	FROM auth_cred
	WHERE user_id = $1
	`
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

func (r *Repository) CreateUser(ctx context.Context, params CreateUserParams) error {
	query := `INSERT INTO users(id, org_id, display_name) VALUES ($1, $2, $3)`
	var orgID any
	if params.OrganizationID == nil {
		orgID = nil
	} else {
		orgID = *params.OrganizationID
	}
	_, err := r.db.Exec(ctx, query, params.ID, orgID, params.Name)
	return err
}

func (r *Repository) CreateAuthCred(ctx context.Context, params CreateAuthCredParams) error {
	query := `
INSERT INTO auth_cred (
user_id, 
email_hash,
email_encrypted,
password_hash, 
role)
VALUES (
$1,
$2,
$3,
$4,
$5)`

	_, err := r.db.Exec(ctx, query, params.UserID, params.EmailHash, params.EmailEncrypted, params.PasswordHash, params.Role)

	return err
}

func (r *Repository) CreateIdentity(ctx context.Context, identity *UserIdentity) error {
	query := `
	INSERT INTO auth_identities (id, user_id, provider, provider_uid)
	VALUES ($1, $2, $3, $4)
	`
	_, err := r.db.Exec(ctx, query,
		identity.ID, identity.UserID, identity.Provider, identity.ProviderUID,
	)
	return err
}

func (r *Repository) UpdateUser(ctx context.Context, user *User) error {
	query := `
	UPDATE users
	SET display_name = $1, updated_at = now()
	WHERE id = $2
	`
	_, err := r.db.Exec(ctx, query, user.Name, user.ID)
	return err
}

func (r *Repository) Begin(ctx context.Context) (pgx.Tx, error) {
	return r.pool.Begin(ctx)
}

func (r *Repository) withTx(tx pgx.Tx) RepositoryInterface {
	return &Repository{db: tx, pool: r.pool}
}
