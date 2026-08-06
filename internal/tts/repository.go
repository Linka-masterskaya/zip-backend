package tts

import (
	"context"
	"errors"
	"fmt"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
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

const createSucceededJob = `
INSERT INTO tts_jobs(text, voice, status, minio_key, sha256, size_bytes)
VALUES($1, $2, 'succeeded', $3, $4, $5)
RETURNING id`

func (r *Repository) CreateSucceededJob(ctx context.Context, entry *BankEntry) (uuid.UUID, error) {
	var jobID uuid.UUID
	err := r.pool.QueryRow(ctx,
		createSucceededJob,
		entry.Text,
		entry.Voice,
		entry.MinioKey,
		entry.SHA256,
		entry.SizeBytes).Scan(&jobID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("tts.CreateSucceededJob: %w", err)
	}

	return jobID, nil
}

const completeJob = `
UPDATE tts_jobs
SET status='succeeded', minio_key=$2, sha256=$3, size_bytes=$4, updated_at=NOW()
WHERE id = $1`

func (r *Repository) CompleteJob(ctx context.Context, jobID uuid.UUID, minioKey, sha256 string, sizeBytes int64) error {
	_, err := r.pool.Exec(ctx, completeJob,
		jobID,
		minioKey,
		sha256,
		sizeBytes)
	if err != nil {
		return fmt.Errorf("tts.CompleteJob: %w", err)
	}

	return nil
}

const updateStatusTTS = `
UPDATE tts_jobs
SET status=$2, updated_at=NOW()
WHERE id = $1`

func (r *Repository) UpdateStatusTTS(ctx context.Context, jobID uuid.UUID, status string) error {
	_, err := r.pool.Exec(ctx,
		updateStatusTTS,
		jobID,
		status)
	if err != nil {
		return fmt.Errorf("tts.UpdateStatusTTS: %w", err)
	}

	return nil
}

const insertJobQuery = `
INSERT INTO tts_jobs (text, voice, status)
VALUES ($1, $2, 'pending')
ON CONFLICT (text, voice) WHERE status IN ('pending', 'in_progress') DO NOTHING
RETURNING id`

const findInflightJobQuery = `
SELECT id FROM tts_jobs
WHERE text = $1 AND voice = $2 AND status IN ('pending', 'in_progress')`

func (r *Repository) CreateOrGetInflightJob(ctx context.Context, text, voice string) (uuid.UUID, bool, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("tts.CreateOrGetInflightJob begin tx: %w", err)
	}
	defer func() {
		rollback(ctx, tx)
	}()

	var returnedID uuid.UUID
	var isNew bool

	err = tx.QueryRow(ctx, insertJobQuery, text, voice).Scan(&returnedID)
	switch {
	case err == nil:
		isNew = true
	case errors.Is(err, pgx.ErrNoRows):
		if err = tx.QueryRow(ctx, findInflightJobQuery, text, voice).Scan(&returnedID); err != nil {
			return uuid.Nil, false, fmt.Errorf("tts.CreateOrGetInflightJob find job: %w", err)
		}
	default:
		return uuid.Nil, false, fmt.Errorf("tts.CreateOrGetInflightJob insert job: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return uuid.Nil, false, fmt.Errorf("tts.CreateOrGetInflightJob commit: %w", err)
	}
	return returnedID, isNew, nil
}

const getFromBankQuery = `
UPDATE audio_bank
SET last_used_at = now()
WHERE text = $1 AND voice = $2
RETURNING text, voice, minio_key, sha256, size_bytes`

func (r *Repository) GetFromBank(ctx context.Context, text, voice string) (*BankEntry, error) {
	var entry BankEntry
	err := r.pool.QueryRow(ctx, getFromBankQuery, text, voice).Scan(
		&entry.Text,
		&entry.Voice,
		&entry.MinioKey,
		&entry.SHA256,
		&entry.SizeBytes,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.ErrNotFound
		}
		return nil, fmt.Errorf("tts.GetFromBank: %w", err)
	}
	return &entry, nil
}

const putToBank = `
INSERT INTO audio_bank
(minio_key, text, voice, sha256, size_bytes)
VALUES($1, $2, $3, $4, $5)
ON CONFLICT (text, voice) DO NOTHING`

func (r *Repository) PutToBank(ctx context.Context, entry *BankEntry) error {
	_, err := r.pool.Exec(ctx, putToBank,
		entry.MinioKey,
		entry.Text,
		entry.Voice,
		entry.SHA256,
		entry.SizeBytes)
	if err != nil {
		return fmt.Errorf("tts.PutToBank: %w", err)
	}

	return nil
}

func (r *Repository) GetOrgID(ctx context.Context, userID uuid.UUID) (uuid.UUID, error) {
	var orgID uuid.UUID
	err := r.pool.QueryRow(ctx, `SELECT org_id 
	FROM users 
	WHERE id=$1 
	AND deleted_at IS NULL AND org_id IS NOT NULL`, userID).Scan(&orgID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, fmt.Errorf("tts.GetOrgID: user has no org: %w", err)
		}
		return uuid.Nil, fmt.Errorf("tts.GetOrgID: %w", err)
	}

	return orgID, nil
}

const getJob = `
SELECT status, minio_key, sha256, size_bytes
FROM tts_jobs
WHERE id=$1`

func (r *Repository) GetJob(ctx context.Context, jobID uuid.UUID) (*JobDetails, error) {
	var jobDetails JobDetails
	err := r.pool.QueryRow(ctx, getJob, jobID).Scan(
		&jobDetails.Status,
		&jobDetails.MinioKey,
		&jobDetails.SHA256,
		&jobDetails.SizeBytes,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.ErrNotFound
		}
		return nil, fmt.Errorf("tts.GetJob: %w", err)
	}

	return &jobDetails, nil
}

const (
	insertMediaFromTTS = `
WITH ins AS (
	INSERT INTO media_files (org_id, uploader_id, sha256, mime_type, size_bytes, minio_key)
	VALUES ($1, $2, $3, $4, $5, $6)
	ON CONFLICT (minio_key) DO NOTHING
	RETURNING id
)
SELECT id FROM ins
UNION ALL
SELECT id FROM media_files WHERE minio_key = $6
LIMIT 1`

	updateOrgQuota = `
UPDATE organizations 
	SET storage_used_bytes = storage_used_bytes + $2 
	WHERE id = $1 
	AND storage_used_bytes + $2 <= storage_quota_bytes 
	RETURNING true`
)

func (r *Repository) CreateMediaFile(ctx context.Context, orgID, userID uuid.UUID, job *JobDetails) (uuid.UUID, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return uuid.Nil, fmt.Errorf("tts.CreateMediaFile: %w", err)
	}
	defer func() {
		rollback(ctx, tx)
	}()

	var org uuid.UUID
	err = tx.QueryRow(ctx,
		`SELECT id FROM organizations WHERE id = $1 FOR UPDATE`,
		orgID).Scan(&org)
	if err != nil {
		return uuid.Nil, fmt.Errorf("tts.CreateMediaFile: %w", err)
	}

	var mediaID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM media_files WHERE minio_key = $1`, job.MinioKey).Scan(&mediaID)
	if err == nil {
		return mediaID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("tts.CreateMediaFile: %w", err)
	}

	var quota bool
	err = tx.QueryRow(ctx, updateOrgQuota, orgID, job.SizeBytes).Scan(&quota)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrQuotaExceeded
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("tts.CreateMediaFile: %w", err)
	}

	err = tx.QueryRow(ctx, insertMediaFromTTS,
		orgID, userID, job.SHA256, job.MimeType, job.SizeBytes, job.MinioKey,
	).Scan(&mediaID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("tts.CreateMediaFile: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("tts.CreateMediaFile: %w", err)
	}
	return mediaID, nil
}

func rollback(ctx context.Context, tx pgx.Tx) {
	err := tx.Rollback(ctx)
	if err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		return
	}
}
