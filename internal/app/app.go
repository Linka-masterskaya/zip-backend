package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os/signal"
	"sync"
	"syscall"
	"time"

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
	cfg             *config.Config
	closer          *Closer
	apiSrv          *http.Server
	metricsSrv      *http.Server
	backgrounds     []func(context.Context) error
	voiceRefreshRun func(context.Context)
	ttsCleanupRun   func(context.Context)
	mediaOrphanRun  func(context.Context)
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
		ctx, cancel := context.WithTimeout(context.Background(), cfg.Server.InfraShutdownTimeout)
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

	mods, err := buildModules(in, closer)
	if err != nil {
		return abort(err)
	}

	rl := httpapi.NewRateLimits(in.redis, cfg)

	// Уборка неподтверждённых регистраций живёт своим контекстом: её надо
	// остановить раньше, чем закроется пул соединений.
	cleanupCtx, stopCleanup := context.WithCancel(context.Background())
	cleanupDone := make(chan struct{})
	go func() {
		defer close(cleanupDone)
		mods.cleaner.Run(cleanupCtx)
	}()
	closer.Add("registration cleanup", func(ctx context.Context) error {
		stopCleanup()
		select {
		case <-cleanupDone:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})

	return &App{
		cfg:         cfg,
		closer:      closer,
		apiSrv:      newAPIServer(cfg, mods, rl, in.redis, in.db),
		metricsSrv:  newMetricsServer(cfg, mods.checker),
		backgrounds: mods.backgrounds,
		voiceRefreshRun: func(ctx context.Context) {
			mods.voiceRefresher.Run(ctx, cfg.Cron.VoiceRefresh.Interval)
		},
		ttsCleanupRun: func(ctx context.Context) {
			mods.ttsCleaner.Run(ctx, cfg.Cron.TTSCleanup.Interval)
		},
		mediaOrphanRun: func(ctx context.Context) {
			mods.mediaOrphans.Run(ctx, cfg.Cron.MediaOrphanScanner.Interval)
		},
	}, nil
}

// Run serves until a termination signal arrives or a server fails, then shuts
// everything down in order: the API server first, then background workers,
// then infrastructure in LIFO order, and the metrics server last so probes
// stay answerable for the whole shutdown.
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

	workCtx, cancelWork := context.WithCancel(context.Background())
	var backgroundWG sync.WaitGroup
	// The worker name is only for logs: a WaitGroup cannot report who is still
	// running, so the stop records are the only way to tell which worker held up
	// a shutdown that timed out.
	startBackground := func(name string, run func(context.Context) error) {
		backgroundWG.Add(1)
		g.Go(func() error {
			defer func() {
				slog.Debug("background worker stopped", "worker", name)
				backgroundWG.Done()
			}()
			return run(workCtx)
		})
	}

	for i, run := range a.backgrounds {
		startBackground(fmt.Sprintf("background[%d]", i), run)
	}

	startBackground("voice refresh", func(ctx context.Context) error {
		a.voiceRefreshRun(ctx)
		return nil
	})

	startBackground("tts cleanup", func(ctx context.Context) error {
		a.ttsCleanupRun(ctx)
		return nil
	})

	startBackground("media orphan scanner", func(ctx context.Context) error {
		a.mediaOrphanRun(ctx)
		return nil
	})

	g.Go(func() error {
		<-gctx.Done()

		return a.shutdown(cancelWork, &backgroundWG)
	})

	return g.Wait()
}

func (a *App) shutdown(cancelWork context.CancelFunc, backgroundWG *sync.WaitGroup) error {
	slog.Info("shutting down...")

	httpCtx, cancelHTTP := context.WithTimeout(
		context.Background(),
		a.cfg.Server.ShutdownTimeout,
	)
	defer cancelHTTP()

	var firstErr error
	if err := a.apiSrv.Shutdown(httpCtx); err != nil {
		slog.Error("api server shutdown", logger.Err(err))
		if firstErr == nil {
			firstErr = err
		}
	}

	cancelWork()
	if !waitWithTimeout(backgroundWG, a.cfg.Server.WorkersShutdownTimeout) {
		slog.Warn("background workers did not finish in time")
	}

	// Infrastructure gets its own deadline: a slow HTTP drain must never skip
	// closing database, Redis and NATS connections.
	infraCtx, cancelInfra := context.WithTimeout(
		context.Background(),
		a.cfg.Server.InfraShutdownTimeout,
	)
	defer cancelInfra()

	if err := a.closer.Close(infraCtx); err != nil {
		if firstErr == nil {
			firstErr = err
		}
	}

	metricsCtx, cancelMetrics := context.WithTimeout(
		context.Background(),
		a.cfg.Server.ShutdownTimeout,
	)
	defer cancelMetrics()
	if err := a.metricsSrv.Shutdown(metricsCtx); err != nil {
		slog.Error("metrics server shutdown", logger.Err(err))
		if firstErr == nil {
			firstErr = err
		}
	}

	return firstErr
}

// waitWithTimeout waits for wg and reports whether it finished within timeout.
// On timeout the inner goroutine stays parked on wg.Wait() — Wait takes no
// context and cannot be interrupted — so it lives until the process exits.
// That is acceptable here: the only caller is shutdown, and the process is
// already on its way out. The Debug record each worker writes on exit tells
// which one failed to finish.
func waitWithTimeout(wg *sync.WaitGroup, timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	t := time.NewTimer(timeout)
	defer t.Stop()

	select {
	case <-done:
		return true
	case <-t.C:
		return false
	}
}
