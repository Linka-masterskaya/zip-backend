// Package logger реализует базовую настройку логгера log/slog
package logger

import (
	"context"
	"log/slog"
	"os"

	"github.com/Linka-masterskaya/zip-backend/internal/reqctx"
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
	if reqId := reqctx.GetRequestID(ctx); reqId != "" {
		r.AddAttrs(slog.String("requestID", reqId))
	} // будет доступно после слияния AB-17

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
	default:
		slog.Error("не задан или передан неверный env",
			slog.String("env", env),
		)
		os.Exit(1)
	}

	slog.SetDefault(slog.New(ContextHandler{handler}))
}
