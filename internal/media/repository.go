package media

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) UserOrg(ctx context.Context, userID uuid.UUID) (uuid.UUID, error) {
	var orgID uuid.UUID
	err := r.pool.QueryRow(ctx, userOrgQuery, userID).Scan(&orgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("media repository user org: %w", err)
	}
	return orgID, nil
}

func (r *Repository) Upsert(ctx context.Context, input File) (*File, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("media repository begin: %w", err)
	}
	defer rollbackMediaTx(ctx, tx)

	var lockedOrg uuid.UUID
	err = tx.QueryRow(ctx, lockOrgQuery, input.OrgID).Scan(&lockedOrg)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("media repository lock organization: %w", err)
	}

	var result File
	err = tx.QueryRow(ctx, findByDigestQuery,
		input.OrgID, input.SHA256,
	).Scan(
		&result.ID, &result.OrgID, &result.UploaderID, &result.Name, &result.SHA256,
		&result.MIMEType, &result.MediaType, &result.SizeBytes, &result.MinIOKey, &result.CreatedAt,
	)
	if err == nil {
		if err = tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("media repository commit existing: %w", err)
		}
		return &result, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("media repository find existing: %w", err)
	}

	var quotaAccepted bool
	err = tx.QueryRow(ctx, reserveQuotaQuery, input.OrgID, input.SizeBytes).Scan(&quotaAccepted)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrQuotaExceeded
	}
	if err != nil {
		return nil, fmt.Errorf("media repository reserve quota: %w", err)
	}

	err = tx.QueryRow(ctx, insertMediaQuery,
		input.OrgID, input.UploaderID, input.Name, input.SHA256, input.MIMEType,
		input.MediaType, input.SizeBytes, input.MinIOKey,
	).Scan(
		&result.ID, &result.OrgID, &result.UploaderID, &result.Name, &result.SHA256,
		&result.MIMEType, &result.MediaType, &result.SizeBytes, &result.MinIOKey, &result.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("media repository insert: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("media repository commit: %w", err)
	}
	return &result, nil
}

func rollbackMediaTx(ctx context.Context, tx pgx.Tx) {
	err := tx.Rollback(ctx)
	if err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		return
	}
}

func (r *Repository) GetAccessible(ctx context.Context, userID, mediaID uuid.UUID) (*File, error) {
	var result File
	err := r.pool.QueryRow(ctx, getAccessibleMediaQuery, userID, mediaID).Scan(
		&result.ID, &result.OrgID, &result.UploaderID, &result.Name, &result.SHA256,
		&result.MIMEType, &result.MediaType, &result.SizeBytes, &result.MinIOKey, &result.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("media repository get: %w", err)
	}
	return &result, nil
}

func (r *Repository) List(
	ctx context.Context,
	orgID uuid.UUID,
	query, mediaType string,
	cursor *mediaCursor,
	limit int,
) ([]File, error) {
	var cursorCreatedAt *time.Time
	var cursorID uuid.UUID
	if cursor != nil {
		cursorCreatedAt = &cursor.CreatedAt
		cursorID = cursor.ID
	}
	rows, err := r.pool.Query(ctx, listMediaQuery, orgID, query, mediaType, cursorCreatedAt, cursorID, limit)
	if err != nil {
		return nil, fmt.Errorf("media repository list: %w", err)
	}
	defer rows.Close()

	var results []File
	for rows.Next() {
		var result File
		if err = rows.Scan(
			&result.ID, &result.OrgID, &result.UploaderID, &result.Name, &result.SHA256,
			&result.MIMEType, &result.MediaType, &result.SizeBytes, &result.MinIOKey, &result.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("media repository list scan: %w", err)
		}
		results = append(results, result)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("media repository list rows: %w", err)
	}
	return results, nil
}

func (r *Repository) Delete(
	ctx context.Context,
	userID, mediaID uuid.UUID,
) (*File, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("media repository delete begin: %w", err)
	}
	defer rollbackMediaTx(ctx, tx)

	var result File
	err = tx.QueryRow(ctx, lockOwnedMediaQuery, userID, mediaID).Scan(
		&result.ID, &result.OrgID, &result.UploaderID, &result.Name, &result.SHA256,
		&result.MIMEType, &result.MediaType, &result.SizeBytes, &result.MinIOKey, &result.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("media repository delete lock: %w", err)
	}
	var inUse bool
	if err = tx.QueryRow(ctx, mediaInUseQuery, mediaID).Scan(&inUse); err != nil {
		return nil, fmt.Errorf("media repository delete usage: %w", err)
	}
	if inUse {
		return nil, ErrInUse
	}
	if _, err = tx.Exec(ctx, deleteMediaQuery, mediaID); err != nil {
		return nil, fmt.Errorf("media repository delete row: %w", err)
	}
	if _, err = tx.Exec(ctx, releaseMediaQuotaQuery, result.OrgID, result.SizeBytes); err != nil {
		return nil, fmt.Errorf("media repository release quota: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("media repository delete commit: %w", err)
	}
	return &result, nil
}
