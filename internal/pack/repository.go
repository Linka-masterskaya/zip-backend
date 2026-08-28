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
	ErrPackNotFound                 = errors.New("pack not found")
	ErrDuplicateDestinationRequired = errors.New("destination folder is required when duplicating another user's pack")
	ErrFolderNotAllowed             = errors.New("folder is not accessible")
	ErrMediaNotAllowed              = errors.New("media is not accessible")
	ErrStudentNotAllowed            = errors.New("student is not accessible")
	ErrInvalidPackMetadata          = errors.New("invalid pack metadata")
	ErrPackPublished                = errors.New("pack is published")
	ErrAlreadyPublished             = errors.New("pack is published in another folder")
	ErrAdaptationNotFound           = errors.New("pack adaptation not found")
)

const duplicateTitleSuffix = " (копия)"

// Repository persists packs in PostgreSQL.
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository creates a PostgreSQL pack repository.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// Create inserts a pack for an authenticated user and an owned folder.
func (r *Repository) Create(ctx context.Context, userID uuid.UUID, input CreateInput) (*Pack, error) {
	result, err := scanPack(r.pool.QueryRow(
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

// Duplicate atomically creates an independent draft pack that reuses media objects.
func (r *Repository) Duplicate(
	ctx context.Context,
	userID, sourcePackID uuid.UUID,
	input DuplicateInput,
) (*Pack, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("pack duplicate begin: %w", err)
	}
	defer rollbackPackTx(ctx, tx)

	source, err := scanPack(tx.QueryRow(ctx, lockDuplicateSourceQuery, userID, sourcePackID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrPackNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("pack duplicate lock source: %w", err)
	}

	targetFolderID, err := packCopyDestinationFolderID(source, userID, input)
	if err != nil {
		return nil, err
	}
	var lockedFolderID uuid.UUID
	err = tx.QueryRow(
		ctx, lockDuplicateFolderQuery, userID, targetFolderID, source.OrgID,
	).Scan(&lockedFolderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrFolderNotAllowed
	}
	if err != nil {
		return nil, fmt.Errorf("pack duplicate lock folder: %w", err)
	}

	result, err := scanPack(tx.QueryRow(
		ctx,
		insertDuplicatePackQuery,
		source.OrgID,
		userID,
		lockedFolderID,
		source.Title+duplicateTitleSuffix,
		source.Age,
		source.Difficulty,
		source.Goals,
		source.Notes,
		source.Config,
	))
	if err != nil {
		return nil, fmt.Errorf("pack duplicate insert: %w", err)
	}
	if _, err = tx.Exec(ctx, copyDuplicateMediaUsagesQuery, sourcePackID, result.ID); err != nil {
		return nil, fmt.Errorf("pack duplicate media usages: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("pack duplicate commit: %w", err)
	}
	return result, nil
}

func packCopyDestinationFolderID(source *Pack, userID uuid.UUID, input DuplicateInput) (uuid.UUID, error) {
	if input.FolderID != nil {
		return *input.FolderID, nil
	}
	if source.OwnerID != userID {
		return uuid.Nil, ErrDuplicateDestinationRequired
	}
	return source.FolderID, nil
}

// Get returns a pack owned by the authenticated user or published within the same organization.
func (r *Repository) Get(ctx context.Context, userID, packID uuid.UUID) (*Pack, error) {
	result, err := scanPack(r.pool.QueryRow(ctx, getPackQuery, userID, packID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrPackNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("pack repository get: %w", err)
	}
	return result, nil
}

// GetForPublication returns a same-organization pack that the caller may publish.
func (r *Repository) GetForPublication(
	ctx context.Context,
	userID, packID uuid.UUID,
	admin bool,
) (*Pack, error) {
	result, err := scanPack(r.pool.QueryRow(
		ctx, getPackForPublicationQuery, userID, packID, admin,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrPackNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("pack repository get for publication: %w", err)
	}
	return result, nil
}

// List returns a bounded page of packs from all folders accessible to the user.
func (r *Repository) List(
	ctx context.Context,
	userID uuid.UUID,
	input ListInput,
) ([]*ListItem, error) {
	limit, offset := repositoryListBounds(input)
	rows, err := r.pool.Query(
		ctx,
		listPacksQuery(input.SortBy, input.Order),
		userID,
		input.Query,
		input.Age,
		input.Difficulty,
		input.Section,
		input.StudentID,
		limit,
		offset,
	)
	if err != nil {
		return nil, fmt.Errorf("pack repository list: %w", err)
	}
	defer rows.Close()

	packs := make([]*ListItem, 0)
	for rows.Next() {
		item, scanErr := scanListItem(rows)
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

// Count returns the total number of pack placements matching the same filters as List.
func (r *Repository) Count(
	ctx context.Context,
	userID uuid.UUID,
	input ListInput,
) (int, error) {
	var total int
	err := r.pool.QueryRow(
		ctx,
		countPacksQuery,
		userID,
		input.Query,
		input.Age,
		input.Difficulty,
		input.Section,
		input.StudentID,
	).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("pack repository count: %w", err)
	}

	return total, nil
}

// Update changes editable pack metadata without touching config.
func (r *Repository) Update(ctx context.Context, userID, packID uuid.UUID, input UpdateInput) (*Pack, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("pack repository update begin: %w", err)
	}
	defer rollbackPackTx(ctx, tx)

	var orgID uuid.UUID
	err = tx.QueryRow(ctx, lockPackForUpdateQuery, userID, packID).Scan(&orgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrPackNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("pack repository update lock pack: %w", err)
	}
	if input.FolderID != nil {
		var folderID uuid.UUID
		err = tx.QueryRow(
			ctx, lockUpdateFolderQuery, userID, *input.FolderID, orgID,
		).Scan(&folderID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrFolderNotAllowed
		}
		if err != nil {
			return nil, fmt.Errorf("pack repository update lock folder: %w", err)
		}
	}

	metadata := filterPatch(input.FilterMetadata)
	result, err := scanPack(tx.QueryRow(ctx, updatePackQuery,
		userID, packID, input.Title, input.FolderID,
		metadata.age.Set, metadata.age.Value,
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
	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("pack repository update commit: %w", err)
	}
	return result, nil
}

// Delete removes an owned pack from the authenticated user's organization.
func (r *Repository) Delete(ctx context.Context, userID, packID uuid.UUID) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("pack repository delete begin: %w", err)
	}
	defer rollbackPackTx(ctx, tx)
	var published bool
	err = tx.QueryRow(ctx, lockPackForDeleteQuery, userID, packID).Scan(&published)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrPackNotFound
	}
	if err != nil {
		return fmt.Errorf("pack repository delete lock: %w", err)
	}
	if published {
		return ErrPackPublished
	}
	adaptationIDs := make([]uuid.UUID, 0)
	rows, queryErr := tx.Query(ctx, adaptationIDsForPackQuery, packID)
	if queryErr != nil {
		return fmt.Errorf("pack repository delete adaptations: %w", queryErr)
	}
	for rows.Next() {
		var adaptationID uuid.UUID
		if queryErr = rows.Scan(&adaptationID); queryErr != nil {
			rows.Close()
			return fmt.Errorf("pack repository delete adaptation scan: %w", queryErr)
		}
		adaptationIDs = append(adaptationIDs, adaptationID)
	}
	queryErr = rows.Err()
	rows.Close()
	if queryErr != nil {
		return fmt.Errorf("pack repository delete adaptation rows: %w", queryErr)
	}
	if _, err = tx.Exec(ctx, deletePackMediaUsagesQuery, packID); err != nil {
		return fmt.Errorf("pack repository delete media usages: %w", err)
	}
	if len(adaptationIDs) > 0 {
		if _, err = tx.Exec(ctx, deleteAdaptationUsagesForIDsQuery, adaptationIDs); err != nil {
			return fmt.Errorf("pack repository delete adaptation usages: %w", err)
		}
	}
	if _, err = tx.Exec(ctx, deletePackQuery, userID, packID); err != nil {
		return fmt.Errorf("pack repository delete: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("pack repository delete commit: %w", err)
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

	result, err := scanPack(r.pool.QueryRow(ctx, movePackQuery, userID, packID, folderID))
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
		r.pool.QueryRow(ctx, publishPackQuery, userID, packID, folderID, admin),
	)
	if err == nil {
		return result, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("pack repository publish: %w", err)
	}

	var otherFolder bool
	if stateErr := r.pool.QueryRow(
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
	tag, err := r.pool.Exec(ctx, unpublishPackQuery, userID, packID, admin)
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
	if err := r.pool.QueryRow(ctx, folderAllowedQuery, userID, folderID).Scan(&allowed); err != nil {
		return false, fmt.Errorf("pack repository folder access: %w", err)
	}
	return allowed, nil
}

func rollbackPackTx(ctx context.Context, tx pgx.Tx) {
	err := tx.Rollback(ctx)
	if err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		return
	}
}

type patchValues struct {
	age        NullablePatch[int]
	difficulty NullablePatch[string]
	goals      []string
}

func filterPatch(metadata *FilterMetadataPatch) patchValues {
	if metadata == nil {
		return patchValues{}
	}
	values := patchValues{
		age:        metadata.Age,
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
	case "packs_age_chk", "packs_difficulty_chk":
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
		&result.Title, &result.Status, &result.Age,
		&result.Difficulty, &result.Goals, &result.Notes, &result.Config,
		&result.CreatedAt, &result.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func scanListItem(row rowScanner) (*ListItem, error) {
	var result ListItem
	err := row.Scan(
		&result.ID, &result.OrgID, &result.OwnerID, &result.FolderID,
		&result.LibraryFolderID, &result.PublishedAt,
		&result.Title, &result.Status, &result.Age,
		&result.Difficulty, &result.Goals, &result.Notes, &result.Config,
		&result.IsFavorite, &result.Section,
		&result.CreatedAt, &result.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &result, nil
}
