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

// ListWithTotal отдаёт страницу и общее число файлов из одного снапшота базы,
// иначе total не сходится с items при параллельных загрузках и удалениях.
func (r *Repository) ListWithTotal(ctx context.Context, q ListQuery) ([]ListItem, int, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("media repository list page begin: %w", err)
	}
	defer rollbackMediaTx(ctx, tx)

	items, err := listMedia(ctx, tx, q)
	if err != nil {
		return nil, 0, err
	}
	total, err := countMedia(ctx, tx, q)
	if err != nil {
		return nil, 0, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, 0, fmt.Errorf("media repository list page commit: %w", err)
	}
	return items, total, nil
}

func listMedia(ctx context.Context, tx pgx.Tx, q ListQuery) ([]ListItem, error) {
	var cursorCreatedAt *time.Time
	var cursorID uuid.UUID
	if q.Cursor != nil {
		cursorCreatedAt = &q.Cursor.CreatedAt
		cursorID = q.Cursor.ID
	}
	rows, err := tx.Query(ctx, listMediaQuery,
		q.OrgID, q.Query, q.MediaType, cursorCreatedAt, cursorID, q.Unused, q.Limit, q.UserID)
	if err != nil {
		return nil, fmt.Errorf("media repository list: %w", err)
	}
	defer rows.Close()

	var results []ListItem
	for rows.Next() {
		var result ListItem
		if err = rows.Scan(
			&result.ID, &result.OrgID, &result.UploaderID, &result.Name, &result.SHA256,
			&result.MIMEType, &result.MediaType, &result.SizeBytes, &result.MinIOKey,
			&result.CreatedAt, &result.CanDelete,
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

func countMedia(ctx context.Context, tx pgx.Tx, q ListQuery) (int, error) {
	var total int
	err := tx.QueryRow(ctx, countMediaQuery, q.OrgID, q.Query, q.MediaType, q.Unused).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("media repository count: %w", err)
	}
	return total, nil
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

func (r *Repository) DeleteBatch(
	ctx context.Context,
	userID uuid.UUID,
	ids []uuid.UUID,
	dryRun bool,
) (*BatchOutcome, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("media repository batch delete begin: %w", err)
	}
	defer rollbackMediaTx(ctx, tx)

	owned, err := lockOwnedMediaBatch(ctx, tx, userID, ids)
	if err != nil {
		return nil, err
	}
	referenced, err := referencedMediaBatch(ctx, tx, ownedIDs(owned))
	if err != nil {
		return nil, err
	}

	outcome := splitBatch(owned, referenced)
	if len(outcome.Deleted) > 0 && !dryRun {
		if _, err = tx.Exec(ctx, deleteMediaBatchQuery, outcome.Deleted); err != nil {
			return nil, fmt.Errorf("media repository batch delete rows: %w", err)
		}
		if _, err = tx.Exec(ctx, releaseMediaQuotaQuery, outcome.orgID, outcome.FreedBytes); err != nil {
			return nil, fmt.Errorf("media repository batch release quota: %w", err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("media repository batch delete commit: %w", err)
	}
	return outcome, nil
}

func splitBatch(owned []ownedMedia, referenced map[uuid.UUID]struct{}) *BatchOutcome {
	outcome := &BatchOutcome{Deleted: []uuid.UUID{}, InUse: []uuid.UUID{}}
	for _, item := range owned {
		if _, inUse := referenced[item.id]; inUse {
			outcome.InUse = append(outcome.InUse, item.id)
			continue
		}
		outcome.Deleted = append(outcome.Deleted, item.id)
		outcome.orgID = item.orgID
		outcome.FreedBytes += item.sizeBytes
	}
	return outcome
}

type ownedMedia struct {
	id        uuid.UUID
	orgID     uuid.UUID
	sizeBytes int64
}

func lockOwnedMediaBatch(ctx context.Context, tx pgx.Tx, userID uuid.UUID, ids []uuid.UUID) ([]ownedMedia, error) {
	rows, err := tx.Query(ctx, lockOwnedMediaBatchQuery, userID, ids)
	if err != nil {
		return nil, fmt.Errorf("media repository batch delete lock: %w", err)
	}
	defer rows.Close()

	var owned []ownedMedia
	for rows.Next() {
		var item ownedMedia
		if err = rows.Scan(&item.id, &item.orgID, &item.sizeBytes); err != nil {
			return nil, fmt.Errorf("media repository batch delete lock scan: %w", err)
		}
		owned = append(owned, item)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("media repository batch delete lock rows: %w", err)
	}
	return owned, nil
}

func referencedMediaBatch(ctx context.Context, tx pgx.Tx, ids []uuid.UUID) (map[uuid.UUID]struct{}, error) {
	referenced := make(map[uuid.UUID]struct{})
	if len(ids) == 0 {
		return referenced, nil
	}
	rows, err := tx.Query(ctx, referencedMediaBatchQuery, ids)
	if err != nil {
		return nil, fmt.Errorf("media repository batch delete usage: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id uuid.UUID
		if err = rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("media repository batch delete usage scan: %w", err)
		}
		referenced[id] = struct{}{}
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("media repository batch delete usage rows: %w", err)
	}
	return referenced, nil
}

func ownedIDs(owned []ownedMedia) []uuid.UUID {
	ids := make([]uuid.UUID, len(owned))
	for i, item := range owned {
		ids[i] = item.id
	}
	return ids
}
