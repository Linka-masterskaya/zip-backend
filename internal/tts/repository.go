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

func (r *Repository) Create(ctx context.Context) error {
	return nil
}

const getMediaIDFromBankQuery = `
UPDATE audio_bank
SET last_used_at = now()
WHERE text = $1 AND voice = $2
RETURNING media_id`

func (r *Repository) GetMediaIDFromBank(ctx context.Context, text, voice string) (uuid.UUID, error) {

	var audioId uuid.UUID
	err := r.pool.QueryRow(ctx, getMediaIDFromBankQuery, text, voice).Scan(&audioId)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, apperr.ErrNotFound
		}
		return uuid.Nil, fmt.Errorf("tts.GetMediaIDFromBank: %w", err)
	}

	return audioId, nil
}

const createJobTTS = `
INSERT INTO ai_jobs(job_id, text, voice, status, created_at, updated_at)
VALUES($1, "pending", NOW()
`

func (r *Repository) CreateJobTTS(ctx context.Context, jobID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, createJobTTS, jobID)
	if err != nil {
		return fmt.Errorf("tts.CreateJobTTS: %w", err)
	}

	return nil
}

// константы нужны
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
		_ = tx.Rollback(ctx)
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
