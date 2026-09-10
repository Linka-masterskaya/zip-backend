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

type storageReaper interface {
	ReapUnreferenced(context.Context, time.Duration, int) (int, error)
}

type TTSCleaner struct {
	repo        cleanupRepo
	reaper      storageReaper
	cleanPeriod time.Duration
	jobsTTL     time.Duration
	reaperGrace time.Duration
	limit       int
	reaperLimit int
}

func NewTTSCleaner(
	repo cleanupRepo,
	reaper storageReaper,
	cleanPeriod, jobsTTL, reaperGrace time.Duration,
	limit, reaperLimit int,
) *TTSCleaner {
	return &TTSCleaner{
		repo:        repo,
		reaper:      reaper,
		cleanPeriod: cleanPeriod,
		jobsTTL:     jobsTTL,
		reaperGrace: reaperGrace,
		limit:       limit,
		reaperLimit: reaperLimit,
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

	// audio_bank is cache metadata, not ownership. GetOldAudio only returns
	// expired rows without media_files references; the global storage reaper then
	// rechecks every domain reference immediately before touching MinIO.
	if len(keys) > 0 {
		if err = c.repo.DeleteFromBank(ctx, keys); err != nil {
			slog.ErrorContext(ctx, "bank cleanup: DeleteFromBank failed", "err", err)
		}
	}

	if c.reaper != nil {
		removed, reapErr := c.reaper.ReapUnreferenced(ctx, c.reaperGrace, c.reaperLimit)
		if reapErr != nil {
			slog.ErrorContext(ctx, "storage reaper: cleanup failed", "removed", removed, "err", reapErr)
		}
	}

	return nil
}
