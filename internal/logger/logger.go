// Package logger реализует базовую настройку логгера log/slog
package logger

import (
	"context"
	"log/slog"
	"os"

	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
)

const (
	envDev  = "dev"
	envProd = "prod"
)

type ContextHandler struct {
	slog.Handler // встраиваем — Enabled/WithAttrs/WithGroup наследуются как есть
}

// Handle вызывается на каждую запись лога. Именно сюда приходит ctx,
// переданный через logger.InfoContext(ctx, ...).
func (h ContextHandler) Handle(ctx context.Context, r slog.Record) error {
	reqId := middleware.GetRequestID(ctx) // будет доступно после слияния AB-17

	if reqId != nil {
		r.AddAttrs(slog.String("requestID", reqId.(string)))
	}

	return h.Handler.Handle(ctx, r)
}

// Init инициализирует и устанавливает дефолтным логгер с настройками,
// зависищями от оркужения.
func Init(env string) {
	var handler slog.Handler

	switch env {
	case envDev:
		handler = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelDebug,
		})
	case envProd:
		handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelInfo,
		})
	}

	slog.SetDefault(slog.New(ContextHandler{handler}))
}
