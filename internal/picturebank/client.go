package picturebank

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/cache"
	"github.com/Linka-masterskaya/zip-backend/internal/config"
	"golang.org/x/sync/singleflight"
)

const (
	outboundLimitScope = "pictures_bank_outbound"
	outboundLimitKey   = "global"
	maxCacheEntries    = 128
)

var (
	ErrUnavailable      = errors.New("pictures bank unavailable")
	ErrRateLimited      = errors.New("pictures bank outbound limit reached")
	ErrResponseTooLarge = errors.New("pictures bank response is too large")
	ErrInvalidResponse  = errors.New("pictures bank returned invalid data")
)

type distributedLimiter interface {
	Allow(context.Context, cache.RateLimitRequest) (bool, int64, error)
}

type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type Client struct {
	baseURL           *url.URL
	http              httpDoer
	limiter           distributedLimiter
	timeout           time.Duration
	requestsPerSecond int64
	maxMetadataBytes  int64
	maxImageBytes     int64
	semaphore         chan struct{}
	cache             *responseCache
	requests          singleflight.Group
}

func NewClient(cfg config.PicturesBankConfig, limiter distributedLimiter) (*Client, error) {
	baseURL, err := url.Parse(cfg.URL)
	if err != nil || baseURL.Scheme != "https" || baseURL.Host == "" {
		return nil, fmt.Errorf("pictures bank URL must be an absolute HTTPS URL")
	}
	if limiter == nil {
		return nil, errors.New("pictures bank distributed limiter is required")
	}
	if cfg.Timeout <= 0 || cfg.RequestsPerSecond <= 0 || cfg.MaxConcurrent <= 0 ||
		cfg.CacheTTL <= 0 || cfg.MaxMetadataBytes <= 0 || cfg.MaxImageBytes <= 0 {
		return nil, errors.New("pictures bank limits must be positive")
	}
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: cfg.Timeout}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          cfg.MaxConcurrent * 2,
		MaxIdleConnsPerHost:   cfg.MaxConcurrent,
		MaxConnsPerHost:       cfg.MaxConcurrent,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   cfg.Timeout,
		ResponseHeaderTimeout: cfg.Timeout,
	}
	return newClient(cfg, baseURL, &http.Client{Transport: transport}, limiter), nil
}

func newClient(
	cfg config.PicturesBankConfig,
	baseURL *url.URL,
	httpClient httpDoer,
	limiter distributedLimiter,
) *Client {
	return &Client{
		baseURL: baseURL, http: httpClient, limiter: limiter,
		timeout: cfg.Timeout, requestsPerSecond: cfg.RequestsPerSecond,
		maxMetadataBytes: cfg.MaxMetadataBytes, maxImageBytes: cfg.MaxImageBytes,
		semaphore: make(chan struct{}, cfg.MaxConcurrent),
		cache:     newResponseCache(cfg.CacheTTL, maxCacheEntries),
	}
}

func (c *Client) Categories(ctx context.Context) ([]Category, error) {
	data, _, err := c.cachedGet(ctx, "categories", "/category/all", nil, c.maxMetadataBytes)
	if err != nil {
		return nil, err
	}
	var result []Category
	if err = json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("%w: decode categories", ErrInvalidResponse)
	}
	return result, nil
}

func (c *Client) Search(ctx context.Context, query string) ([]Picture, error) {
	values := url.Values{"query": []string{query}}
	key := "search:" + strings.ToLower(query)
	data, _, err := c.cachedGet(ctx, key, "/picture/search/", values, c.maxMetadataBytes)
	if err != nil {
		return nil, err
	}
	var result []Picture
	if err = json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("%w: decode search", ErrInvalidResponse)
	}
	return result, nil
}

func (c *Client) Image(ctx context.Context, pictureID string) (*Image, error) {
	key := "image:" + pictureID
	result := c.requests.DoChan(key, func() (any, error) {
		requestCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.timeout)
		defer cancel()
		data, contentType, err := c.get(
			requestCtx, "/picture/"+url.PathEscape(pictureID)+"/buffer", nil, c.maxImageBytes,
		)
		if err != nil {
			return nil, err
		}
		if !allowedImageType(contentType) {
			contentType = http.DetectContentType(data)
		}
		if !allowedImageType(contentType) {
			return nil, fmt.Errorf("%w: unexpected image content type", ErrInvalidResponse)
		}
		return &Image{Data: data, ContentType: contentType}, nil
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case value := <-result:
		if value.Err != nil {
			return nil, value.Err
		}
		image, ok := value.Val.(*Image)
		if !ok {
			return nil, ErrInvalidResponse
		}
		return image, nil
	}
}

func (c *Client) cachedGet(
	ctx context.Context,
	key, path string,
	query url.Values,
	limit int64,
) ([]byte, string, error) {
	if data, contentType, ok := c.cache.get(key); ok {
		return data, contentType, nil
	}
	result := c.requests.DoChan(key, func() (any, error) {
		if data, contentType, ok := c.cache.get(key); ok {
			return cachedResponse{data: data, contentType: contentType}, nil
		}
		requestCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.timeout)
		defer cancel()
		data, contentType, err := c.get(requestCtx, path, query, limit)
		if err != nil {
			return nil, err
		}
		c.cache.set(key, data, contentType)
		return cachedResponse{data: data, contentType: contentType}, nil
	})
	select {
	case <-ctx.Done():
		return nil, "", ctx.Err()
	case value := <-result:
		if value.Err != nil {
			return nil, "", value.Err
		}
		response, ok := value.Val.(cachedResponse)
		if !ok {
			return nil, "", ErrInvalidResponse
		}
		return response.data, response.contentType, nil
	}
}

func (c *Client) get(
	ctx context.Context,
	path string,
	query url.Values,
	limit int64,
) ([]byte, string, error) {
	select {
	case c.semaphore <- struct{}{}:
		defer func() { <-c.semaphore }()
	case <-ctx.Done():
		return nil, "", ctx.Err()
	}
	allowed, _, err := c.limiter.Allow(ctx, cache.RateLimitRequest{
		Scope: outboundLimitScope, Key: outboundLimitKey,
		Limit: c.requestsPerSecond, WindowSize: time.Second,
	})
	if err != nil {
		return nil, "", fmt.Errorf("%w: distributed limiter: %v", ErrUnavailable, err)
	}
	if !allowed {
		return nil, "", ErrRateLimited
	}
	endpoint := c.baseURL.JoinPath(path)
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, "", fmt.Errorf("pictures bank request: %w", err)
	}
	request.Header.Set("Accept", "application/json, image/*")
	request.Header.Set("User-Agent", "Linka-Editor-Backend/1.0")
	response, err := c.http.Do(request)
	if err != nil {
		return nil, "", fmt.Errorf("%w: request failed", ErrUnavailable)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		if closeErr := response.Body.Close(); closeErr != nil {
			return nil, "", fmt.Errorf("%w: close error response", ErrUnavailable)
		}
		return nil, "", fmt.Errorf("%w: status %d", ErrUnavailable, response.StatusCode)
	}
	data, readErr := io.ReadAll(io.LimitReader(response.Body, limit+1))
	closeErr := response.Body.Close()
	if readErr != nil {
		return nil, "", fmt.Errorf("%w: read response", ErrUnavailable)
	}
	if closeErr != nil {
		return nil, "", fmt.Errorf("%w: close response", ErrUnavailable)
	}
	if int64(len(data)) > limit {
		return nil, "", ErrResponseTooLarge
	}
	contentType := strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0])
	if contentType == "" {
		contentType = http.DetectContentType(data)
	}
	return data, contentType, nil
}

func allowedImageType(contentType string) bool {
	switch contentType {
	case "image/png", "image/jpeg", "image/webp", "image/gif":
		return true
	default:
		return false
	}
}

type cachedResponse struct {
	data        []byte
	contentType string
}

type responseCache struct {
	mu         sync.Mutex
	ttl        time.Duration
	maxEntries int
	items      map[string]cacheEntry
}

type cacheEntry struct {
	response  cachedResponse
	expiresAt time.Time
	createdAt time.Time
}

func newResponseCache(ttl time.Duration, maxEntries int) *responseCache {
	return &responseCache{ttl: ttl, maxEntries: maxEntries, items: make(map[string]cacheEntry)}
}

func (c *responseCache) get(key string) ([]byte, string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	item, ok := c.items[key]
	if !ok {
		return nil, "", false
	}
	if time.Now().After(item.expiresAt) {
		delete(c.items, key)
		return nil, "", false
	}
	return item.response.data, item.response.contentType, true
}

func (c *responseCache) set(key string, data []byte, contentType string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for itemKey, item := range c.items {
		if now.After(item.expiresAt) {
			delete(c.items, itemKey)
		}
	}
	if len(c.items) >= c.maxEntries {
		var oldestKey string
		var oldestTime time.Time
		for itemKey, item := range c.items {
			if oldestKey == "" || item.createdAt.Before(oldestTime) {
				oldestKey, oldestTime = itemKey, item.createdAt
			}
		}
		delete(c.items, oldestKey)
	}
	c.items[key] = cacheEntry{
		response:  cachedResponse{data: data, contentType: contentType},
		expiresAt: now.Add(c.ttl), createdAt: now,
	}
}
