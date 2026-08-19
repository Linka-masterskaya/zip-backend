package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/Linka-masterskaya/zip-backend/internal/broker"
	"github.com/Linka-masterskaya/zip-backend/internal/cache"
	"github.com/Linka-masterskaya/zip-backend/internal/config"
	"github.com/Linka-masterskaya/zip-backend/internal/cryptox"
	"github.com/Linka-masterskaya/zip-backend/internal/db"
	"github.com/Linka-masterskaya/zip-backend/internal/mailer"
	"github.com/Linka-masterskaya/zip-backend/internal/storage"
)

type infra struct {
	cfg     *config.Config
	db      *pgxpool.Pool
	redis   *cache.Client
	nc      *nats.Conn
	js      jetstream.JetStream
	pub     *broker.Publisher
	crypto  *cryptox.Cryptox
	mailer  *mailer.SMTPSender
	storage *storage.Client
}

// initInfra creates every external dependency. Each resource registers its
// shutdown with the closer immediately after it is created, so a failure at any
// step still releases everything created before it.
func initInfra(cfg *config.Config, closer *Closer) (*infra, error) {
	storageClient, err := storage.New(cfg.MinIO)
	if err != nil {
		return nil, fmt.Errorf("minio connect: %w", err)
	}

	nc, pub, js, err := initNATS(cfg.NATS)
	if err != nil {
		return nil, fmt.Errorf("nats init: %w", err)
	}
	closer.Add("nats", func(context.Context) error { return nc.Drain() })

	redisClient, err := cache.NewClient(cache.Config{
		URL:        cfg.Redis.URL,
		ClientName: cfg.Redis.ClientName,
		PoolSize:   cfg.Redis.PoolSize,
	})
	if err != nil {
		return nil, fmt.Errorf("redis init: %w", err)
	}
	closer.Add("redis", func(context.Context) error { return redisClient.Close() })

	dbPool, err := db.New(cfg.DB)
	if err != nil {
		return nil, fmt.Errorf("postgres init: %w", err)
	}
	closer.Add("postgres", func(context.Context) error { dbPool.Close(); return nil })

	cryptoClient, err := cryptox.New(cfg.Crypto.AESKey, cfg.Crypto.HMACKey)
	if err != nil {
		return nil, fmt.Errorf("cryptox init: %w", err)
	}

	smtpSender, err := mailer.NewSMTPSender(cfg.SMTP, cfg.App.FrontendURL)
	if err != nil {
		return nil, fmt.Errorf("smtp init: %w", err)
	}

	return &infra{
		cfg:     cfg,
		db:      dbPool,
		redis:   redisClient,
		nc:      nc,
		js:      js,
		pub:     pub,
		crypto:  cryptoClient,
		mailer:  smtpSender,
		storage: storageClient,
	}, nil
}

func initNATS(cfg config.NATSConfig) (*nats.Conn, *broker.Publisher, jetstream.JetStream, error) {
	nc, err := broker.New(cfg.Connection)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("initNATS: %w", err)
	}
	initialized := false
	defer func() {
		if !initialized {
			nc.Close()
		}
	}()
	slog.Info("nats connected", "url", cfg.Connection.URL)

	js, err := jetstream.New(nc)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("initNATS: jetstream: %w", err)
	}

	if err := broker.InitStreams(cfg.Stream, js); err != nil {
		return nil, nil, nil, fmt.Errorf("initNATS: streams: %w", err)
	}
	slog.Info("jetstream stream ready", "stream", cfg.Stream.Name)

	initialized = true
	return nc, broker.NewPublisher(js), js, nil
}
