// Package storage provides object storage clients and helpers.
package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/Linka-masterskaya/zip-backend/internal/config"
)

const defaultMinIOTimeout = 15 * time.Second

// ErrObjectNotFound is returned when requested object does not exist.
var ErrObjectNotFound = errors.New("object not found")

// Client provides access to MinIO object storage operations.
type Client struct {
	client   *minio.Client
	bucket   string
	registry *pgxpool.Pool
}

// GetObject opens an object for streaming. The caller must close the returned reader.
func (c *Client) GetObject(ctx context.Context, key string) (io.ReadCloser, error) {
	if c == nil || c.client == nil {
		return nil, errors.New("minio client is not initialized")
	}
	if key == "" {
		return nil, errors.New("object key is required")
	}

	object, err := c.client.GetObject(ctx, c.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("get object %q: %w", key, err)
	}
	if _, err = object.Stat(); err != nil {
		if closeErr := object.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close object after stat: %w", closeErr))
		}
		if isNotFound(err) {
			return nil, ErrObjectNotFound
		}
		return nil, fmt.Errorf("stat opened object %q: %w", key, err)
	}
	return object, nil
}

// New creates a MinIO client, ensures the configured bucket exists, and keeps it private.
// Production callers pass PostgreSQL as the storage-object registry. The
// optional form without a registry is kept for isolated MinIO tests that do
// not exercise object lifecycle bookkeeping.
func New(cfg config.MinIOConfig, registries ...*pgxpool.Pool) (*Client, error) {
	if len(registries) > 1 {
		return nil, errors.New("only one storage object registry may be configured")
	}
	var registry *pgxpool.Pool
	if len(registries) == 1 {
		if registries[0] == nil {
			return nil, errors.New("storage object registry database is required")
		}
		registry = registries[0]
	}
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
		client:   client,
		bucket:   cfg.Bucket,
		registry: registry,
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

// Ping checks that the configured media bucket is reachable. Checking the
// specific bucket also works behind a bucket-scoped reverse-proxy route.
func (c *Client) Ping(ctx context.Context) error {
	if c == nil || c.client == nil {
		return errors.New("minio client is not initialized")
	}

	exists, err := c.client.BucketExists(ctx, c.bucket)
	if err != nil {
		return fmt.Errorf("check minio bucket %q: %w", c.bucket, err)
	}
	if !exists {
		return fmt.Errorf("minio bucket %q does not exist", c.bucket)
	}
	return nil
}

func validatePutObjectArgs(c *Client, key string, reader io.Reader, size int64, contentType string) error {
	switch {
	case c == nil || c.client == nil:
		return errors.New("minio client is not initialized")
	case key == "":
		return errors.New("object key is required")
	case reader == nil:
		return errors.New("object reader is required")
	case size < 0:
		return errors.New("object size must be non-negative")
	case contentType == "":
		return errors.New("object content type is required")
	default:
		return nil
	}
}

// PutObject uploads an object to the configured bucket.
func (c *Client) PutObject(ctx context.Context, key string, reader io.Reader, size int64, contentType string) error {
	if err := validatePutObjectArgs(c, key, reader, size, contentType); err != nil {
		return err
	}

	registryConn, releaseLock, err := acquireObjectLock(ctx, c.registry, key)
	if err != nil {
		return err
	}
	defer releaseLock()

	// Remember whether this key was already a registered shared object. A failed
	// metadata refresh must not compensate by deleting a pre-existing blob (TTS
	// keys are deterministic and can legitimately be shared by many media rows).
	preexisting := false
	if registryConn != nil {
		if err = registryConn.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM storage_objects WHERE key = $1)`, key,
		).Scan(&preexisting); err != nil {
			return fmt.Errorf("check existing storage object %q: %w", key, err)
		}
	}

	hash := sha256.New()
	hashedReader := io.TeeReader(reader, hash)
	_, err = c.client.PutObject(ctx, c.bucket, key, hashedReader, size, minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return fmt.Errorf("put object %q: %w", key, err)
	}

	digest := hex.EncodeToString(hash.Sum(nil))
	var registryExec registryExecutor
	if registryConn != nil {
		registryExec = registryConn
	} else if c.registry != nil {
		registryExec = c.registry
	}
	if err = registerObject(ctx, registryExec, key, size, contentType, digest); err == nil {
		return nil
	}

	// The MinIO write succeeded but the registry write did not. Retry the
	// metadata write with a detached bounded context first: cancellation of the
	// request must not be allowed to create an invisible MinIO object.
	cleanupCtx, cancel := context.WithTimeout(context.Background(), defaultMinIOTimeout)
	defer cancel()
	if retryErr := registerObject(cleanupCtx, registryExec, key, size, contentType, digest); retryErr == nil {
		return nil
	} else if preexisting {
		// Preserve an already-registered shared key. Deleting it would break every
		// domain row that still references it. The caller receives the registry
		// failure and can retry the idempotent PutObject later.
		return errors.Join(err, fmt.Errorf("retry register existing storage object %q: %w", key, retryErr))
	}

	// This was a new key. Remove the successfully uploaded blob so every failed
	// PutObject converges to "no blob, no registry row" instead of creating an
	// object the registry-driven reaper can never discover.
	cleanupErr := c.client.RemoveObject(cleanupCtx, c.bucket, key, minio.RemoveObjectOptions{})
	if cleanupErr != nil && !isNotFound(cleanupErr) {
		return errors.Join(err, fmt.Errorf("compensate uploaded object %q: %w", key, cleanupErr))
	}

	// Exec errors can be ambiguous if the connection broke after PostgreSQL
	// accepted the statement. Delete any possible row while the key lock is held.
	if cleanupRegistryErr := unregisterObject(cleanupCtx, registryExec, key); cleanupRegistryErr != nil {
		return errors.Join(err, cleanupRegistryErr)
	}
	return err
}

// RemoveObject deletes an object from the configured bucket.
func (c *Client) RemoveObject(ctx context.Context, key string) error {
	if c == nil || c.client == nil {
		return errors.New("minio client is not initialized")
	}
	if key == "" {
		return errors.New("object key is required")
	}

	registryConn, releaseLock, err := acquireObjectLock(ctx, c.registry, key)
	if err != nil {
		return err
	}
	defer releaseLock()

	blobDeleted, err := c.removeObjectLocked(ctx, key, registryConn)
	if blobDeleted {
		// A registry-only failure after a successful MinIO delete must not make a
		// domain caller roll back quota/metadata as though the blob still existed.
		// The stale row remains discoverable and is cleaned by a later reaper run.
		if err != nil {
			slog.WarnContext(ctx, "storage registry cleanup deferred", "key", key, "err", err)
		}
		return nil
	}
	return err
}

// removeObjectLocked removes a blob while the caller owns the per-key advisory
// lock. blobDeleted is true once MinIO accepted (or had already applied) the
// deletion; in that state a remaining error is registry-only.
func (c *Client) removeObjectLocked(ctx context.Context, key string, registryConn *pgxpool.Conn) (blobDeleted bool, err error) {
	err = c.client.RemoveObject(ctx, c.bucket, key, minio.RemoveObjectOptions{})
	if err != nil && !isNotFound(err) {
		return false, fmt.Errorf("remove object %q: %w", key, err)
	}

	var registryExec registryExecutor
	if registryConn != nil {
		registryExec = registryConn
	} else if c.registry != nil {
		registryExec = c.registry
	}
	if err = unregisterObject(ctx, registryExec, key); err == nil {
		return true, nil
	}
	firstRegistryErr := err

	// The blob is already gone. Retry registry cleanup without inheriting request
	// cancellation. If it still fails, keep the row for the idempotent reaper.
	cleanupCtx, cancel := context.WithTimeout(context.Background(), objectLockTimeout)
	defer cancel()
	if retryErr := unregisterObject(cleanupCtx, registryExec, key); retryErr != nil {
		return true, errors.Join(firstRegistryErr, retryErr)
	}
	return true, nil
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
