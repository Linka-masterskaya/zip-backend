// Command server runs the HTTP API server.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/lib/pq"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/Linka-masterskaya/zip-backend/internal/broker"
	"github.com/Linka-masterskaya/zip-backend/internal/cache"
	"github.com/Linka-masterskaya/zip-backend/internal/config"
	"github.com/Linka-masterskaya/zip-backend/internal/db"
	"github.com/Linka-masterskaya/zip-backend/internal/logger"
	"github.com/Linka-masterskaya/zip-backend/internal/metrics"
	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
	"github.com/Linka-masterskaya/zip-backend/internal/pack"
	"github.com/Linka-masterskaya/zip-backend/internal/storage"
	"github.com/Linka-masterskaya/zip-backend/migrations"
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
		slog.Error("config load failed", logger.Err(err)) //nolint:gosec // ошибка на старте приложения.
		os.Exit(1)
	}

	// Обработка флага --migrate
	runMigrationsIfNeeded(cfg)

	logger.Init(cfg.App.Env)

	metrics.Initialize()

	// Пока инициализируем MinIO только для проверки подключения и создания bucket при старте
	// Клиент будет сохранен и передан в сервисы позже, когда появятся операции с объектами
	if _, err := storage.New(cfg.MinIO); err != nil {
		slog.Error("minio connect failed", logger.Err(err)) //nolint:gosec // ошибка на старте приложения.
		os.Exit(1)
	}
	slog.Info("minio connected", "bucket", cfg.MinIO.Bucket) //nolint:gosec // ошибка на старте приложения.

	nc, publisher, err := initNATS(cfg.NATS)
	if err != nil {
		slog.Error("failed to init nats", logger.Err(err)) //nolint:gosec // ошибка на старте приложения.
		os.Exit(1)
	}
	defer func() {
		if err := nc.Drain(); err != nil {
			slog.Error("nats drain", logger.Err(err)) //nolint:gosec // ошибка на старте приложения.
		}
	}()

	redisClient, err := cache.NewClient(cfg.Redis)
	if err != nil {
		slog.Error("redis initialization failed:", logger.Err(err)) //nolint:gosec // ошибка на старте приложения.
		os.Exit(1)
	}

	// Postgres. Инициализация
	dbPool, err := db.New(cfg.DB)
	if err != nil {
		slog.Error("postgres initialization failed:", logger.Err(err)) //nolint:gosec // ошибка на старте приложения.
		os.Exit(1)
	}
	defer dbPool.Close()

	slog.Info("database connected", "pool_size", cfg.DB.MaxConns) //nolint:gosec // ошибка на старте приложения.

	packRepo := pack.NewRepository(redisClient)
	packService := pack.NewService(packRepo, publisher)
	packHandler := pack.NewHandler(packService)

	mainMux := http.NewServeMux()
	mainMux.Handle("POST /api/v1/packs", middleware.ErrorMiddleware(packHandler.CreatePack))
	mainMux.Handle("GET /api/v1/packs/{id}", middleware.ErrorMiddleware(packHandler.GetPack))
	mainMux.Handle("GET /api/v1/packs", middleware.ErrorMiddleware(packHandler.ListPacks))

	wrappedHandler := middleware.Chain(
		mainMux,
		middleware.RecoveryMiddleware,
		middleware.RequestIDMiddleware,
		middleware.Metrics,
		middleware.CORSMiddleware(cfg.App.FrontendURL),
	)

	srv := &http.Server{
		Addr:         ":" + cfg.App.Port,
		Handler:      wrappedHandler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	metricsMux := http.NewServeMux()
	metricsMux.Handle("GET /metrics", metrics.NewHandler())
	metricsMux.HandleFunc("GET /health", healthHandler(cfg.App.Env))
	metricsMux.HandleFunc("GET /readyz", readyzHandler(redisClient))

	metricsSrv := &http.Server{
		Addr:         ":9090",
		Handler:      metricsMux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}

	slog.Info("starting server", //nolint:gosec // ошибка на старте приложения.
		"addr", srv.Addr,
		"env", cfg.App.Env,
		"version", version,
		"buildTime", buildTime,
	)

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("main server error", logger.Err(err))
			os.Exit(1)
		}
	}()

	go func() {
		slog.Info("starting metrics and health server", "addr", metricsSrv.Addr)
		if err := metricsSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("metrics server error", logger.Err(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := metricsSrv.Shutdown(ctx); err != nil {
		slog.Error("metrics server shutdown error", "err", err)
	}

	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("shutdown error", logger.Err(err))
	}

	// Redis. Закрываем соединение
	if err := redisClient.Close(); err != nil {
		slog.Error("redis close error", logger.Err(err)) //nolint:gosec // ошибка на останове приложения.
	}
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

func healthHandler(env string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]string{"status": "ok", "env": env}); err != nil {
			slog.Error("health response encode failed", logger.Err(err))
		}
	}
}

func readyzHandler(redisClient *cache.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		w.Header().Set("Content-Type", "application/json")

		if err := redisClient.Ping(ctx); err != nil {
			slog.Error("readyz: redis unavailable", logger.Err(err)) //nolint:gosec // ошибка на останове приложения.
			w.WriteHeader(http.StatusServiceUnavailable)
			if err := json.NewEncoder(w).Encode(map[string]string{"status": "redis unavailable"}); err != nil {
				slog.Error("readyz response encode failed", logger.Err(err))
			}
			return
		}

		if err := json.NewEncoder(w).Encode(map[string]string{"status": "ready"}); err != nil {
			slog.Error("readyz response encode failed", logger.Err(err))
		}
	}
}

// runMigrationsIfNeeded проверяет флаг --migrate и выполняет миграции, если он установлен.
func runMigrationsIfNeeded(cfg *config.Config) {
	migrateFlag := flag.Bool("migrate", false, "Run database migrations and exit")
	flag.Parse()

	if !*migrateFlag {
		return
	}

	// Подключаемся к БД только для миграций
	dbConn, err := sql.Open("postgres", cfg.DB.URL)
	if err != nil {
		slog.Error("failed to connect to postgres for migration", logger.Err(err)) //nolint:gosec // ошибка на старте приложения.
		os.Exit(1)
	}
	defer func() {
		if err := dbConn.Close(); err != nil {
			slog.Error("failed to close db connection after migration", logger.Err(err)) //nolint:gosec // ошибка на старте приложения.
		}
	}()

	if err := migrations.Run(dbConn); err != nil {
		log.Fatalf("Migration failed: %v", err)
	}
	log.Println("Migrations completed. Exiting.")
	os.Exit(0)
}
