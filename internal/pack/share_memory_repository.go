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

func (r *memoryShareJobRepository) ClaimShareJob(_ context.Context, lease time.Duration) (*shareJobRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now().UTC()
	var selected *shareJobRecord
	for id := range r.jobs {
		candidate := r.jobs[id]
		eligible := candidate.Status == ShareTaskQueued && !candidate.NextAttemptAt.After(now)
		if candidate.Status == ShareTaskProcessing && (candidate.LeaseUntil == nil || !candidate.LeaseUntil.After(now)) {
			eligible = true
		}
		if !eligible {
			continue
		}
		if selected == nil || candidate.NextAttemptAt.Before(selected.NextAttemptAt) ||
			(candidate.NextAttemptAt.Equal(selected.NextAttemptAt) && candidate.CreatedAt.Before(selected.CreatedAt)) {
			copyCandidate := candidate
			selected = &copyCandidate
		}
	}
	if selected == nil {
		return nil, nil
	}

	leaseUntil := now.Add(lease)
	selected.Status = ShareTaskProcessing
	selected.Message = ""
	selected.Attempts++
	selected.LeaseToken = uuid.New()
	selected.LeaseUntil = &leaseUntil
	selected.UpdatedAt = now
	r.jobs[selected.ID] = *selected
	copyJob := *selected
	return &copyJob, nil
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

func (r *memoryShareJobRepository) CompleteShareJob(_ context.Context, id, leaseToken uuid.UUID) error {
	return r.finish(id, leaseToken, ShareTaskSent, "")
}

func (r *memoryShareJobRepository) FailShareJob(_ context.Context, id, leaseToken uuid.UUID, message string) error {
	return r.finish(id, leaseToken, ShareTaskFailed, message)
}

func (r *memoryShareJobRepository) finish(id, leaseToken uuid.UUID, status ShareTaskStatus, message string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	job, ok := r.jobs[id]
	if !ok || job.Status != ShareTaskProcessing || job.LeaseToken != leaseToken {
		return errShareJobLeaseLost
	}
	now := time.Now().UTC()
	job.Status = status
	job.Message = message
	job.LeaseToken = uuid.Nil
	job.LeaseUntil = nil
	job.UpdatedAt = now
	r.jobs[id] = job
	return nil
}

func (r *memoryShareJobRepository) RequeueShareJob(
	_ context.Context,
	id, leaseToken uuid.UUID,
	message string,
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
