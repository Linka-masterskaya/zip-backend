// Package broker provides NATS JetStream messaging for asynchronous job
// processing (TTS generation, ClamAV file scanning, LLM card editing).
package broker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/config"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

func onDisconnect(_ *nats.Conn, err error) {
	if err == nil {
		slog.Info("nats disconnected (graceful)")
		return
	}
	slog.Error("nats disconnected", "err", err)
}

func reconnectDelay(attempts int) time.Duration {
	delay := time.Second * time.Duration(math.Pow(2, float64(attempts)))
	if delay > 30*time.Second {
		delay = 30 * time.Second
	}
	return delay
}

func onReconnect(_ *nats.Conn) {
	slog.Info("nats reconnected")
}

// Conn is a NATS connection that reports when it has finished closing.
// The channel is closed from the library's ClosedHandler, which fires once the
// connection is gone for good: after an explicit Close, after a completed drain,
// or after reconnect attempts run out.
type Conn struct {
	NC     *nats.Conn
	closed chan struct{}
}

// New creates a NATS connection with reconnect handling, backoff, and lifecycle logging.
func New(cfg config.ConnectionConfig) (*Conn, error) {
	closed := make(chan struct{})

	nc, err := nats.Connect(cfg.URL,
		nats.MaxReconnects(cfg.MaxReconnect),
		nats.PingInterval(cfg.PingInterval),
		nats.MaxPingsOutstanding(cfg.MaxPingsOutstanding),
		nats.CustomReconnectDelay(reconnectDelay),
		nats.DisconnectErrHandler(onDisconnect),
		nats.ReconnectHandler(onReconnect),
		nats.ClosedHandler(func(_ *nats.Conn) {
			slog.Info("nats connection closed")
			close(closed)
		}),
		nats.DrainTimeout(cfg.DrainTimeout),
	)
	if err != nil {
		return nil, fmt.Errorf("nats.New: %w", err)
	}
	return &Conn{NC: nc, closed: closed}, nil
}

// DrainAndWait drains the connection and blocks until it is actually closed.
// Drain itself only starts the process: it hands pending publishes and acks to
// the server in the background, so returning immediately would let the caller
// close the pool underneath an unfinished flush. If ctx expires first, the
// connection is torn down and ctx.Err() is returned.
func (c *Conn) DrainAndWait(ctx context.Context) error {
	if err := c.NC.Drain(); err != nil {
		if errors.Is(err, nats.ErrConnectionClosed) {
			return nil
		}
		return err
	}

	select {
	case <-c.closed:
		return nil
	case <-ctx.Done():
		c.NC.Close()
		return ctx.Err()
	}
}

// InitStreams creates or updates the AI_JOBS stream config.
func InitStreams(cfg config.StreamConfig, js jetstream.JetStream) error {
	ctx, cancel := context.WithTimeout(context.Background(), cfg.InitTimeout)
	defer cancel()

	_, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:       cfg.Name,
		Storage:    jetstream.FileStorage,
		Retention:  jetstream.WorkQueuePolicy,
		Subjects:   []string{SubjectLLMRequest, SubjectLLMResponseAll, SubjectTTSJobs, SubjectTTSDoneAll, SubjectClamAVJobs},
		MaxAge:     cfg.MaxAge,
		MaxBytes:   cfg.MaxBytes,
		MaxMsgs:    cfg.MaxMsgs,
		Duplicates: cfg.Duplicates,
	})
	if err != nil {
		return fmt.Errorf("InitStreams: create or update stream: %w", err)
	}

	return nil
}

func (c *Conn) IsConnected() bool { return c.NC.IsConnected() }
