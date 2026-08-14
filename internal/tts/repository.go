package tts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

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

func (r *Repository) UpsertVoices(ctx context.Context, voices []Voice) error {
	data, err := json.Marshal(voices)
	if err != nil {
		return fmt.Errorf("tts.UpsertVoices: marshal: %w", err)
	}

	_, err = r.pool.Exec(ctx, upsertCache, "tts_voices", data)
	if err != nil {
		return fmt.Errorf("tts.UpsertVoices: %w", err)
	}
	return nil
}

func (r *Repository) GetVoices(ctx context.Context) ([]Voice, error) {
	var data []byte
	err := r.pool.QueryRow(ctx, getCache, "tts_voices").Scan(&data)
	if err != nil {
		return nil, fmt.Errorf("tts.GetVoices: %w", err)
	}

	var voices []Voice
	if err := json.Unmarshal(data, &voices); err != nil {
		return nil, fmt.Errorf("tts.GetVoices: unmarshal: %w", err)
	}
	return voices, nil
}

func (r *Repository) GetOldAudio(ctx context.Context, ttl time.Duration, limit int) ([]string, error) {
	cutoff := time.Now().Add(-ttl)
	rows, err := r.pool.Query(ctx, selectExpiredBank, cutoff, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

func (r *Repository) DeleteFromBank(ctx context.Context, keys []string) error {
	_, err := r.pool.Exec(ctx, deleteFromBank, keys)
	if err != nil {
		return fmt.Errorf("tts.DeleteFromBank: %w", err)
	}
	return nil
}

func (r *Repository) DeleteOldJobs(ctx context.Context, cutoff time.Time) error {
	_, err := r.pool.Exec(ctx, deleteOldJobs, cutoff)
	if err != nil {
		return fmt.Errorf("tts.DeleteOldJobs: %w", err)
	}
	return nil
}

func rollback(ctx context.Context, tx pgx.Tx) {
	err := tx.Rollback(ctx)
	if err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		return
	}
}
