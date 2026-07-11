// Package storage provides object storage clients and helpers.
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/Linka-masterskaya/zip-backend/internal/config"
)

const defaultMinIOTimeout = 15 * time.Second

// ErrObjectNotFound is returned when requested object does not exist.
var ErrObjectNotFound = errors.New("object not found")

// Client provides access to MinIO object storage operations.
type Client struct {
	client *minio.Client
	bucket string
}

// New creates a MinIO client, ensures the configured bucket exists, and keeps it private.
func New(cfg config.MinIOConfig) (*Client, error) {
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

	timeout := defaultMinIOTimeout
	if cfg.Timeout != "" {
		timeout, err = time.ParseDuration(cfg.Timeout)
		if err != nil {
			return nil, fmt.Errorf("parse minio timeout: %w", err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	if err := ensureBucket(ctx, client, cfg.Bucket); err != nil {
		return nil, err
	}

	return &Client{
		client: client,
		bucket: cfg.Bucket,
	}, nil
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
func (c *Client) PresignedURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	if c == nil || c.client == nil {
		return "", errors.New("minio client is not initialized")
	}
	if key == "" {
		return "", errors.New("object key is required")
	}
	if ttl <= 0 {
		return "", errors.New("ttl must be positive")
	}

	objectURL, err := c.client.PresignedGetObject(
		ctx,
		c.bucket,
		key,
		ttl,
		url.Values{},
	)
	if err != nil {
		return "", fmt.Errorf("generate presigned url for %q: %w", key, err)
	}

	return objectURL.String(), nil
}

// PutObject uploads an object to the configured bucket.
func (c *Client) PutObject(ctx context.Context, key string, reader io.Reader, size int64, contentType string) error {
	if c == nil || c.client == nil {
		return errors.New("minio client is not initialized")
	}
	if key == "" {
		return errors.New("object key is required")
	}
	if reader == nil {
		return errors.New("object reader is required")
	}
	if size < 0 {
		return errors.New("object size must be non-negative")
	}

	_, err := c.client.PutObject(ctx, c.bucket, key, reader, size, minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return fmt.Errorf("put object %q: %w", key, err)
	}
	return nil
}

// RemoveObject deletes an object from the configured bucket.
func (c *Client) RemoveObject(ctx context.Context, key string) error {
	if c == nil || c.client == nil {
		return errors.New("minio client is not initialized")
	}
	if key == "" {
		return errors.New("object key is required")
	}

	err := c.client.RemoveObject(ctx, c.bucket, key, minio.RemoveObjectOptions{})
	if err != nil {
		return fmt.Errorf("remove object %q: %w", key, err)
	}
	return nil
}

// ObjectSize returns object size in bytes.
func (c *Client) ObjectSize(ctx context.Context, key string) (int64, error) {
	if c == nil || c.client == nil {
		return 0, errors.New("minio client is not initialized")
	}
	if key == "" {
		return 0, errors.New("object key is required")
	}

	info, err := c.client.StatObject(ctx, c.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		if isNotFound(err) {
			return 0, ErrObjectNotFound
		}
		return 0, fmt.Errorf("stat object %q: %w", key, err)
	}
	return info.Size, nil
}

func isNotFound(err error) bool {
	errResp := minio.ToErrorResponse(err)
	return errResp.Code == "NoSuchKey" || errResp.Code == "NotFound" || errResp.StatusCode == 404
}
