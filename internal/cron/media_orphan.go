package cron

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/media"
)

type mediaOrphanRepo interface {
	ClearSoftDeletedStudentAvatars(context.Context) (int64, error)
	DeleteOrphanBatch(context.Context, int, time.Time) (media.OrphanBatchResult, error)
}

// MediaOrphanScanner periodically removes unreferenced media_files in bounded
// batches. MinIO objects are intentionally left to the global storage reaper;
// this scanner owns DB references and organization quota only.
type MediaOrphanScanner struct {
	repo        mediaOrphanRepo
	batchSize   int
	gracePeriod time.Duration
}

func NewMediaOrphanScanner(repo mediaOrphanRepo, batchSize int, gracePeriod time.Duration) *MediaOrphanScanner {
	return &MediaOrphanScanner{repo: repo, batchSize: batchSize, gracePeriod: gracePeriod}
}

func (s *MediaOrphanScanner) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			result, err := s.Scan(ctx)
			if err != nil {
				slog.ErrorContext(ctx, "cron.MediaOrphanScanner: scan failed",
					"count", result.Count,
					"bytes", result.Bytes,
					"err", err,
				)
			}
		case <-ctx.Done():
			return
		}
	}
}

func (s *MediaOrphanScanner) Scan(ctx context.Context) (media.OrphanBatchResult, error) {
	cleared, err := s.repo.ClearSoftDeletedStudentAvatars(ctx)
	if err != nil {
		return media.OrphanBatchResult{}, err
	}

	createdBefore := time.Now().Add(-s.gracePeriod)
	var total media.OrphanBatchResult
	for {
		batch, batchErr := s.repo.DeleteOrphanBatch(ctx, s.batchSize, createdBefore)
		total.Count += batch.Count
		total.Bytes += batch.Bytes
		if batchErr != nil {
			return total, fmt.Errorf("media orphan scan after %d files/%d bytes: %w", total.Count, total.Bytes, batchErr)
		}
		if batch.Candidates < int64(s.batchSize) {
			break
		}
		if err := ctx.Err(); err != nil {
			return total, err
		}
	}

	slog.InfoContext(ctx, "media orphan scan complete",
		"count", total.Count,
		"bytes", total.Bytes,
		"soft_deleted_avatar_refs_cleared", cleared,
	)
	return total, nil
}
