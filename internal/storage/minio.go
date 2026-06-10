// Package storage provides object storage clients and helpers.
package storage

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/Linka-masterskaya/zip-backend/internal/config"
)

var (
	defaultClient *minio.Client
	defaultBucket string
)

// New creates a MinIO client, ensures the configured bucket exists, and keeps it private.
func New(cfg config.MinIOConfig) (*minio.Client, error) {
	if cfg.Endpoint == "" {
		return nil, errors.New("minio endpoint is required")
	}
	if cfg.AccessKey == "" {
		return nil, errors.New("minio access_key is required")
	}
	if cfg.SecretKey == "" {
		return nil, errors.New("minio secret_key is required")
	}
	if cfg.Bucket == "" {
		return nil, errors.New("minio bucket is required")
	}

	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("create minio client: %w", err)
	}

	ctx := context.Background()
	if err := ensureBucket(ctx, client, cfg.Bucket); err != nil {
		return nil, err
	}

	defaultClient = client
	defaultBucket = cfg.Bucket

	return client, nil
}

func ensureBucket(ctx context.Context, client *minio.Client, bucket string) error {
	exists, err := client.BucketExists(ctx, bucket)
	if err != nil {
		return fmt.Errorf("check minio bucket %q: %w", bucket, err)
	}

	if !exists {
		if err := client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
			return fmt.Errorf("create minio bucket %q: %w", bucket, err)
		}
	}

	if err := client.SetBucketPolicy(ctx, bucket, ""); err != nil {
		return fmt.Errorf("set private minio bucket policy %q: %w", bucket, err)
	}

	return nil
}

// PresignedURL returns a temporary URL for reading an object from the configured private bucket.
func PresignedURL(key string, ttl time.Duration) (string, error) {
	if defaultClient == nil {
		return "", errors.New("minio client is not initialized")
	}
	if key == "" {
		return "", errors.New("object key is required")
	}
	if ttl <= 0 {
		return "", errors.New("ttl must be positive")
	}

	objectURL, err := defaultClient.PresignedGetObject(
		context.Background(),
		defaultBucket,
		key,
		ttl,
		url.Values{},
	)
	if err != nil {
		return "", fmt.Errorf("generate presigned url for %q: %w", key, err)
	}

	return objectURL.String(), nil
}
