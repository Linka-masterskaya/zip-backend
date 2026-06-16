// Command server runs the HTTP API server.
package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/config"
	"github.com/Linka-masterskaya/zip-backend/internal/metrics"
	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
	"github.com/Linka-masterskaya/zip-backend/internal/redis"
)

var (
	version   string
	buildTime string
)

func main() {
	cfgPath := os.Getenv("CONFIG_PATH")
	if cfgPath == "" {
		cfgPath = "config/config.dev.yml"
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		slog.Error("config load failed", "err", err)
		os.Exit(1)
	}

	slog.SetDefault(newLogger(cfg.App.Env))

	metrics.Initialize()

	redisClient, err := redis.NewClient(cfg.Redis.URL)
	if err != nil {
		slog.Error("redis initialization failed:", "err", err)
		os.Exit(1)
	}

	mainMux := http.NewServeMux()
	wrappedHandler := middleware.Metrics(mainMux)

	srv := &http.Server{
		Addr:         ":" + cfg.App.Port,
		Handler:      wrappedHandler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	metricsMux := http.NewServeMux()
	metricsMux.Handle("GET /metrics", metrics.NewHandler())

	metricsMux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]string{
			"status": "ok",
			"env":    cfg.App.Env,
		}); err != nil {
			slog.Error("health response encode failed", "err", err)
		}
	})

	metricsSrv := &http.Server{
		Addr:         ":9090",
		Handler:      metricsMux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}

	slog.Info("starting server",
		"addr", srv.Addr,
		"env", cfg.App.Env,
		"version", version,
		"buildTime", buildTime,
	)

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("main server error", "err", err)
			os.Exit(1)
		}
	}()

	go func() {
		slog.Info("starting metrics and health server", "addr", metricsSrv.Addr)
		if err := metricsSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("metrics server error", "err", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	go func() {
		if err := metricsSrv.Shutdown(ctx); err != nil {
			slog.Error("metrics server shutdown error", "err", err)
		}
	}()

	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("shutdown error", "err", err)
	}

	// Redis. Закрываем соединение
	if err := redisClient.Close(); err != nil {
		slog.Error("redis close error", "err", err)
	}

}

func newLogger(env string) *slog.Logger {
	if env == "prod" {
		return slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
}
