package pack

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
)

// memoryShareJobRepository is used by unit-level constructors only. Production
// wiring passes Repository, which persists the same contract in PostgreSQL.
type memoryShareJobRepository struct {
	mu   sync.Mutex
	jobs map[uuid.UUID]shareJobRecord
}

func newMemoryShareJobRepository() *memoryShareJobRepository {
	return &memoryShareJobRepository{jobs: make(map[uuid.UUID]shareJobRecord)}
}

func (r *memoryShareJobRepository) EnqueueShareJob(_ context.Context, job shareJobRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.jobs[job.ID] = job
	return nil
}

func (r *memoryShareJobRepository) ClaimShareJob(
	_ context.Context,
	lease time.Duration,
	maxAttempts int,
) (*shareJobRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now().UTC()
	r.failExhaustedJobs(now, maxAttempts)
	selected := r.selectDueJob(now, maxAttempts)
	if selected == nil {
		return nil, nil
	}

	leaseUntil := now.Add(lease)
	selected.Status = ShareTaskProcessing
	if selected.EmailSentAt == nil {
		selected.Message = ""
		selected.Attempts++
	}
	selected.LeaseToken = uuid.New()
	selected.LeaseUntil = &leaseUntil
	selected.UpdatedAt = now
	r.jobs[selected.ID] = *selected
	copyJob := *selected
	return &copyJob, nil
}

func (r *memoryShareJobRepository) failExhaustedJobs(now time.Time, maxAttempts int) {
	for id, candidate := range r.jobs {
		if !shareJobAttemptsExhausted(candidate, now, maxAttempts) {
			continue
		}
		candidate.Status = ShareTaskFailed
		candidate.Message = "delivery failed after maximum attempts"
		if candidate.LastError == "" {
			candidate.LastError = "maximum delivery attempts exhausted"
		}
		candidate.LeaseToken = uuid.Nil
		candidate.LeaseUntil = nil
		candidate.UpdatedAt = now
		r.jobs[id] = candidate
	}
}

func (r *memoryShareJobRepository) selectDueJob(now time.Time, maxAttempts int) *shareJobRecord {
	var selected *shareJobRecord
	for id := range r.jobs {
		candidate := r.jobs[id]
		if !shareJobDueForClaim(candidate, now, maxAttempts) {
			continue
		}
		if selected == nil || shareJobComesBefore(candidate, *selected) {
			copyCandidate := candidate
			selected = &copyCandidate
		}
	}
	return selected
}

func shareJobAttemptsExhausted(job shareJobRecord, now time.Time, maxAttempts int) bool {
	if job.EmailSentAt != nil || job.Attempts < maxAttempts {
		return false
	}
	return shareJobDueByStatus(job, now)
}

func shareJobDueForClaim(job shareJobRecord, now time.Time, maxAttempts int) bool {
	if job.EmailSentAt == nil && job.Attempts >= maxAttempts {
		return false
	}
	return shareJobDueByStatus(job, now)
}

func shareJobDueByStatus(job shareJobRecord, now time.Time) bool {
	if job.Status == ShareTaskQueued {
		return !job.NextAttemptAt.After(now)
	}
	return job.Status == ShareTaskProcessing && (job.LeaseUntil == nil || !job.LeaseUntil.After(now))
}

func shareJobComesBefore(candidate, selected shareJobRecord) bool {
	if candidate.NextAttemptAt.Before(selected.NextAttemptAt) {
		return true
	}
	return candidate.NextAttemptAt.Equal(selected.NextAttemptAt) && candidate.CreatedAt.Before(selected.CreatedAt)
}

func (r *memoryShareJobRepository) GetShareJob(_ context.Context, id uuid.UUID) (*shareJobRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	job, ok := r.jobs[id]
	if !ok {
		return nil, errShareJobNotFound
	}
	copyJob := job
	return &copyJob, nil
}

func (r *memoryShareJobRepository) MarkShareJobEmailSent(_ context.Context, id, leaseToken uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	job, ok := r.jobs[id]
	if !ok || job.Status != ShareTaskProcessing || job.LeaseToken != leaseToken {
		return errShareJobLeaseLost
	}
	now := time.Now().UTC()
	if job.EmailSentAt == nil {
		job.EmailSentAt = &now
	}
	job.Message = "email accepted by SMTP; finalizing"
	job.LastError = ""
	job.UpdatedAt = now
	r.jobs[id] = job
	return nil
}

func (r *memoryShareJobRepository) CompleteShareJob(_ context.Context, id, leaseToken uuid.UUID) error {
	return r.finish(id, leaseToken, ShareTaskSent, "", "")
}

func (r *memoryShareJobRepository) FailShareJob(
	_ context.Context,
	id, leaseToken uuid.UUID,
	message, lastError string,
) error {
	return r.finish(id, leaseToken, ShareTaskFailed, message, lastError)
}

func (r *memoryShareJobRepository) finish(
	id, leaseToken uuid.UUID,
	status ShareTaskStatus,
	message, lastError string,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	job, ok := r.jobs[id]
	if !ok || job.Status != ShareTaskProcessing || job.LeaseToken != leaseToken {
		return errShareJobLeaseLost
	}
	now := time.Now().UTC()
	job.Status = status
	job.Message = message
	job.LastError = lastError
	job.LeaseToken = uuid.Nil
	job.LeaseUntil = nil
	job.UpdatedAt = now
	r.jobs[id] = job
	return nil
}

func (r *memoryShareJobRepository) RequeueShareJob(
	_ context.Context,
	id, leaseToken uuid.UUID,
	message, lastError string,
	delay time.Duration,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	job, ok := r.jobs[id]
	if !ok || job.Status != ShareTaskProcessing || job.LeaseToken != leaseToken {
		return errShareJobLeaseLost
	}
	if delay < 0 {
		delay = 0
	}
	now := time.Now().UTC()
	job.Status = ShareTaskQueued
	job.Message = message
	job.LastError = lastError
	job.LeaseToken = uuid.Nil
	job.LeaseUntil = nil
	job.NextAttemptAt = now.Add(delay)
	job.UpdatedAt = now
	r.jobs[id] = job
	return nil
}

func (r *memoryShareJobRepository) PruneShareJobs(_ context.Context, before time.Time) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var deleted int64
	for id, job := range r.jobs {
		if isTerminalShareStatus(job.Status) && job.UpdatedAt.Before(before) {
			delete(r.jobs, id)
			deleted++
		}
	}
	return deleted, nil
}
