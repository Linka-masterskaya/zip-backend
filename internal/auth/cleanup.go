package auth

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Linka-masterskaya/zip-backend/internal/logger"
)

type staleRegistrationDeleter interface {
	DeleteStaleUnverifiedUsers(ctx context.Context, cutoff time.Time) (int64, error)
}

// NewRegistrationCleanerFromPool собирает уборщик со своим репозиторием.
// Отдельный конструктор нужен потому, что удаление регистраций не входит в
// интерфейс, которым пользуется authService, — сервис его никогда не вызывает.
func NewRegistrationCleanerFromPool(
	pool *pgxpool.Pool,
	retention, interval time.Duration,
) *RegistrationCleaner {
	return NewRegistrationCleaner(&authRepo{db: pool, pool: pool}, retention, interval)
}

// RegistrationCleaner периодически освобождает адреса, на которых висят
// неподтверждённые регистрации. Пока такая строка жива, настоящий владелец
// адреса не может им воспользоваться.
type RegistrationCleaner struct {
	repo      staleRegistrationDeleter
	retention time.Duration
	interval  time.Duration
}

// NewRegistrationCleaner собирает уборщик. Нулевой retention отключает его.
func NewRegistrationCleaner(
	repo staleRegistrationDeleter,
	retention, interval time.Duration,
) *RegistrationCleaner {
	return &RegistrationCleaner{repo: repo, retention: retention, interval: interval}
}

// Run работает до отмены контекста. Ошибка одного прохода не останавливает
// цикл: уборка не критична для запросов и должна пережить недоступность базы.
func (c *RegistrationCleaner) Run(ctx context.Context) {
	if c.retention <= 0 || c.interval <= 0 {
		slog.InfoContext(ctx, "unverified registration cleanup disabled")
		return
	}

	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	c.runOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.runOnce(ctx)
		}
	}
}

func (c *RegistrationCleaner) runOnce(ctx context.Context) {
	deleted, err := c.repo.DeleteStaleUnverifiedUsers(ctx, time.Now().Add(-c.retention))
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		slog.ErrorContext(ctx, "unverified registration cleanup failed", logger.Err(err))
		return
	}
	if deleted > 0 {
		slog.InfoContext(ctx, "unverified registrations removed", "count", deleted)
	}
}
