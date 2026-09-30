package broker_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"

	"github.com/Linka-masterskaya/zip-backend/internal/broker"
	"github.com/Linka-masterskaya/zip-backend/internal/config"
)

func startTestNATS(t *testing.T) string {
	opts := &server.Options{
		Port:      -1,
		JetStream: true,
		StoreDir:  t.TempDir(),
	}
	s, err := server.NewServer(opts)
	require.NoError(t, err)

	go s.Start()
	if !s.ReadyForConnections(5 * time.Second) {
		t.Fatal("nats server not ready")
	}

	t.Cleanup(s.Shutdown)
	return s.ClientURL()
}

// testNATSConfig builds a minimal static NATS config for tests (no file/config.Load dependency).
func testNATSConfig(url string) config.NATSConfig {
	return config.NATSConfig{
		Connection: config.ConnectionConfig{
			URL:                 url,
			MaxReconnect:        -1,
			PingInterval:        2 * time.Second,
			MaxPingsOutstanding: 2,
		},
		Stream: config.StreamConfig{
			Name:        "AI_JOBS",
			InitTimeout: 5 * time.Second,
			MaxAge:      24 * time.Hour,
			MaxBytes:    100 << 20,
			MaxMsgs:     100000,
			Duplicates:  5 * time.Minute,
		},
		Consumers: config.ConsumersConfig{
			TTS:    config.ConsumerSettings{Durable: "test-tts", AckWait: 30 * time.Second, MaxDeliver: 3, FetchMaxWait: time.Second},
			ClamAV: config.ConsumerSettings{Durable: "test-clamav", AckWait: 30 * time.Second, MaxDeliver: 3, FetchMaxWait: time.Second},
		},
	}
}

func setupBroker(t *testing.T, natsCfg config.NATSConfig) (*broker.Conn, jetstream.JetStream) {
	nc, err := broker.New(natsCfg.Connection)
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = nc.DrainAndWait(ctx)
	})

	js, err := jetstream.New(nc.NC)
	require.NoError(t, err)

	require.NoError(t, broker.InitStreams(natsCfg.Stream, js))

	return nc, js
}

func TestInitStreams(t *testing.T) {
	url := startTestNATS(t)
	natsCfg := testNATSConfig(url)

	_, js := setupBroker(t, natsCfg)

	info, err := js.Stream(context.Background(), natsCfg.Stream.Name)
	require.NoError(t, err)
	require.Equal(t, natsCfg.Stream.Name, info.CachedInfo().Config.Name)

	// idempotency check — second call should not error
	require.NoError(t, broker.InitStreams(natsCfg.Stream, js))
}

func TestPublishAndConsumeTTS(t *testing.T) {
	url := startTestNATS(t)
	natsCfg := testNATSConfig(url)

	_, js := setupBroker(t, natsCfg)

	publisher := broker.NewPublisher(js)
	consumer := broker.NewConsumer(js, natsCfg.Stream.Name, natsCfg.Consumers)

	job := broker.TTSJob{JobId: "j1", OrgID: "org1", UserID: "u1", Text: "hello", Voice: "alena"}
	require.NoError(t, publisher.PublishTTSJob(context.Background(), job))

	received := make(chan broker.TTSJob, 1)

	require.NoError(t, consumer.Start(func(_ context.Context, j broker.TTSJob, _ bool) error {
		received <- j
		return nil
	}))
	t.Cleanup(func() { _ = consumer.Shutdown(context.Background()) })

	select {
	case got := <-received:
		require.Equal(t, job, got)
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for message")
	}
}

func ttsCfgWithAckWait(url string, ackWait time.Duration) config.NATSConfig {
	cfg := testNATSConfig(url)
	cfg.Consumers.TTS.AckWait = ackWait
	return cfg
}

func runTTSConsumer(t *testing.T, cfg config.NATSConfig, js jetstream.JetStream, h broker.TTSJobHandler) *broker.Consumer {
	t.Helper()
	consumer := broker.NewConsumer(js, cfg.Stream.Name, cfg.Consumers)
	require.NoError(t, consumer.Start(h))
	t.Cleanup(func() { _ = consumer.Shutdown(context.Background()) })
	return consumer
}

func TestKeepAliveExtendsAckWait(t *testing.T) {
	url := startTestNATS(t)
	natsCfg := ttsCfgWithAckWait(url, time.Second)

	_, js := setupBroker(t, natsCfg)

	publisher := broker.NewPublisher(js)
	job := broker.TTSJob{JobId: "j1", OrgID: "org1", UserID: "u1", Text: "hello", Voice: "alena"}
	require.NoError(t, publisher.PublishTTSJob(context.Background(), job))

	var calls int32
	done := make(chan struct{}, 1)

	runTTSConsumer(t, natsCfg, js, func(_ context.Context, _ broker.TTSJob, _ bool) error {
		atomic.AddInt32(&calls, 1)
		time.Sleep(3 * time.Second)
		done <- struct{}{}
		return nil
	})

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for handler")
	}

	time.Sleep(2 * time.Second)
	require.Equal(t, int32(1), atomic.LoadInt32(&calls), "message was redelivered while handler was running")
}

func TestPanicInHandlerIsRecovered(t *testing.T) {
	url := startTestNATS(t)
	natsCfg := ttsCfgWithAckWait(url, 2*time.Second)

	_, js := setupBroker(t, natsCfg)

	publisher := broker.NewPublisher(js)
	job := broker.TTSJob{JobId: "j1", OrgID: "org1", UserID: "u1", Text: "hello", Voice: "alena"}
	require.NoError(t, publisher.PublishTTSJob(context.Background(), job))

	var calls int32
	recovered := make(chan struct{}, 1)

	runTTSConsumer(t, natsCfg, js, func(_ context.Context, _ broker.TTSJob, _ bool) error {
		if atomic.AddInt32(&calls, 1) == 1 {
			panic("boom")
		}
		recovered <- struct{}{}
		return nil
	})

	select {
	case <-recovered:
	case <-time.After(15 * time.Second):
		t.Fatal("consumer did not survive panic")
	}
}

func TestNakWithDelayBackoff(t *testing.T) {
	url := startTestNATS(t)
	natsCfg := ttsCfgWithAckWait(url, 30*time.Second)

	_, js := setupBroker(t, natsCfg)

	publisher := broker.NewPublisher(js)
	job := broker.TTSJob{JobId: "j1", OrgID: "org1", UserID: "u1", Text: "hello", Voice: "alena"}
	require.NoError(t, publisher.PublishTTSJob(context.Background(), job))

	var mu sync.Mutex
	var at []time.Time
	second := make(chan struct{}, 1)

	runTTSConsumer(t, natsCfg, js, func(_ context.Context, _ broker.TTSJob, _ bool) error {
		mu.Lock()
		at = append(at, time.Now())
		n := len(at)
		mu.Unlock()
		if n == 2 {
			second <- struct{}{}
		}
		return errors.New("boom")
	})

	select {
	case <-second:
	case <-time.After(15 * time.Second):
		t.Fatal("timeout waiting for redelivery")
	}

	mu.Lock()
	gap := at[1].Sub(at[0])
	mu.Unlock()
	require.GreaterOrEqual(t, gap, 1500*time.Millisecond, "redelivery was immediate, NakWithDelay not applied")
}

func TestBadPayloadIsTerminated(t *testing.T) {
	url := startTestNATS(t)
	natsCfg := ttsCfgWithAckWait(url, 2*time.Second)

	_, js := setupBroker(t, natsCfg)

	_, err := js.Publish(context.Background(), broker.SubjectTTSJobs, []byte("{not json"))
	require.NoError(t, err)

	var calls int32
	runTTSConsumer(t, natsCfg, js, func(_ context.Context, _ broker.TTSJob, _ bool) error {
		atomic.AddInt32(&calls, 1)
		return nil
	})

	time.Sleep(5 * time.Second) // больше ack_wait, переотдача успела бы произойти
	require.Zero(t, atomic.LoadInt32(&calls), "handler was called for unparsable message")
}

func TestShutdownWaitsForInFlightJob(t *testing.T) {
	url := startTestNATS(t)
	natsCfg := testNATSConfig(url)

	_, js := setupBroker(t, natsCfg)

	publisher := broker.NewPublisher(js)
	job := broker.TTSJob{JobId: "j1", OrgID: "org1", UserID: "u1", Text: "hello", Voice: "alena"}
	require.NoError(t, publisher.PublishTTSJob(context.Background(), job))

	started := make(chan struct{})
	var finished atomic.Bool

	consumer := runTTSConsumer(t, natsCfg, js, func(_ context.Context, _ broker.TTSJob, _ bool) error {
		close(started)
		time.Sleep(2 * time.Second)
		finished.Store(true)
		return nil
	})

	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("handler did not start")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, consumer.Shutdown(ctx))
	require.True(t, finished.Load(), "shutdown returned before the in-flight job finished")
}

func TestShutdownCancelsJobWhenBudgetExpires(t *testing.T) {
	url := startTestNATS(t)
	natsCfg := testNATSConfig(url)

	_, js := setupBroker(t, natsCfg)

	publisher := broker.NewPublisher(js)
	job := broker.TTSJob{JobId: "j1", OrgID: "org1", UserID: "u1", Text: "hello", Voice: "alena"}
	require.NoError(t, publisher.PublishTTSJob(context.Background(), job))

	started := make(chan struct{})
	jobCancelled := make(chan struct{})

	consumer := runTTSConsumer(t, natsCfg, js, func(ctx context.Context, _ broker.TTSJob, _ bool) error {
		close(started)
		<-ctx.Done()
		close(jobCancelled)
		return ctx.Err()
	})

	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("handler did not start")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, consumer.Shutdown(ctx), context.DeadlineExceeded)

	select {
	case <-jobCancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("job context was not cancelled after the budget expired")
	}
}

func TestShutdownIsIdempotent(t *testing.T) {
	url := startTestNATS(t)
	natsCfg := testNATSConfig(url)

	_, js := setupBroker(t, natsCfg)

	consumer := runTTSConsumer(t, natsCfg, js, func(context.Context, broker.TTSJob, bool) error {
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, consumer.Shutdown(ctx))
	require.NoError(t, consumer.Shutdown(ctx))
}

// Stop only prevents the next fetch: a message already returned by the fetch in
// flight is still handled, because that call cannot be interrupted. The loop is
// therefore drained first, and only then is the second job published.
func TestStopPreventsNewFetches(t *testing.T) {
	url := startTestNATS(t)
	natsCfg := testNATSConfig(url)

	_, js := setupBroker(t, natsCfg)

	publisher := broker.NewPublisher(js)
	first := broker.TTSJob{JobId: "j1", OrgID: "org1", UserID: "u1", Text: "hello", Voice: "alena"}
	require.NoError(t, publisher.PublishTTSJob(context.Background(), first))

	handled := make(chan string, 4)
	consumer := runTTSConsumer(t, natsCfg, js, func(_ context.Context, j broker.TTSJob, _ bool) error {
		handled <- j.JobId
		return nil
	})

	// Wait for the loop to prove it is actually fetching before stopping it.
	select {
	case got := <-handled:
		require.Equal(t, first.JobId, got)
	case <-time.After(10 * time.Second):
		t.Fatal("first job was not handled")
	}

	consumer.Stop()

	// Shutdown returns once the loop has left, so nothing is fetching any more.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, consumer.Shutdown(ctx))

	second := broker.TTSJob{JobId: "j2", OrgID: "org1", UserID: "u1", Text: "world", Voice: "alena"}
	require.NoError(t, publisher.PublishTTSJob(context.Background(), second))

	// Well past FetchMaxWait: a running loop would have picked it up by now.
	select {
	case got := <-handled:
		t.Fatalf("handler received %q after the loop stopped", got)
	case <-time.After(3 * natsCfg.Consumers.TTS.FetchMaxWait):
	}
}

func TestStartFailsOnMissingStream(t *testing.T) {
	url := startTestNATS(t)
	natsCfg := testNATSConfig(url)

	_, js := setupBroker(t, natsCfg)

	consumer := broker.NewConsumer(js, "NO_SUCH_STREAM", natsCfg.Consumers)
	require.Error(t, consumer.Start(func(context.Context, broker.TTSJob, bool) error {
		return nil
	}))
}

func TestStartTwiceIsRejected(t *testing.T) {
	url := startTestNATS(t)
	natsCfg := testNATSConfig(url)

	_, js := setupBroker(t, natsCfg)

	noop := func(context.Context, broker.TTSJob, bool) error { return nil }

	consumer := broker.NewConsumer(js, natsCfg.Stream.Name, natsCfg.Consumers)
	require.NoError(t, consumer.Start(noop))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = consumer.Shutdown(ctx)
	})

	require.Error(t, consumer.Start(noop), "second Start must not spawn another fetch loop")
}

func TestStartAfterFailureIsAllowed(t *testing.T) {
	url := startTestNATS(t)
	natsCfg := testNATSConfig(url)

	_, js := setupBroker(t, natsCfg)

	noop := func(context.Context, broker.TTSJob, bool) error { return nil }

	// The first attempt fails before anything is running, so the consumer must
	// not consider itself started.
	consumer := broker.NewConsumer(js, "NO_SUCH_STREAM", natsCfg.Consumers)
	require.Error(t, consumer.Start(noop))
	require.ErrorContains(t, consumer.Start(noop), "create consumer")
}

func TestDrainAndWaitClosesConnection(t *testing.T) {
	url := startTestNATS(t)
	natsCfg := testNATSConfig(url)

	nc, _ := setupBroker(t, natsCfg)
	require.False(t, nc.NC.IsClosed())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, nc.DrainAndWait(ctx))

	require.True(t, nc.NC.IsClosed(), "DrainAndWait returned before the connection was closed")
}

func TestDrainAndWaitIsIdempotent(t *testing.T) {
	url := startTestNATS(t)
	natsCfg := testNATSConfig(url)

	nc, _ := setupBroker(t, natsCfg)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, nc.DrainAndWait(ctx))
	require.NoError(t, nc.DrainAndWait(ctx))
}
