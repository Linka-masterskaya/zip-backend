package picturebank

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/cache"
	"github.com/Linka-masterskaya/zip-backend/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

func TestClientCachesAndCoalescesIdenticalRequests(t *testing.T) {
	var upstreamCalls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamCalls.Add(1)
		time.Sleep(25 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"category","name":"Category"}]`))
	}))
	t.Cleanup(upstream.Close)
	limiter := &fakeDistributedLimiter{allowed: true}
	client := testClient(t, upstream, limiter, testPicturesConfig())

	var group errgroup.Group
	start := make(chan struct{})
	for range 20 {
		group.Go(func() error {
			<-start
			categories, err := client.Categories(t.Context())
			if err != nil {
				return err
			}
			if len(categories) != 1 {
				return errors.New("unexpected category count")
			}
			return nil
		})
	}
	close(start)
	require.NoError(t, group.Wait())

	_, err := client.Categories(t.Context())
	require.NoError(t, err)
	assert.Equal(t, int64(1), upstreamCalls.Load())
	assert.Equal(t, 1, limiter.callCount())
	limitRequest := limiter.lastRequest()
	assert.Equal(t, outboundLimitScope, limitRequest.Scope)
	assert.Equal(t, outboundLimitKey, limitRequest.Key)
	assert.Equal(t, int64(5), limitRequest.Limit)
	assert.Equal(t, time.Second, limitRequest.WindowSize)
}

func TestClientFailsClosedWhenOutboundBudgetIsUnavailable(t *testing.T) {
	tests := []struct {
		name      string
		limiter   *fakeDistributedLimiter
		targetErr error
	}{
		{
			name: "budget exhausted",
			limiter: &fakeDistributedLimiter{
				allowed: false,
			},
			targetErr: ErrRateLimited,
		},
		{
			name: "redis error",
			limiter: &fakeDistributedLimiter{
				allowed: true, err: errors.New("redis unavailable"),
			},
			targetErr: ErrUnavailable,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var upstreamCalls atomic.Int64
			upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				upstreamCalls.Add(1)
			}))
			t.Cleanup(upstream.Close)
			client := testClient(t, upstream, test.limiter, testPicturesConfig())

			_, err := client.Categories(t.Context())

			require.ErrorIs(t, err, test.targetErr)
			assert.Zero(t, upstreamCalls.Load(), "upstream must not be called after limiter rejection")
		})
	}
}

func TestClientLimitsResponseSizeAndImageType(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/category/all" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"larger":"than limit"}`))
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>not an image</html>"))
	}))
	t.Cleanup(upstream.Close)
	cfg := testPicturesConfig()
	cfg.MaxMetadataBytes = 8
	client := testClient(t, upstream, &fakeDistributedLimiter{allowed: true}, cfg)

	_, err := client.Categories(t.Context())
	require.ErrorIs(t, err, ErrResponseTooLarge)
	_, err = client.Image(t.Context(), "c39d9b34-5339-4295-a56e-8996af77beb7")
	require.ErrorIs(t, err, ErrInvalidResponse)
}

func TestClientCapsConcurrentUpstreamRequests(t *testing.T) {
	var active atomic.Int64
	var maximum atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		current := active.Add(1)
		for {
			seen := maximum.Load()
			if current <= seen || maximum.CompareAndSwap(seen, current) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		active.Add(-1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(upstream.Close)
	cfg := testPicturesConfig()
	cfg.MaxConcurrent = 2
	client := testClient(t, upstream, &fakeDistributedLimiter{allowed: true}, cfg)

	var group errgroup.Group
	for index := range 12 {
		query := string(rune('a' + index))
		group.Go(func() error {
			_, err := client.Search(t.Context(), query)
			return err
		})
	}
	require.NoError(t, group.Wait())
	assert.LessOrEqual(t, maximum.Load(), int64(2))
}

func TestClientDoesNotRetryUpstreamFailures(t *testing.T) {
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(upstream.Close)
	client := testClient(
		t, upstream, &fakeDistributedLimiter{allowed: true}, testPicturesConfig(),
	)

	_, err := client.Categories(t.Context())

	require.ErrorIs(t, err, ErrUnavailable)
	assert.Equal(t, int64(1), calls.Load())
}

func TestClientSniffsImagesWithGenericUpstreamMIME(t *testing.T) {
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(testPNG())
	}))
	t.Cleanup(upstream.Close)
	client := testClient(
		t, upstream, &fakeDistributedLimiter{allowed: true}, testPicturesConfig(),
	)

	image, err := client.Image(t.Context(), "c39d9b34-5339-4295-a56e-8996af77beb7")

	require.NoError(t, err)
	assert.Equal(t, "image/png", image.ContentType)
	assert.Equal(t, testPNG(), image.Data)

	second, err := client.Image(t.Context(), "c39d9b34-5339-4295-a56e-8996af77beb7")
	require.NoError(t, err)
	assert.Equal(t, image.Data, second.Data)
	assert.EqualValues(t, 1, calls.Load(), "image bytes must be served from TTL cache")
}

func TestClientDistinguishesRemovedPicture(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusGone} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
			}))
			t.Cleanup(upstream.Close)
			client := testClient(
				t, upstream, &fakeDistributedLimiter{allowed: true}, testPicturesConfig(),
			)

			_, err := client.Image(t.Context(), "c39d9b34-5339-4295-a56e-8996af77beb7")

			require.ErrorIs(t, err, ErrPictureNotFound)
		})
	}
}

func testClient(
	t *testing.T,
	server *httptest.Server,
	limiter distributedLimiter,
	cfg config.PicturesBankConfig,
) *Client {
	t.Helper()
	baseURL, err := url.Parse(server.URL)
	require.NoError(t, err)
	return newClient(cfg, baseURL, server.Client(), limiter)
}

func testPicturesConfig() config.PicturesBankConfig {
	return config.PicturesBankConfig{
		Timeout: 2 * time.Second, RequestsPerSecond: 5,
		MaxConcurrent: 4, CacheTTL: time.Minute,
		MaxMetadataBytes: 2 * 1024 * 1024, MaxImageBytes: 10 * 1024 * 1024,
	}
}

type fakeDistributedLimiter struct {
	mu      sync.Mutex
	allowed bool
	err     error
	calls   int
	last    cache.RateLimitRequest
}

func (l *fakeDistributedLimiter) Allow(
	_ context.Context,
	request cache.RateLimitRequest,
) (bool, int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls++
	l.last = request
	return l.allowed, 1, l.err
}

func (l *fakeDistributedLimiter) lastRequest() cache.RateLimitRequest {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.last
}

func testPNG() []byte {
	return []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
	}
}

func (l *fakeDistributedLimiter) callCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.calls
}
