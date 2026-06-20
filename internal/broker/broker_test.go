package broker_test

import (
	"context"
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

func loadTestNATSConfig(t *testing.T, url string) config.NATSConfig {
	cfg, err := config.Load("../../config/config.dev.yml")
	require.NoError(t, err)

	cfg.NATS.Connection.URL = url
	return cfg.NATS
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
	natsCfg := loadTestNATSConfig(t, url)

	_, js := setupBroker(t, natsCfg)

	info, err := js.Stream(context.Background(), natsCfg.Stream.Name)
	require.NoError(t, err)
	require.Equal(t, natsCfg.Stream.Name, info.CachedInfo().Config.Name)

	// idempotency check — second call should not error
	require.NoError(t, broker.InitStreams(natsCfg.Stream, js))
}

func TestPublishAndConsumeTTS(t *testing.T) {
	url := startTestNATS(t)
	natsCfg := loadTestNATSConfig(t, url)

	_, js := setupBroker(t, natsCfg)

	publisher := broker.NewPublisher(js)
	consumer := broker.NewConsumer(js, natsCfg.Stream.Name, natsCfg.Consumers)

	job := broker.TTSJob{PackID: "p1", CardID: "c1", Text: "hello", Voice: "ru"}
	require.NoError(t, publisher.PublishTTSJob(context.Background(), job))

	ctx, cancel := context.WithCancel(context.Background())
	received := make(chan broker.TTSJob, 1)

	go func() {
		_ = consumer.ConsumeTTSJobs(ctx, func(_ context.Context, j broker.TTSJob) error {
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
	natsCfg := loadTestNATSConfig(t, url)

	_, js := setupBroker(t, natsCfg)

	publisher := broker.NewPublisher(js)
	consumer := broker.NewConsumer(js, natsCfg.Stream.Name, natsCfg.Consumers)

	job := broker.ClamAVJob{FileID: "f1", FilePath: "/tmp/f1"}
	require.NoError(t, publisher.PublishClamAVJob(context.Background(), job))

	ctx, cancel := context.WithCancel(context.Background())
	received := make(chan broker.ClamAVJob, 1)

	go func() {
		_ = consumer.ConsumeClamAVJobs(ctx, func(_ context.Context, j broker.ClamAVJob) error {
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
