package health

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type pingerFunc func(context.Context) error

func (f pingerFunc) Ping(ctx context.Context) error {
	return f(ctx)
}

type pingerStub struct{}

func (*pingerStub) Ping(context.Context) error {
	return nil
}

type connectionCheckerFunc func() bool

func (f connectionCheckerFunc) IsConnected() bool {
	return f()
}

type listerFunc func(context.Context) ([]minio.BucketInfo, error)

func (f listerFunc) ListBuckets(ctx context.Context) ([]minio.BucketInfo, error) {
	return f(ctx)
}

func TestNewCheckerRejectsNilDependencies(t *testing.T) {
	validPinger := pingerFunc(func(context.Context) error { return nil })
	validNATS := connectionCheckerFunc(func() bool { return true })
	validMinIO := listerFunc(func(context.Context) ([]minio.BucketInfo, error) { return nil, nil })

	tests := []struct {
		name        string
		db          Pinger
		redisClient Pinger
		natsConn    ConnectionChecker
		minioClient Lister
		wantErr     string
	}{
		{
			name: "postgres is nil", redisClient: validPinger, natsConn: validNATS, minioClient: validMinIO,
			wantErr: "postgres client not initialized",
		},
		{
			name: "redis is nil", db: validPinger, natsConn: validNATS, minioClient: validMinIO,
			wantErr: "redis client not initialized",
		},
		{
			name: "nats is nil", db: validPinger, redisClient: validPinger, minioClient: validMinIO,
			wantErr: "nats client not initialized",
		},
		{
			name: "minio is nil", db: validPinger, redisClient: validPinger, natsConn: validNATS,
			wantErr: "minio client not initialized",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checker, err := NewChecker(tt.db, tt.redisClient, tt.natsConn, tt.minioClient)

			require.Error(t, err)
			assert.EqualError(t, err, tt.wantErr)
			assert.Nil(t, checker)
		})
	}
}

func TestNewCheckerRejectsTypedNilDependency(t *testing.T) {
	var db *pingerStub
	validPinger := pingerFunc(func(context.Context) error { return nil })
	validNATS := connectionCheckerFunc(func() bool { return true })
	validMinIO := listerFunc(func(context.Context) ([]minio.BucketInfo, error) { return nil, nil })

	checker, err := NewChecker(db, validPinger, validNATS, validMinIO)

	require.Error(t, err)
	assert.EqualError(t, err, "postgres client not initialized")
	assert.Nil(t, checker)
}

func TestCheckerRunAllDependenciesReady(t *testing.T) {
	checker := newTestChecker(nil, nil, nil)

	status, body := checker.Run(context.Background())
	result := body.(response)

	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, StatusOK, result.Status)
	require.Len(t, result.Checks, 4)
	for _, check := range result.Checks {
		assert.Equal(t, StatusOK, check.Status)
		assert.Empty(t, check.Error)
	}
}

func TestCheckerRunDependencyError(t *testing.T) {
	checker := newTestChecker(errors.New("connection refused"), nil, nil)

	status, body := checker.Run(context.Background())
	result := body.(response)

	assert.Equal(t, http.StatusServiceUnavailable, status)
	assert.Equal(t, StatusDegraded, result.Status)
	assert.Equal(t, StatusError, result.Checks["redis"].Status)
	assert.Equal(t, "connection refused", result.Checks["redis"].Error)
	assert.Equal(t, StatusOK, result.Checks["postgres"].Status)
	assert.Equal(t, StatusOK, result.Checks["nats"].Status)
	assert.Equal(t, StatusOK, result.Checks["minio"].Status)
}

func TestCheckerRunRecoversCheckPanic(t *testing.T) {
	checker := newTestChecker(nil, nil, func(context.Context) ([]minio.BucketInfo, error) {
		panic("minio exploded")
	})

	status, body := checker.Run(context.Background())
	result := body.(response)

	assert.Equal(t, http.StatusServiceUnavailable, status)
	assert.Equal(t, StatusDegraded, result.Status)
	assert.Equal(t, StatusError, result.Checks["minio"].Status)
	assert.True(t, strings.Contains(result.Checks["minio"].Error, "panic: minio exploded"))
}

func TestCheckerRunTimesOutChecksInParallel(t *testing.T) {
	waitForContext := func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}
	checker := &Checker{
		db:          pingerFunc(waitForContext),
		redisClient: pingerFunc(waitForContext),
		natsConn:    connectionCheckerFunc(func() bool { return true }),
		minioClient: listerFunc(func(ctx context.Context) ([]minio.BucketInfo, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}),
	}

	startedAt := time.Now()
	status, body := checker.Run(context.Background())
	elapsed := time.Since(startedAt)
	result := body.(response)

	assert.Equal(t, http.StatusServiceUnavailable, status)
	assert.Equal(t, StatusDegraded, result.Status)
	assert.Less(t, elapsed, 3*time.Second)
	assert.GreaterOrEqual(t, elapsed, checkTimeout)
	assert.Equal(t, context.DeadlineExceeded.Error(), result.Checks["postgres"].Error)
	assert.Equal(t, context.DeadlineExceeded.Error(), result.Checks["redis"].Error)
	assert.Equal(t, context.DeadlineExceeded.Error(), result.Checks["minio"].Error)
}

func newTestChecker(
	redisErr error,
	postgresErr error,
	minioCheck listerFunc,
) *Checker {
	if minioCheck == nil {
		minioCheck = func(context.Context) ([]minio.BucketInfo, error) {
			return nil, nil
		}
	}

	return &Checker{
		db:          pingerFunc(func(context.Context) error { return postgresErr }),
		redisClient: pingerFunc(func(context.Context) error { return redisErr }),
		natsConn:    connectionCheckerFunc(func() bool { return true }),
		minioClient: minioCheck,
	}
}
