// Command server runs the HTTP API server.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	broker "github.com/Linka-masterskaya/zip-backend/internal/broker"
	"github.com/Linka-masterskaya/zip-backend/internal/config"
	"github.com/Linka-masterskaya/zip-backend/internal/metrics"
	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
	"github.com/Linka-masterskaya/zip-backend/internal/redis"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
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

	nc, publisher, err := initNATS(cfg.NATS)
	if err != nil {
		slog.Error("failed to init nats", "err", err)
		os.Exit(1)
	}
	defer func() {
		if err := nc.Drain(); err != nil {
			slog.Error("nats drain", "err", err)
		}
	}()
	_ = publisher // временно, пока нет хендлеров в server

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

func initNATS(cfg config.NATSConfig) (*nats.Conn, *broker.Publisher, error) {
	nc, err := broker.New(cfg.Connection)
	if err != nil {
		return nil, nil, fmt.Errorf("initNATS: %w", err)
	}
	slog.Info("nats connected", "url", cfg.Connection.URL)

	js, err := jetstream.New(nc)
	if err != nil {
		return nil, nil, fmt.Errorf("initNATS: jetstream: %w", err)
	}

	if err := broker.InitStreams(cfg.Stream, js); err != nil {
		return nil, nil, fmt.Errorf("initNATS: streams: %w", err)
	}
	slog.Info("jetstream stream ready", "stream", cfg.Stream.Name)

	publisher := broker.NewPublisher(js)

	return nc, publisher, nil
}
