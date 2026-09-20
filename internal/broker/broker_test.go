package broker_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
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

func setupBroker(t *testing.T, natsCfg config.NATSConfig) (*nats.Conn, jetstream.JetStream) {
	nc, err := broker.New(natsCfg.Connection)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = nc.Drain()
	})

	js, err := jetstream.New(nc)
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

	ctx, cancel := context.WithCancel(context.Background())
	received := make(chan broker.TTSJob, 1)

	go func() {
		_ = consumer.ConsumeTTSJobs(ctx, func(_ context.Context, j broker.TTSJob, _ bool) error {
			received <- j
			cancel()
			return nil
		})
	}()

	select {
	case got := <-received:
		require.Equal(t, job, got)
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for message")
	}
}

func TestPublishAndConsumeClamAV(t *testing.T) {
	url := startTestNATS(t)
	natsCfg := testNATSConfig(url)

	_, js := setupBroker(t, natsCfg)

	publisher := broker.NewPublisher(js)
	consumer := broker.NewConsumer(js, natsCfg.Stream.Name, natsCfg.Consumers)

	job := broker.ClamAVJob{FileID: "f1", FilePath: "/tmp/f1"}
	require.NoError(t, publisher.PublishClamAVJob(context.Background(), job))

	ctx, cancel := context.WithCancel(context.Background())
	received := make(chan broker.ClamAVJob, 1)

	go func() {
		_ = consumer.ConsumeClamAVJobs(ctx, func(_ context.Context, j broker.ClamAVJob, _ bool) error {
			received <- j
			cancel()
			return nil
		})
	}()

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

func runTTSConsumer(t *testing.T, cfg config.NATSConfig, js jetstream.JetStream, h broker.TTSJobHandler) context.CancelFunc {
	t.Helper()
	consumer := broker.NewConsumer(js, cfg.Stream.Name, cfg.Consumers)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = consumer.ConsumeTTSJobs(ctx, h) }()
	t.Cleanup(cancel)
	return cancel
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
