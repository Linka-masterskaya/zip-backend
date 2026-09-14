package media

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// OrphanBatchResult describes media_files removed by one scanner batch.
type OrphanBatchResult struct {
	Count      int64
	Bytes      int64
	Candidates int64
}

// ClearSoftDeletedStudentAvatars removes stale avatar references from archived
// students. Archived students are not restorable, so their avatars must not keep
// media_files alive indefinitely.
func (r *Repository) ClearSoftDeletedStudentAvatars(ctx context.Context) (int64, error) {
	tag, err := r.pool.Exec(ctx, clearSoftDeletedStudentAvatarsQuery)
	if err != nil {
		return 0, fmt.Errorf("media orphan scanner clear archived avatars: %w", err)
	}
	return tag.RowsAffected(), nil
}

// DeleteOrphanBatch removes at most batchSize media_files that have no domain
// references and returns the corresponding organization quota in the same
// transaction. Fresh rows newer than createdBefore are ignored so an upload can
// attach its first domain reference before becoming eligible for cleanup. A recent
// storage_objects update protects re-uploads that reuse an older media_files row. Candidate
// rows are locked first; references are then checked
// again in a second statement, which gets a fresh READ COMMITTED snapshot. This
// protects files referenced by transactions that were already in flight while
// the candidate lock was being acquired. SKIP LOCKED lets concurrent scanners
// process different rows without waiting on each other.
func (r *Repository) DeleteOrphanBatch(
	ctx context.Context,
	batchSize int,
	createdBefore time.Time,
) (OrphanBatchResult, error) {
	if batchSize <= 0 {
		return OrphanBatchResult{}, fmt.Errorf("media orphan scanner batch size must be > 0")
	}

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return OrphanBatchResult{}, fmt.Errorf("media orphan scanner begin: %w", err)
	}
	defer rollbackMediaTx(ctx, tx)

	candidateIDs, err := selectOrphanCandidates(ctx, tx, batchSize, createdBefore)
	if err != nil {
		return OrphanBatchResult{}, err
	}

	result := OrphanBatchResult{Candidates: int64(len(candidateIDs))}
	if len(candidateIDs) > 0 {
		if err = tx.QueryRow(ctx, deleteLockedOrphansQuery, candidateIDs, createdBefore).Scan(&result.Count, &result.Bytes); err != nil {
			return OrphanBatchResult{}, fmt.Errorf("media orphan scanner delete batch: %w", err)
		}
	}

	if err = tx.Commit(ctx); err != nil {
		return OrphanBatchResult{}, fmt.Errorf("media orphan scanner commit: %w", err)
	}
	return result, nil
}

func selectOrphanCandidates(
	ctx context.Context,
	tx pgx.Tx,
	batchSize int,
	createdBefore time.Time,
) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, selectOrphanCandidatesQuery, batchSize, createdBefore)
	if err != nil {
		return nil, fmt.Errorf("media orphan scanner select candidates: %w", err)
	}
	defer rows.Close()

	ids := make([]uuid.UUID, 0, batchSize)
	for rows.Next() {
		var id uuid.UUID
		if err = rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("media orphan scanner scan candidate: %w", err)
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("media orphan scanner candidates rows: %w", err)
	}
	return ids, nil
}

const clearSoftDeletedStudentAvatarsQuery = `
	UPDATE students
	SET avatar_media_id = NULL
	WHERE deleted_at IS NOT NULL
	  AND avatar_media_id IS NOT NULL`

const selectOrphanCandidatesQuery = `
	/* media orphan scanner candidates */
	SELECT mf.id
	FROM media_files mf
	WHERE mf.created_at <= $2
		AND NOT EXISTS (
			SELECT 1
			FROM storage_objects so
			WHERE so.key = mf.minio_key
			  AND so.updated_at > $2
		)
		AND NOT EXISTS (
			SELECT 1 FROM media_usages mu WHERE mu.media_id = mf.id
		)
		AND NOT EXISTS (
			SELECT 1 FROM students s WHERE s.avatar_media_id = mf.id
		)
		AND NOT EXISTS (
			SELECT 1
			FROM tts_jobs j
			WHERE j.media_id = mf.id
			  AND j.status IN ('pending', 'in_progress')
		)
	ORDER BY mf.id
	LIMIT $1
	FOR UPDATE OF mf SKIP LOCKED`

const deleteLockedOrphansQuery = `
	WITH deleted AS (
		DELETE FROM media_files mf
		WHERE mf.id = ANY($1::uuid[])
			AND mf.created_at <= $2
			AND NOT EXISTS (
				SELECT 1
				FROM storage_objects so
				WHERE so.key = mf.minio_key
				  AND so.updated_at > $2
			)
			AND NOT EXISTS (
				SELECT 1 FROM media_usages mu WHERE mu.media_id = mf.id
			)
			AND NOT EXISTS (
				SELECT 1 FROM students s WHERE s.avatar_media_id = mf.id
			)
			AND NOT EXISTS (
				SELECT 1
				FROM tts_jobs j
				WHERE j.media_id = mf.id
				  AND j.status IN ('pending', 'in_progress')
			)
		RETURNING mf.org_id, mf.size_bytes
	), updated AS (
		UPDATE organizations o
		SET storage_used_bytes = GREATEST(o.storage_used_bytes - d.total_bytes, 0)
		FROM (
			SELECT org_id, SUM(size_bytes) AS total_bytes
			FROM deleted
			GROUP BY org_id
		) d
		WHERE o.id = d.org_id
	)
	SELECT count(*), COALESCE(SUM(size_bytes), 0)
	FROM deleted`
