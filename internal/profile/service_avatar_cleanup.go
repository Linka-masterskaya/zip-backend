package profile

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/logger"
	"github.com/Linka-masterskaya/zip-backend/internal/storage"
)

const (
	avatarCleanupPollInterval = 15 * time.Second
	avatarCleanupBatchSize    = 32
)

// RunAvatarCleanupWorker continuously drains durable avatar cleanup jobs. Jobs
// are leased in PostgreSQL, so multiple application instances can run this
// worker safely. Transient MinIO/DB failures are recorded and retried.
func (s *Service) RunAvatarCleanupWorker(ctx context.Context) error {
	if s.storage == nil {
		return nil
	}

	// Drain immediately at startup so jobs left by a previous process crash do
	// not wait for the first ticker interval.
	s.drainAvatarCleanup(ctx)

	ticker := time.NewTicker(avatarCleanupPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			s.drainAvatarCleanup(ctx)
		}
	}
}

func (s *Service) drainAvatarCleanup(ctx context.Context) {
	for range avatarCleanupBatchSize {
		processed, err := s.processAvatarCleanup(ctx, "")
		if err != nil {
			slog.Error("process avatar cleanup job", logger.Err(err))
			if processed {
				// The failed job has been rescheduled; continue draining other
				// due jobs so one broken object cannot starve the queue.
				continue
			}
			return
		}
		if !processed {
			return
		}
	}
}

// cleanupDeletedAvatar attempts the just-created durable job immediately. A
// failure does not roll back account deletion: the worker will retry the job.
func (s *Service) cleanupDeletedAvatar(ctx context.Context, userID string, change AvatarChange) {
	if change.OldKey == "" || s.storage == nil {
		return
	}
	processed, err := s.processAvatarCleanup(ctx, change.OldKey)
	if err != nil {
		slog.Error("process soft-deleted user avatar cleanup",
			"user_id", userID,
			"key", change.OldKey,
			logger.Err(err),
		)
		return
	}
	if !processed {
		slog.Debug("soft-delete avatar cleanup job is already leased",
			"user_id", userID,
			"key", change.OldKey,
		)
	}
}

func (s *Service) processAvatarCleanup(ctx context.Context, objectKey string) (bool, error) {
	job, err := s.repo.ClaimAvatarCleanupJob(ctx, objectKey)
	if err != nil {
		return false, err
	}
	if job == nil {
		return false, nil
	}

	if err := s.executeAvatarCleanupJob(ctx, job); err != nil {
		if retryErr := s.repo.RetryAvatarCleanup(ctx, job.ID, err); retryErr != nil {
			return true, errors.Join(err, retryErr)
		}
		return true, err
	}
	return true, nil
}

func (s *Service) executeAvatarCleanupJob(ctx context.Context, job *AvatarCleanupJob) error {
	size := int64(0)
	if job.ObjectSize.Valid {
		size = job.ObjectSize.Int64
	} else {
		var err error
		size, err = s.storage.ObjectSize(ctx, job.ObjectKey)
		if errors.Is(err, storage.ErrObjectNotFound) {
			// The object was removed outside this worker before its size could be
			// recorded. There are no bytes left to remove; complete the durable
			// job without guessing an organization quota delta.
			if !job.QuotaAdjusted {
				if adjustErr := s.repo.AdjustAvatarCleanupQuota(ctx, job.ID, 0); adjustErr != nil {
					return adjustErr
				}
			}
			return s.repo.CompleteAvatarCleanup(ctx, job.ID)
		}
		if err != nil {
			return fmt.Errorf("stat avatar cleanup object %q: %w", job.ObjectKey, err)
		}
		if err := s.repo.SetAvatarCleanupSize(ctx, job.ID, size); err != nil {
			return err
		}
	}

	// Remove the object before releasing its quota. The size has already been
	// persisted, so a crash after a successful delete can still reconcile quota
	// on the next retry without having to stat the now-missing object.
	if err := s.storage.RemoveObject(ctx, job.ObjectKey); err != nil && !errors.Is(err, storage.ErrObjectNotFound) {
		return fmt.Errorf("remove avatar cleanup object %q: %w", job.ObjectKey, err)
	}

	if !job.QuotaAdjusted {
		if err := s.repo.AdjustAvatarCleanupQuota(ctx, job.ID, size); err != nil {
			return err
		}
	}

	if err := s.repo.CompleteAvatarCleanup(ctx, job.ID); err != nil {
		return err
	}
	return nil
}
