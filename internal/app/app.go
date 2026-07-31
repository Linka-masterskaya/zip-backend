package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os/signal"
	"syscall"

	"golang.org/x/sync/errgroup"

	"github.com/Linka-masterskaya/zip-backend/internal/config"
	"github.com/Linka-masterskaya/zip-backend/internal/httpapi"
	"github.com/Linka-masterskaya/zip-backend/internal/logger"
	"github.com/Linka-masterskaya/zip-backend/internal/metrics"
)

var (
	// Version and BuildTime are injected at build time via -ldflags.
	Version   string
	BuildTime string
)

// App owns the assembled servers and everything they must release on shutdown.
type App struct {
	cfg        *config.Config
	closer     *Closer
	apiSrv     *http.Server
	metricsSrv *http.Server
}

// Bootstrap loads configuration, creates infrastructure and wires the servers.
// On failure it releases whatever was already created.
func Bootstrap(cfgPath string) (*App, error) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("config load: %w", err)
	}

	logger.Init(cfg.App.Env)
	metrics.Initialize()

	closer := &Closer{}

	// Any failure past this point must release what the closer already holds.
	abort := func(err error) (*App, error) {
		ctx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
		defer cancel()
		if closeErr := closer.Close(ctx); closeErr != nil {
			slog.Error("cleanup after failed bootstrap", logger.Err(closeErr))
		}
		return nil, err
	}

	in, err := initInfra(cfg, closer)
	if err != nil {
		return abort(err)
	}

	mods, err := buildModules(in)
	if err != nil {
		return abort(err)
	}

	rl := httpapi.NewRateLimits(in.redis, cfg)

	return &App{
		cfg:        cfg,
		closer:     closer,
		apiSrv:     newAPIServer(cfg, mods, rl, in.redis),
		metricsSrv: newMetricsServer(cfg, mods.checker),
	}, nil
}

// Run serves until a termination signal arrives or a server fails, then shuts
// everything down in order: HTTP servers first, then infrastructure in LIFO order.
func (a *App) Run(ctx context.Context) error {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	slog.Info("starting server",
		"addr", a.apiSrv.Addr,
		"metrics", a.metricsSrv.Addr,
		"env", a.cfg.App.Env,
		"version", Version,
		"buildTime", BuildTime,
	)

	// Both servers are load-bearing: /livez and /readyz live on the metrics
	// port, so a pod that loses it either never passes readiness or restarts
	// forever on liveness. Failing loudly beats serving traffic unprobeable.
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return serveHTTP(gctx, a.apiSrv) })
	g.Go(func() error { return serveHTTP(gctx, a.metricsSrv) })
	g.Go(func() error {
		<-gctx.Done()
		return a.shutdown()
	})

	return g.Wait()
}

func (a *App) shutdown() error {
	slog.Info("shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), a.cfg.Server.ShutdownTimeout)
	defer cancel()

	var firstErr error
	if err := a.metricsSrv.Shutdown(ctx); err != nil {
		slog.Error("metrics server shutdown", logger.Err(err))
		firstErr = err
	}
	if err := a.apiSrv.Shutdown(ctx); err != nil {
		slog.Error("api server shutdown", logger.Err(err))
		if firstErr == nil {
			firstErr = err
		}
	}
	if err := a.closer.Close(ctx); err != nil && firstErr == nil {
		firstErr = err
	}

	return firstErr
}
