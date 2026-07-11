// Command server runs the HTTP API server.

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/lib/pq"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/Linka-masterskaya/zip-backend/internal/auth"
	"github.com/Linka-masterskaya/zip-backend/internal/broker"
	"github.com/Linka-masterskaya/zip-backend/internal/cache"
	"github.com/Linka-masterskaya/zip-backend/internal/config"
	"github.com/Linka-masterskaya/zip-backend/internal/cryptox"
	"github.com/Linka-masterskaya/zip-backend/internal/db"
	"github.com/Linka-masterskaya/zip-backend/internal/logger"
	"github.com/Linka-masterskaya/zip-backend/internal/mailer"
	"github.com/Linka-masterskaya/zip-backend/internal/metrics"
	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
	"github.com/Linka-masterskaya/zip-backend/internal/pack"
	"github.com/Linka-masterskaya/zip-backend/internal/profile"
	"github.com/Linka-masterskaya/zip-backend/internal/storage"
	"github.com/Linka-masterskaya/zip-backend/migrations"
)

var (
	version   string
	buildTime string
)

func main() {
	if err := run(); err != nil {
		slog.Error("application failed", "err", err)
		os.Exit(1)
	}
}

func run() error {
	deps, err := initInfra()
	if err != nil {
		return err
	}
	defer deps.db.Close()
	defer func() {
		if err := deps.nc.Drain(); err != nil {
			slog.Error("nats drain", logger.Err(err))
		}
	}()

	packRepo := pack.NewRepository(deps.redis)
	packService := pack.NewService(packRepo, deps.pub)
	packHandler := pack.NewHandler(packService)

	authRepo := auth.NewAuthRepo(deps.db)

	authCfg := auth.Config{
		JWTSecret:                deps.cfg.JWT.Secret,
		FrontendURL:              deps.cfg.App.FrontendURL,
		AccessTokenTTL:           deps.cfg.Auth.AccessTokenTTL,
		RefreshTokenTTL:          deps.cfg.Auth.RefreshTokenTTL,
		VerifyEmailTokenTTL:      deps.cfg.Auth.VerifyEmailTokenTTL,
		RequireEmailVerification: deps.cfg.Auth.RequireEmailVerification,
		CookieSecure:             deps.cfg.Auth.CookieSecure,
	}

	authService := auth.NewAuthService(
		authRepo,
		deps.redis,
		deps.mailer,
		authCfg,
		deps.crypto,
	)

	packRateLimit := middleware.RateLimit(deps.redis, "packs_api", int64(deps.cfg.Auth.PackRateLimit), 1*time.Minute, deps.cfg.App.TrustedProxies)
	loginRateLimit := middleware.RateLimit(deps.redis, "login", int64(deps.cfg.Auth.LoginRateLimit), 1*time.Minute, deps.cfg.App.TrustedProxies)
	forgotRateLimit := middleware.RateLimit(deps.redis, "forgot", int64(deps.cfg.Auth.ForgotRateLimit), 1*time.Minute, deps.cfg.App.TrustedProxies)
	resetRateLimit := middleware.RateLimit(deps.redis, "reset", int64(deps.cfg.Auth.ResetRateLimit), 1*time.Minute, deps.cfg.App.TrustedProxies)
	verifyResendRateLimit := middleware.RateLimit(deps.redis, "verify-resend", int64(deps.cfg.Auth.VerifyResendRateLimit), 1*time.Minute, deps.cfg.App.TrustedProxies)
	emailConfirmRateLimit := middleware.RateLimit(deps.redis, "email-confirm", int64(deps.cfg.Auth.EmailConfirmRateLimit), 1*time.Minute, deps.cfg.App.TrustedProxies)

	mainMux := http.NewServeMux()
	mainMux.Handle("POST /api/v1/packs", packRateLimit(middleware.ErrorMiddleware(packHandler.CreatePack)))
	mainMux.Handle("GET /api/v1/packs/{id}", packRateLimit(middleware.ErrorMiddleware(packHandler.GetPack)))
	mainMux.Handle("GET /api/v1/packs", packRateLimit(middleware.ErrorMiddleware(packHandler.ListPacks)))

	authHandler := auth.NewAuthHandler(authService, authCfg)

	mainMux.Handle(
		"POST /auth/login",
		loginRateLimit(
			middleware.ErrorMiddleware(authHandler.Login),
		),
	)

	mainMux.Handle(
		"POST /auth/forgot",
		forgotRateLimit(
			middleware.ErrorMiddleware(authHandler.ForgotPassword),
		),
	)

	mainMux.Handle(
		"POST /auth/reset",
		resetRateLimit(
			middleware.ErrorMiddleware(authHandler.ResetPassword),
		),
	)

	mainMux.Handle(
		"POST /auth/verify-resend",
		verifyResendRateLimit(
			middleware.ErrorMiddleware(authHandler.VerifyResend),
		),
	)

	mainMux.Handle(
		"POST /auth/email-confirm",
		emailConfirmRateLimit(
			middleware.ErrorMiddleware(authHandler.EmailConfirm),
		),
	)

	authMW := middleware.NewAuthMW([]byte(deps.cfg.JWT.Secret))
	authHandler.RegisterRoutes(mainMux, authMW, deps.redis, deps.cfg)

	profileRepo := profile.NewRepository(deps.db)
	profileService := profile.NewService(profileRepo, deps.storage)
	profileHandler := profile.NewHandler(profileService)
	mainMux.Handle(
		"PUT /api/v1/profile/me/avatar",
		middleware.ErrorMiddleware(authMW.AuthMiddleware(profileHandler.UploadAvatar)),
	)
	mainMux.Handle(
		"DELETE /api/v1/profile/me/avatar",
		middleware.ErrorMiddleware(authMW.AuthMiddleware(profileHandler.DeleteAvatar)),
	)

	wrappedHandler := middleware.Chain(
		mainMux,
		middleware.RecoveryMiddleware,
		middleware.RequestIDMiddleware,
		middleware.Metrics,
		middleware.CORSMiddleware(deps.cfg.App.FrontendURL),
		middleware.SecurityHeaders,
	)

	srv := &http.Server{
		Addr:         ":" + deps.cfg.App.Port,
		Handler:      wrappedHandler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	metricsMux := http.NewServeMux()
	metricsMux.Handle("GET /metrics", metrics.NewHandler())
	metricsMux.HandleFunc("GET /health", healthHandler(deps.cfg.App.Env))
	metricsMux.HandleFunc("GET /readyz", readyzHandler(deps.redis))

	metricsSrv := &http.Server{
		Addr:         ":9091",
		Handler:      metricsMux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}

	slog.Info("starting server",
		"addr", srv.Addr,
		"env", deps.cfg.App.Env,
		"version", version,
		"buildTime", buildTime,
	)

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("main server error", logger.Err(err))
			os.Exit(1)
		}
	}()

	go func() {
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
	if err := deps.redis.Close(); err != nil {
		slog.Error("redis close error", logger.Err(err))
	}

	return nil
}

type infra struct {
	cfg     *config.Config
	db      *pgxpool.Pool
	redis   *cache.Client
	nc      *nats.Conn
	pub     *broker.Publisher
	crypto  *cryptox.Cryptox
	mailer  *mailer.SMTPSender
	storage *storage.Client
}

func initInfra() (*infra, error) {
	cfgPath := os.Getenv("CONFIG_PATH")
	if cfgPath == "" {
		cfgPath = "config/config.dev.yml"
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("config load: %w", err)
	}

	runMigrationsIfNeeded(cfg)
	logger.Init(cfg.App.Env)
	metrics.Initialize()

	storageClient, err := storage.New(cfg.MinIO)
	if err != nil {
		return nil, fmt.Errorf("minio connect: %w", err)
	}

	nc, pub, err := initNATS(cfg.NATS)
	if err != nil {
		return nil, fmt.Errorf("nats init: %w", err)
	}

	redisClient, err := cache.NewClient(cache.Config{
		URL:        cfg.Redis.URL,
		ClientName: cfg.Redis.ClientName,
		PoolSize:   cfg.Redis.PoolSize,
	})
	if err != nil {
		return nil, fmt.Errorf("redis init: %w", err)
	}

	dbPool, err := db.New(cfg.DB)
	if err != nil {
		return nil, fmt.Errorf("postgres init: %w", err)
	}

	cryptoClient, err := cryptox.New(cfg.Crypto.AESKey, cfg.Crypto.HMACKey)
	if err != nil {
		return nil, fmt.Errorf("cryptox init: %w", err)
	}

	smtpSender, err := mailer.NewSMTPSender(cfg.SMTP, cfg.App.PublicURL)
	if err != nil {
		return nil, fmt.Errorf("smtp init: %w", err)
	}

	return &infra{
		cfg: cfg, db: dbPool, redis: redisClient,
		nc: nc, pub: pub, crypto: cryptoClient, mailer: smtpSender,
		storage: storageClient,
	}, nil
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
			slog.Error("readyz: redis unavailable", logger.Err(err))
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
		slog.Error("failed to connect to postgres for migration", logger.Err(err))
		os.Exit(1)
	}
	defer func() {
		if err := dbConn.Close(); err != nil {
			slog.Error("failed to close db connection after migration", logger.Err(err))
		}
	}()

	if err := migrations.Run(dbConn); err != nil {
		log.Fatalf("Migration failed: %v", err)
	}
	log.Println("Migrations completed. Exiting.")
	os.Exit(0)
}
