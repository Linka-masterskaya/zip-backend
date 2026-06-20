// Package redis provides Redis client initialization helpers.
package redis

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

// Client wraps a Redis client connection.
type Client struct {
	Rdb *redis.Client
}

// NewClient creates a Redis client from URL and verifies the connection.
func NewClient(URL string) (*Client, error) {
	options, err := redis.ParseURL(URL)

	if err != nil {
		return nil, errors.Join(fmt.Errorf("failed to parse redis URL: %s", URL), err)
	}

	rdb := redis.NewClient(options)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = rdb.Ping(ctx).Err()

	if err != nil {
		return nil, errors.Join(fmt.Errorf("failed to ping redis"), err)
	}

	slog.Info("Connection to redis is established")

	return &Client{Rdb: rdb}, nil

}

// Close closes the Redis client connection.
func (c *Client) Close() error {
	if c.Rdb != nil {
		return c.Rdb.Close()
	}
	return nil
}
