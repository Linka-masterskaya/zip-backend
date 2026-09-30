// Package broker provides NATS JetStream messaging for asynchronous TTS job processing.
package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/config"
	"github.com/nats-io/nats.go/jetstream"
)

// Consumer reads asynchronous TTS jobs from the AI_JOBS stream.
type Consumer struct {
	js         jetstream.JetStream
	streamName string
	cfg        config.ConsumersConfig

	// ctx is the consumer's own lifetime, not a request scope, and is
	// deliberately stored on the struct. Handlers run on it so that stopping the
	// consumer does not tear down the job already in flight: the fetch loop exits
	// on stop, while ctx stays alive until the job is done. cancel is the last
	// resort, used by Shutdown once its budget runs out.
	ctx    context.Context
	cancel context.CancelFunc

	stop     chan struct{}
	stopOnce sync.Once
	started  atomic.Bool
	wg       sync.WaitGroup
}

// NewConsumer creates a Consumer for the given stream using the provided consumer settings.
func NewConsumer(js jetstream.JetStream, streamName string, cfg config.ConsumersConfig) *Consumer {
	ctx, cancel := context.WithCancel(context.Background())
	return &Consumer{
		js:         js,
		streamName: streamName,
		cfg:        cfg,
		ctx:        ctx,
		cancel:     cancel,
		stop:       make(chan struct{}),
	}
}

// Start creates the TTS JetStream consumer and runs its fetch loop in the
// background. Creation errors surface synchronously, so a misconfigured
// consumer fails at bootstrap instead of silently never delivering jobs.
func (c *Consumer) Start(handler TTSJobHandler) error {
	if !c.started.CompareAndSwap(false, true) {
		return errors.New("Start: consumer already started")
	}
	cons, err := createConsumer(c.ctx, c.js, c.streamName, SubjectTTSJobs, c.cfg.TTS)
	if err != nil {
		c.started.Store(false)
		return err
	}

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		runLoop(c, cons, c.cfg.TTS, handler)
	}()

	return nil
}

func createConsumer(
	ctx context.Context,
	js jetstream.JetStream,
	streamName, filterSubject string,
	cfg config.ConsumerSettings,
) (jetstream.Consumer, error) {
	cons, err := js.CreateOrUpdateConsumer(ctx, streamName, jetstream.ConsumerConfig{
		Durable:       cfg.Durable,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       cfg.AckWait,
		MaxDeliver:    cfg.MaxDeliver,
		MaxWaiting:    1,
		FilterSubject: filterSubject,
	})
	if err != nil {
		return nil, fmt.Errorf("createConsumer[%s]: create consumer: %w", cfg.Durable, err)
	}

	return cons, nil
}

func runLoop[T any](
	c *Consumer,
	cons jetstream.Consumer,
	cfg config.ConsumerSettings,
	handler func(context.Context, T, bool) error,
) {
	backoff := time.Second
	maxBackoff := cfg.MaxBackoff

	for {
		select {
		case <-c.stop:
			return
		default:
		}

		msgs, err := cons.Fetch(1, jetstream.FetchMaxWait(cfg.FetchMaxWait))
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return
			}
			slog.ErrorContext(c.ctx, "runLoop: fetch failed, retrying", "consumer", cfg.Durable, "err", err)
			select {
			case <-c.ctx.Done():
				return
			case <-c.stop:
				return
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}
		backoff = time.Second

		processBatch(c.ctx, msgs, cfg, handler)
	}
}

func processBatch[T any](
	ctx context.Context,
	msgs jetstream.MessageBatch,
	cfg config.ConsumerSettings,
	handler func(context.Context, T, bool) error,
) {
	for msg := range msgs.Messages() {
		handleMsg(ctx, msg, cfg, handler)
	}

	if err := msgs.Error(); err != nil {
		slog.Error("processBatch: fetch batch error", "consumer", cfg.Durable, "err", err)
	}
}

func handleMsg[T any](
	ctx context.Context,
	msg jetstream.Msg,
	cfg config.ConsumerSettings,
	handler func(context.Context, T, bool) error,
) {
	delay := 2 * time.Second
	defer func() {
		if r := recover(); r != nil {
			slog.Error("processBatch: panic", "stack", string(debug.Stack()), "consumer", cfg.Durable, "panic", r)
			if err := msg.NakWithDelay(delay); err != nil {
				slog.Error("processBatch: nak failed", "err", err)
			}
		}
	}()

	var job T
	if err := json.Unmarshal(msg.Data(), &job); err != nil {
		slog.Error("processBatch: unmarshal, terminating message", "consumer", cfg.Durable, "err", err)
		if termErr := msg.Term(); termErr != nil {
			slog.Error("processBatch: term failed", "consumer", cfg.Durable, "err", termErr)
		}
		return
	}

	meta, metaErr := msg.Metadata()
	isLastAttempt := metaErr == nil && cfg.MaxDeliver > 0 && meta.NumDelivered >= uint64(cfg.MaxDeliver)
	if metaErr == nil {
		n := meta.NumDelivered
		if n > 10 {
			n = 10
		}
		delay = time.Duration(n) * 2 * time.Second
	}

	stop := keepAlive(msg, cfg.AckWait/2)
	defer stop()

	err := handler(ctx, job, isLastAttempt)
	if err != nil {
		slog.Error("processBatch: handler", "consumer", cfg.Durable, "err", err)
		if nakErr := msg.NakWithDelay(delay); nakErr != nil {
			slog.Error("processBatch: nak failed", "consumer", cfg.Durable, "err", nakErr)
		}
		return
	}

	if ackErr := msg.Ack(); ackErr != nil {
		slog.Error("processBatch: ack failed", "consumer", cfg.Durable, "err", ackErr)
	}
}

func keepAlive(msg jetstream.Msg, every time.Duration) func() {
	if every <= 0 {
		return func() {}
	}

	done := make(chan struct{})
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				if err := msg.InProgress(); err != nil {
					slog.Warn("keepAlive: in progress failed", "err", err)
				}
			}
		}
	}()
	return func() { close(done) }
}

// Shutdown stops the consumer in two phases. First it closes the stop channel,
// which makes the fetch loop exit once the message it already holds is acked —
// the job keeps running on the consumer's own context, so nothing is torn down
// mid-flight. Then it waits for the loop to return within the caller's budget.
// Only if that budget expires does it cancel the context, aborting the job so
// the message is redelivered after ack_wait. Safe to call more than once.
func (c *Consumer) Shutdown(ctx context.Context) error {
	c.Stop()

	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		c.cancel()
		return nil
	case <-ctx.Done():
		c.cancel()
		return ctx.Err()
	}
}

// Stop keeps the fetch loop from starting another fetch and returns immediately.
// It does not cut the loop short: a fetch already in progress cannot be
// interrupted, so the message it returns is still handled, and the loop leaves
// up to FetchMaxWait later. The job in flight keeps running on the consumer's
// context; waiting for it and owning the budget is Shutdown's job.
func (c *Consumer) Stop() {
	c.stopOnce.Do(func() { close(c.stop) })
}
