// Package broker provides NATS JetStream messaging for asynchronous job
// processing (TTS generation, ClamAV file scanning, LLM card editing).
package broker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/Linka-masterskaya/zip-backend/internal/config"
	"github.com/nats-io/nats.go/jetstream"
)

// Consumer reads asynchronous jobs (TTS, ClamAV) from the AI_JOBS stream.
type Consumer struct {
	js         jetstream.JetStream
	streamName string
	cfg        config.ConsumersConfig
}

// NewConsumer creates a Consumer for the given stream using the provided consumer settings.
func NewConsumer(js jetstream.JetStream, streamName string, cfg config.ConsumersConfig) *Consumer {
	return &Consumer{js: js, streamName: streamName, cfg: cfg}
}

// ConsumeTTSJobs reads text-to-speech jobs and invokes handler for each one.
func (c *Consumer) ConsumeTTSJobs(ctx context.Context, handler TTSJobHandler) error {
	return consumeJobs(ctx, c.js, c.streamName, SubjectTTSJobs, c.cfg.TTS, handler)
}

// ConsumeClamAVJobs reads ClamAV file scan jobs and invokes handler for each one.
func (c *Consumer) ConsumeClamAVJobs(ctx context.Context, handler ClamAVJobHandler) error {
	return consumeJobs(ctx, c.js, c.streamName, SubjectClamAVJobs, c.cfg.ClamAV, handler)
}

func consumeJobs[T any](
	ctx context.Context,
	js jetstream.JetStream,
	streamName, filterSubject string,
	cfg config.ConsumerSettings,
	handler func(context.Context, T) error,
) error {
	cons, err := js.CreateOrUpdateConsumer(ctx, streamName, jetstream.ConsumerConfig{
		Durable:       cfg.Durable,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       cfg.AckWait,
		MaxDeliver:    cfg.MaxDeliver,
		MaxWaiting:    1,
		FilterSubject: filterSubject,
	})
	if err != nil {
		return fmt.Errorf("consumeJobs[%s]: create consumer: %w", cfg.Durable, err)
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		msgs, err := cons.Fetch(1, jetstream.FetchMaxWait(cfg.FetchMaxWait))
		if err != nil {
			return fmt.Errorf("consumeJobs[%s]: fetch: %w", cfg.Durable, err)
		}

		for msg := range msgs.Messages() {
			var job T
			if err := json.Unmarshal(msg.Data(), &job); err != nil {
				slog.Error("consumeJobs: unmarshal, terminating message", "consumer", cfg.Durable, "err", err)
				if termErr := msg.Term(); termErr != nil {
					slog.Error("consumeJobs: term failed", "consumer", cfg.Durable, "err", termErr)
				}
				continue
			}

			if err := handler(ctx, job); err != nil {
				slog.Error("consumeJobs: handler", "consumer", cfg.Durable, "err", err)
				if nakErr := msg.Nak(); nakErr != nil {
					slog.Error("consumeJobs: nak failed", "consumer", cfg.Durable, "err", nakErr)
				}
				continue
			}

			if ackErr := msg.Ack(); ackErr != nil {
				slog.Error("consumeJobs: ack failed", "consumer", cfg.Durable, "err", ackErr)
			}
		}

		if err := msgs.Error(); err != nil {
			slog.Error("consumeJobs: fetch batch error", "consumer", cfg.Durable, "err", err)
		}
	}
}
