package cron

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

type cleanupRepo interface {
	GetOldAudio(context.Context, time.Duration, int) ([]string, error)
	DeleteFromBank(context.Context, []string) error
	CleanupOldJobs(context.Context, time.Time) error
}

type storage interface {
	RemoveObject(context.Context, string) error
}

type TTSCleaner struct {
	repo        cleanupRepo
	stor        storage
	cleanPeriod time.Duration
	jobsTTL     time.Duration
	limit       int
}

func NewTTSCleaner(repo cleanupRepo, stor storage, cleanPeriod, jobsTTL time.Duration, limit int) *TTSCleaner {
	return &TTSCleaner{
		repo:        repo,
		stor:        stor,
		cleanPeriod: cleanPeriod,
		jobsTTL:     jobsTTL,
		limit:       limit,
	}
}

func (c *TTSCleaner) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := c.Cleanup(ctx); err != nil {
				slog.ErrorContext(ctx, "cron.TTSCleaner: error", "err", err)
			}
		case <-ctx.Done():
			return
		}
	}
}

func (c *TTSCleaner) Cleanup(ctx context.Context) error {
	jobsCutoff := time.Now().Add(-c.jobsTTL)
	if err := c.repo.CleanupOldJobs(ctx, jobsCutoff); err != nil {
		slog.ErrorContext(ctx, "cron.Cleanup: cleanup old jobs failed", "err", err)
	}

	keys, err := c.repo.GetOldAudio(ctx, c.cleanPeriod, c.limit)
	if err != nil {
		return fmt.Errorf("cron.Cleaner: %w", err)
	}

	var deleted []string
	for _, key := range keys {
		if err := c.stor.RemoveObject(ctx, key); err != nil {
			slog.ErrorContext(ctx, "bank cleanup: minio delete failed", "key", key, "err", err)
			continue
		}
		deleted = append(deleted, key)
	}

	if len(deleted) > 0 {
		err = c.repo.DeleteFromBank(ctx, deleted)
		if err != nil {
			slog.ErrorContext(ctx, "bank cleanup: DeleteFromBank failed", "err", err)
		}
	}

	return nil
}
