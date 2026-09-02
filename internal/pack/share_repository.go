package pack

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var (
	errShareJobNotFound  = errors.New("pack share job not found")
	errShareJobLeaseLost = errors.New("pack share job lease lost")
)

type shareJobRecord struct {
	ID            uuid.UUID
	OwnerID       uuid.UUID
	PackID        uuid.UUID
	StudentID     uuid.UUID
	RequestID     string
	Status        ShareTaskStatus
	Message       string
	LastError     string
	Attempts      int
	LeaseToken    uuid.UUID
	LeaseUntil    *time.Time
	EmailSentAt   *time.Time
	NextAttemptAt time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type shareJobRepository interface {
	EnqueueShareJob(context.Context, shareJobRecord) error
	ClaimShareJob(context.Context, time.Duration, int) (*shareJobRecord, error)
	GetShareJob(context.Context, uuid.UUID) (*shareJobRecord, error)
	MarkShareJobEmailSent(context.Context, uuid.UUID, uuid.UUID) error
	CompleteShareJob(context.Context, uuid.UUID, uuid.UUID) error
	FailShareJob(context.Context, uuid.UUID, uuid.UUID, string, string) error
	RequeueShareJob(context.Context, uuid.UUID, uuid.UUID, string, string, time.Duration) error
	PruneShareJobs(context.Context, time.Time) (int64, error)
}

// EnqueueShareJob persists a queued pack-share delivery before HTTP 202 is returned.
func (r *Repository) EnqueueShareJob(ctx context.Context, job shareJobRecord) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO pack_share_jobs (
			id, owner_id, pack_id, student_id, request_id, status, message, last_error,
			next_attempt_at, created_at, updated_at
		) VALUES ($1, $2, $3, $4, NULLIF($5, ''), 'queued', '', '', $6, $6, $6)
	`, job.ID, job.OwnerID, job.PackID, job.StudentID, job.RequestID, job.CreatedAt)
	if err != nil {
		return fmt.Errorf("enqueue pack share job: %w", err)
	}
	return nil
}

// ClaimShareJob leases one due delivery using SKIP LOCKED for multi-instance workers.
// Jobs that have already exhausted maxAttempts are terminally failed before another
// expensive export/SMTP attempt can start. A job with email_sent_at set is still
// claimable so a new worker can finalize it without sending the message again.
func (r *Repository) ClaimShareJob(ctx context.Context, lease time.Duration, maxAttempts int) (*shareJobRecord, error) {
	if lease <= 0 {
		return nil, fmt.Errorf("pack share lease must be positive")
	}
	if maxAttempts <= 0 {
		return nil, fmt.Errorf("pack share max attempts must be positive")
	}
	leaseSeconds := int64((lease + time.Second - 1) / time.Second)
	leaseToken := uuid.New()

	job, err := scanShareJob(r.pool.QueryRow(ctx, `
		WITH exhausted AS (
			UPDATE pack_share_jobs
			SET status = 'failed',
				message = 'delivery failed after maximum attempts',
				last_error = CASE
					WHEN last_error = '' THEN 'maximum delivery attempts exhausted'
					ELSE last_error
				END,
				lease_token = NULL,
				lease_until = NULL,
				completed_at = now(),
				updated_at = now()
			WHERE email_sent_at IS NULL
			  AND attempts >= $3
			  AND (
				(status = 'queued' AND next_attempt_at <= now())
				OR (status = 'processing' AND (lease_until IS NULL OR lease_until <= now()))
			  )
			RETURNING id
		), candidate AS (
			SELECT id
			FROM pack_share_jobs
			WHERE (
				status = 'queued'
				AND next_attempt_at <= now()
				AND (email_sent_at IS NOT NULL OR attempts < $3)
			) OR (
				status = 'processing'
				AND (lease_until IS NULL OR lease_until <= now())
				AND (email_sent_at IS NOT NULL OR attempts < $3)
			)
			ORDER BY next_attempt_at, created_at, id
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE pack_share_jobs j
		SET status = 'processing',
			message = CASE WHEN j.email_sent_at IS NULL THEN '' ELSE j.message END,
			attempts = CASE WHEN j.email_sent_at IS NULL THEN j.attempts + 1 ELSE j.attempts END,
			lease_token = $1,
			lease_until = now() + make_interval(secs => $2::double precision),
			updated_at = now(),
			completed_at = NULL
		FROM candidate c
		WHERE j.id = c.id
		RETURNING j.id, j.owner_id, j.pack_id, j.student_id,
		          COALESCE(j.request_id, ''), j.status, j.message, j.last_error, j.attempts,
		          j.lease_token, j.lease_until, j.email_sent_at, j.next_attempt_at,
		          j.created_at, j.updated_at
	`, leaseToken, leaseSeconds, maxAttempts))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim pack share job: %w", err)
	}
	return job, nil
}

// GetShareJob returns durable delivery state by task ID.
func (r *Repository) GetShareJob(ctx context.Context, id uuid.UUID) (*shareJobRecord, error) {
	job, err := scanShareJob(r.pool.QueryRow(ctx, `
		SELECT id, owner_id, pack_id, student_id, COALESCE(request_id, ''),
		       status, message, last_error, attempts, lease_token, lease_until,
		       email_sent_at, next_attempt_at, created_at, updated_at
		FROM pack_share_jobs
		WHERE id = $1
	`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errShareJobNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get pack share job: %w", err)
	}
	return job, nil
}

// MarkShareJobEmailSent persists the SMTP side effect before the lease is finalized.
// If CompleteShareJob subsequently fails, a reclaimed job sees email_sent_at and
// completes without dispatching the attachment a second time.
func (r *Repository) MarkShareJobEmailSent(ctx context.Context, id, leaseToken uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE pack_share_jobs
		SET email_sent_at = COALESCE(email_sent_at, now()),
			message = 'email accepted by SMTP; finalizing',
			last_error = '',
			updated_at = now()
		WHERE id = $1
		  AND status = 'processing'
		  AND lease_token = $2
	`, id, leaseToken)
	if err != nil {
		return fmt.Errorf("mark pack share email sent: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return errShareJobLeaseLost
	}
	return nil
}

// CompleteShareJob marks the currently leased delivery as sent.
func (r *Repository) CompleteShareJob(ctx context.Context, id, leaseToken uuid.UUID) error {
	return r.finishShareJob(ctx, id, leaseToken, ShareTaskSent, "", "")
}

// FailShareJob marks the currently leased delivery as terminally failed.
func (r *Repository) FailShareJob(
	ctx context.Context,
	id, leaseToken uuid.UUID,
	message, lastError string,
) error {
	return r.finishShareJob(ctx, id, leaseToken, ShareTaskFailed, message, lastError)
}

func (r *Repository) finishShareJob(
	ctx context.Context,
	id, leaseToken uuid.UUID,
	status ShareTaskStatus,
	message, lastError string,
) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE pack_share_jobs
		SET status = $3,
			message = $4,
			last_error = $5,
			lease_token = NULL,
			lease_until = NULL,
			completed_at = now(),
			updated_at = now()
		WHERE id = $1
		  AND status = 'processing'
		  AND lease_token = $2
	`, id, leaseToken, status, message, lastError)
	if err != nil {
		return fmt.Errorf("finish pack share job: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return errShareJobLeaseLost
	}
	return nil
}

// RequeueShareJob releases the current lease and schedules the delivery again.
func (r *Repository) RequeueShareJob(
	ctx context.Context,
	id, leaseToken uuid.UUID,
	message, lastError string,
	delay time.Duration,
) error {
	if delay < 0 {
		delay = 0
	}
	delaySeconds := int64((delay + time.Second - 1) / time.Second)
	tag, err := r.pool.Exec(ctx, `
		UPDATE pack_share_jobs
		SET status = 'queued',
			message = $3,
			last_error = $4,
			next_attempt_at = now() + make_interval(secs => $5::double precision),
			lease_token = NULL,
			lease_until = NULL,
			completed_at = NULL,
			updated_at = now()
		WHERE id = $1
		  AND status = 'processing'
		  AND lease_token = $2
	`, id, leaseToken, message, lastError, delaySeconds)
	if err != nil {
		return fmt.Errorf("requeue pack share job: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return errShareJobLeaseLost
	}
	return nil
}

// PruneShareJobs deletes terminal delivery records older than the retention cutoff.
func (r *Repository) PruneShareJobs(ctx context.Context, before time.Time) (int64, error) {
	tag, err := r.pool.Exec(ctx, `
		DELETE FROM pack_share_jobs
		WHERE completed_at IS NOT NULL
		  AND completed_at < $1
	`, before)
	if err != nil {
		return 0, fmt.Errorf("prune pack share jobs: %w", err)
	}
	return tag.RowsAffected(), nil
}

type shareJobScanner interface {
	Scan(...any) error
}

func scanShareJob(row shareJobScanner) (*shareJobRecord, error) {
	var job shareJobRecord
	var status string
	var leaseToken pgtype.UUID
	var leaseUntil pgtype.Timestamptz
	var emailSentAt pgtype.Timestamptz
	if err := row.Scan(
		&job.ID,
		&job.OwnerID,
		&job.PackID,
		&job.StudentID,
		&job.RequestID,
		&status,
		&job.Message,
		&job.LastError,
		&job.Attempts,
		&leaseToken,
		&leaseUntil,
		&emailSentAt,
		&job.NextAttemptAt,
		&job.CreatedAt,
		&job.UpdatedAt,
	); err != nil {
		return nil, err
	}
	job.Status = ShareTaskStatus(status)
	if leaseToken.Valid {
		job.LeaseToken = uuid.UUID(leaseToken.Bytes)
	}
	if leaseUntil.Valid {
		value := leaseUntil.Time
		job.LeaseUntil = &value
	}
	if emailSentAt.Valid {
		value := emailSentAt.Time
		job.EmailSentAt = &value
	}
	return &job, nil
}
