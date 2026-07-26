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
	ErrPackPublished       = errors.New("pack is published")
	ErrAlreadyPublished    = errors.New("pack is published in another folder")
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
	result, err := scanPack(r.db.QueryRow(
		ctx, createPackQuery, userID, input.FolderID, input.Title, input.Config,
	))
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
	result, err := scanPack(r.db.QueryRow(ctx, getPackQuery, userID, packID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrPackNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("pack repository get: %w", err)
	}
	return result, nil
}

// List returns a bounded page of packs from an owned folder.
func (r *Repository) List(
	ctx context.Context,
	userID, folderID uuid.UUID,
	input ListInput,
) ([]*Pack, error) {
	allowed, err := r.folderAllowed(ctx, userID, folderID)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, ErrFolderNotAllowed
	}

	limit, offset := repositoryListBounds(input)
	rows, err := r.db.Query(ctx, listPacksQuery, userID, folderID, limit, offset)
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
	result, err := scanPack(r.db.QueryRow(ctx, updatePackQuery,
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
	tag, err := r.db.Exec(ctx, deletePackQuery, userID, packID)
	if err != nil {
		return fmt.Errorf("pack repository delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		var published bool
		err = r.db.QueryRow(ctx, packPublishedQuery, userID, packID).Scan(&published)
		if err != nil {
			return fmt.Errorf("pack repository delete state: %w", err)
		}
		if published {
			return ErrPackPublished
		}
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

	result, err := scanPack(r.db.QueryRow(ctx, movePackQuery, userID, packID, folderID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrPackNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("pack repository move: %w", err)
	}
	return result, nil
}

func (r *Repository) Publish(
	ctx context.Context,
	userID, packID, folderID uuid.UUID,
	admin bool,
) (*Pack, error) {
	result, err := scanPack(
		r.db.QueryRow(ctx, publishPackQuery, userID, packID, folderID, admin),
	)
	if err == nil {
		return result, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("pack repository publish: %w", err)
	}

	var otherFolder bool
	if stateErr := r.db.QueryRow(
		ctx, packPublishedInOtherFolderQuery, userID, packID, admin, folderID,
	).Scan(&otherFolder); stateErr != nil {
		return nil, fmt.Errorf("pack repository publish state: %w", stateErr)
	}
	if otherFolder {
		return nil, ErrAlreadyPublished
	}
	return nil, ErrFolderNotAllowed
}

func (r *Repository) Unpublish(
	ctx context.Context,
	userID, packID uuid.UUID,
	admin bool,
) error {
	tag, err := r.db.Exec(ctx, unpublishPackQuery, userID, packID, admin)
	if err != nil {
		return fmt.Errorf("pack repository unpublish: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrPackNotFound
	}
	return nil
}

func repositoryListBounds(input ListInput) (int, int) {
	const defaultLimit = 50
	const maxLimit = 100

	limit := input.Limit
	if limit < 1 || limit > maxLimit {
		limit = defaultLimit
	}
	offset := input.Offset
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func (r *Repository) folderAllowed(ctx context.Context, userID, folderID uuid.UUID) (bool, error) {
	var allowed bool
	if err := r.db.QueryRow(ctx, folderAllowedQuery, userID, folderID).Scan(&allowed); err != nil {
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
		&result.LibraryFolderID, &result.PublishedAt,
		&result.Title, &result.Status, &result.AgeMin, &result.AgeMax,
		&result.Difficulty, &result.Goals, &result.Notes, &result.Config,
		&result.CreatedAt, &result.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &result, nil
}
