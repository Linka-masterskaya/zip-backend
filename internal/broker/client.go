// Package broker provides NATS JetStream messaging for asynchronous job
// processing (TTS generation, ClamAV file scanning, LLM card editing).
package broker

import (
	"context"
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

func onClosed(_ *nats.Conn) {
	slog.Info("nats connection closed")
}

// New creates a NATS connection with reconnect handling, backoff, and lifecycle logging.
func New(cfg config.ConnectionConfig) (*nats.Conn, error) {
	nc, err := nats.Connect(cfg.URL,
		nats.MaxReconnects(cfg.MaxReconnect),
		nats.PingInterval(cfg.PingInterval),
		nats.MaxPingsOutstanding(cfg.MaxPingsOutstanding),
		nats.CustomReconnectDelay(reconnectDelay),
		nats.DisconnectErrHandler(onDisconnect),
		nats.ReconnectHandler(onReconnect),
		nats.ClosedHandler(onClosed),
	)
	if err != nil {
		return nil, fmt.Errorf("nats.New: %w", err)
	}
	return nc, nil
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
