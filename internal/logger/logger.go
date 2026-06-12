// Package logger реализует базовую настройку логгера log/slog
package logger

import (
	"log/slog"
	"os"
)

const (
	envDev  = "dev"
	envProd = "prod"
)

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

	slog.SetDefault(slog.New(handler))
}
