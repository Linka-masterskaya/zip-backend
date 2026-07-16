package pack

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrPackNotFound        = errors.New("pack not found")
	ErrFolderNotAllowed    = errors.New("folder is not accessible")
	ErrInvalidPackMetadata = errors.New("invalid pack metadata")
)

type dbtx interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

// Repository persists packs in PostgreSQL.
type Repository struct {
	db dbtx
}

// NewRepository creates a PostgreSQL pack repository.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{db: pool}
}

// Create inserts a pack for an authenticated user and an owned folder.
func (r *Repository) Create(ctx context.Context, userID uuid.UUID, input CreateInput) (*Pack, error) {
	query := `
		INSERT INTO packs (org_id, owner_id, folder_id, title, config)
		SELECT u.org_id, u.id, f.id, $3, $4
		FROM users u
		JOIN folders f ON f.id = $2 AND f.owner_id = u.id
		WHERE u.id = $1
		  AND u.org_id IS NOT NULL
		  AND u.deleted_at IS NULL
		RETURNING ` + packColumns

	result, err := scanPack(r.db.QueryRow(ctx, query, userID, input.FolderID, input.Title, input.Config))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrFolderNotAllowed
	}
	if err != nil {
		return nil, fmt.Errorf("pack repository create: %w", err)
	}
	return result, nil
}

// Get returns a pack owned by the authenticated user in the same organization.
func (r *Repository) Get(ctx context.Context, userID, packID uuid.UUID) (*Pack, error) {
	query := `
		SELECT ` + qualifiedPackColumns + `
		FROM packs p
		JOIN users u ON u.id = $1
		WHERE p.id = $2
		  AND p.owner_id = u.id
		  AND p.org_id = u.org_id
		  AND u.deleted_at IS NULL`

	result, err := scanPack(r.db.QueryRow(ctx, query, userID, packID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrPackNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("pack repository get: %w", err)
	}
	return result, nil
}

// List returns packs from an owned folder for the authenticated user.
func (r *Repository) List(ctx context.Context, userID, folderID uuid.UUID) ([]*Pack, error) {
	allowed, err := r.folderAllowed(ctx, userID, folderID)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, ErrFolderNotAllowed
	}

	query := `
		SELECT ` + qualifiedPackColumns + `
		FROM packs p
		JOIN users u ON u.id = $1
		WHERE p.folder_id = $2
		  AND p.owner_id = u.id
		  AND p.org_id = u.org_id
		  AND u.deleted_at IS NULL
		ORDER BY p.updated_at DESC, p.id`

	rows, err := r.db.Query(ctx, query, userID, folderID)
	if err != nil {
		return nil, fmt.Errorf("pack repository list: %w", err)
	}
	defer rows.Close()

	packs := make([]*Pack, 0)
	for rows.Next() {
		item, scanErr := scanPack(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("pack repository list scan: %w", scanErr)
		}
		packs = append(packs, item)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("pack repository list rows: %w", err)
	}
	return packs, nil
}

// Update changes editable pack metadata without touching config.
func (r *Repository) Update(ctx context.Context, userID, packID uuid.UUID, input UpdateInput) (*Pack, error) {
	if input.FolderID != nil {
		allowed, err := r.folderAllowed(ctx, userID, *input.FolderID)
		if err != nil {
			return nil, err
		}
		if !allowed {
			return nil, ErrFolderNotAllowed
		}
	}

	metadata := filterPatch(input.FilterMetadata)
	query := `
		UPDATE packs p
		SET title = COALESCE($3::text, p.title),
		    folder_id = COALESCE($4::uuid, p.folder_id),
		    age_min = CASE WHEN $5::boolean THEN $6::int ELSE p.age_min END,
		    age_max = CASE WHEN $7::boolean THEN $8::int ELSE p.age_max END,
		    difficulty = CASE WHEN $9::boolean THEN $10::text ELSE p.difficulty END,
		    goals = COALESCE($11::text[], p.goals),
		    notes = CASE WHEN $12::boolean THEN $13::text ELSE p.notes END,
		    updated_at = now()
		FROM users u
		WHERE p.id = $2
		  AND u.id = $1
		  AND p.owner_id = u.id
		  AND p.org_id = u.org_id
		  AND u.deleted_at IS NULL
		RETURNING ` + qualifiedPackColumns

	result, err := scanPack(r.db.QueryRow(ctx, query,
		userID, packID, input.Title, input.FolderID,
		metadata.ageMin.Set, metadata.ageMin.Value,
		metadata.ageMax.Set, metadata.ageMax.Value,
		metadata.difficulty.Set, metadata.difficulty.Value, metadata.goals,
		input.Notes.Set, input.Notes.Value,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrPackNotFound
	}
	if isMetadataConstraintError(err) {
		return nil, ErrInvalidPackMetadata
	}
	if err != nil {
		return nil, fmt.Errorf("pack repository update: %w", err)
	}
	return result, nil
}

// Delete removes an owned pack from the authenticated user's organization.
func (r *Repository) Delete(ctx context.Context, userID, packID uuid.UUID) error {
	query := `
		DELETE FROM packs p
		USING users u
		WHERE p.id = $2
		  AND u.id = $1
		  AND p.owner_id = u.id
		  AND p.org_id = u.org_id
		  AND u.deleted_at IS NULL`

	tag, err := r.db.Exec(ctx, query, userID, packID)
	if err != nil {
		return fmt.Errorf("pack repository delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrPackNotFound
	}
	return nil
}

// Move moves an owned pack to another folder owned by the same user.
func (r *Repository) Move(ctx context.Context, userID, packID, folderID uuid.UUID) (*Pack, error) {
	allowed, err := r.folderAllowed(ctx, userID, folderID)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, ErrFolderNotAllowed
	}

	query := `
		UPDATE packs p
		SET folder_id = f.id, updated_at = now()
		FROM users u, folders f
		WHERE p.id = $2
		  AND u.id = $1
		  AND f.id = $3
		  AND f.owner_id = u.id
		  AND p.owner_id = u.id
		  AND p.org_id = u.org_id
		  AND u.deleted_at IS NULL
		RETURNING ` + qualifiedPackColumns

	result, err := scanPack(r.db.QueryRow(ctx, query, userID, packID, folderID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrPackNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("pack repository move: %w", err)
	}
	return result, nil
}

func (r *Repository) folderAllowed(ctx context.Context, userID, folderID uuid.UUID) (bool, error) {
	query := `
		SELECT EXISTS (
			SELECT 1
			FROM users u
			JOIN folders f ON f.owner_id = u.id
			WHERE u.id = $1
			  AND f.id = $2
			  AND u.org_id IS NOT NULL
			  AND u.deleted_at IS NULL
		)`
	var allowed bool
	if err := r.db.QueryRow(ctx, query, userID, folderID).Scan(&allowed); err != nil {
		return false, fmt.Errorf("pack repository folder access: %w", err)
	}
	return allowed, nil
}

type patchValues struct {
	ageMin     NullablePatch[int]
	ageMax     NullablePatch[int]
	difficulty NullablePatch[string]
	goals      []string
}

func filterPatch(metadata *FilterMetadataPatch) patchValues {
	if metadata == nil {
		return patchValues{}
	}
	values := patchValues{
		ageMin: metadata.AgeMin, ageMax: metadata.AgeMax,
		difficulty: metadata.Difficulty,
	}
	if metadata.Goals != nil {
		values.goals = *metadata.Goals
	}
	return values
}

func isMetadataConstraintError(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		return false
	}
	switch pgErr.ConstraintName {
	case "packs_age_min_chk", "packs_age_max_chk",
		"packs_age_range_chk", "packs_difficulty_chk":
		return true
	default:
		return false
	}
}

type rowScanner interface {
	Scan(...any) error
}

func scanPack(row rowScanner) (*Pack, error) {
	var result Pack
	err := row.Scan(
		&result.ID, &result.OrgID, &result.OwnerID, &result.FolderID,
		&result.Title, &result.Status, &result.AgeMin, &result.AgeMax,
		&result.Difficulty, &result.Goals, &result.Notes, &result.Config,
		&result.CreatedAt, &result.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

const packColumns = `
	id, org_id, owner_id, folder_id, title, status, age_min, age_max,
	difficulty, goals, notes, config, created_at, updated_at`

const qualifiedPackColumns = `
	p.id, p.org_id, p.owner_id, p.folder_id, p.title, p.status,
	p.age_min, p.age_max, p.difficulty, p.goals, p.notes, p.config,
	p.created_at, p.updated_at`
