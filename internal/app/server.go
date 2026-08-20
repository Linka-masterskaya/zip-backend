package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	apidocs "github.com/Linka-masterskaya/zip-backend/docs/api"

	"github.com/Linka-masterskaya/zip-backend/internal/cache"
	"github.com/Linka-masterskaya/zip-backend/internal/config"
	"github.com/Linka-masterskaya/zip-backend/internal/health"
	"github.com/Linka-masterskaya/zip-backend/internal/httpapi"
	"github.com/Linka-masterskaya/zip-backend/internal/logger"
	"github.com/Linka-masterskaya/zip-backend/internal/metrics"
	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
)

// serveHTTP binds the server's address and serves until shutdown. Binding here
// rather than inside ListenAndServe makes an "address already in use" failure a
// plain error the caller can act on.
func serveHTTP(ctx context.Context, srv *http.Server) error {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", srv.Addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", srv.Addr, err)
	}
	return serveHTTPListener(srv, ln)
}

// serveHTTPListener serves on an already bound listener and reports a graceful
// shutdown as success rather than an error.
func serveHTTPListener(srv *http.Server, ln net.Listener) error {
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

type activeUserStore struct {
	db *pgxpool.Pool
}

func (s activeUserStore) IsUserActive(ctx context.Context, userID uuid.UUID) (bool, error) {
	var active bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM users WHERE id = $1 AND deleted_at IS NULL
		)
	`, userID).Scan(&active)
	if err != nil {
		return false, fmt.Errorf("check active user: %w", err)
	}
	return active, nil
}

func newAPIServer(cfg *config.Config, mods *modules, rl httpapi.RateLimits, redis *cache.Client, db *pgxpool.Pool) *http.Server {
	authMW := middleware.NewAuthMW([]byte(cfg.JWT.Secret))
	if redis != nil {
		authMW = middleware.NewAuthMW([]byte(cfg.JWT.Secret), redis)
	}
	if db != nil {
		authMW.WithActiveUserStore(activeUserStore{db: db})
	}
	mux := http.NewServeMux()

	httpapi.RegisterPackRoutes(mux, authMW, rl.Packs, mods.packs)
	httpapi.RegisterMediaRoutes(mux, authMW, rl.Packs, mods.media)
	httpapi.RegisterFolderRoutes(mux, authMW, rl.Packs, mods.folders)
	httpapi.RegisterStudentRoutes(mux, authMW, rl.Packs, mods.students)
	httpapi.RegisterPictureBankRoutes(mux, authMW, rl.Pictures, mods.pictures)
	httpapi.RegisterAuthRoutes(mux, authMW, rl, redis, mods.auth)
	httpapi.RegisterProfileRoutes(mux, authMW, rl, mods.profile)
	httpapi.RegisterTTSRoutes(mux, authMW, rl.TTS, mods.tts)
	registerDocsRoutes(mux, cfg.App.DocsEnabled)

	handler := middleware.Chain(
		mux,
		middleware.RecoveryMiddleware,
		middleware.RequestIDMiddleware,
		middleware.Metrics,
		middleware.CORSMiddleware(cfg.CORS.AllowOrigins),
		middleware.SecurityHeaders,
	)

	return &http.Server{
		Addr:         ":" + cfg.App.Port,
		Handler:      handler,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
		IdleTimeout:  cfg.Server.IdleTimeout,
	}
}

func registerDocsRoutes(mux *http.ServeMux, enabled bool) {
	if enabled {
		apidocs.RegisterRoutes(mux)
	}
}

func newMetricsServer(cfg *config.Config, checker *health.Checker) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", metrics.NewHandler())
	mux.HandleFunc("GET /health", healthHandler(cfg.App.Env))
	mux.HandleFunc("GET /livez", livezHandler)
	mux.HandleFunc("GET /readyz", readyzHandler(checker))

	return &http.Server{
		Addr:         ":" + cfg.Server.MetricsPort,
		Handler:      mux,
		ReadTimeout:  cfg.Server.MetricsReadTimeout,
		WriteTimeout: cfg.Server.MetricsWriteTimeout,
	}
}

func healthHandler(env string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"status": health.StatusOK, "env": env}); err != nil {
			slog.Error("health response encode failed", logger.Err(err))
		}
	}
}

// livezHandler always answers 200 without touching any dependency.
func livezHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(map[string]health.Status{"status": health.StatusAlive}); err != nil {
		slog.Error("failed to encode /livez response", logger.Err(err))
	}
}

// readyzHandler checks every dependency in parallel and reports the aggregate.
func readyzHandler(checker *health.Checker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status, body := checker.Run(r.Context())
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if err := json.NewEncoder(w).Encode(body); err != nil {
			slog.Error("failed to encode /readyz response", logger.Err(err))
		}
	}
}
