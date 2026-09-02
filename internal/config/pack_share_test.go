package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidatePackShareConfig(t *testing.T) {
	valid := PackShareConfig{
		Workers:            2,
		PollInterval:       500 * time.Millisecond,
		JobTimeout:         10 * time.Minute,
		DailySendsPerUser:  10,
		DailyBytesPerUser:  100 * 1024 * 1024,
		MaxAttachmentBytes: 15 * 1024 * 1024,
		SendRetries:        3,
		SendTimeout:        30 * time.Second,
		RetryBackoff:       2 * time.Second,
		ShutdownTimeout:    3 * time.Minute,
	}

	tests := []struct {
		name    string
		mutate  func(*PackShareConfig)
		message string
	}{
		{"workers", func(cfg *PackShareConfig) { cfg.Workers = 0 }, "pack_share.workers must be > 0"},
		{"poll interval", func(cfg *PackShareConfig) { cfg.PollInterval = 0 }, "pack_share.poll_interval must be > 0"},
		{"job timeout", func(cfg *PackShareConfig) { cfg.JobTimeout = 0 }, "pack_share.job_timeout must be > 0"},
		{"daily sends", func(cfg *PackShareConfig) { cfg.DailySendsPerUser = 0 }, "pack_share.daily_sends_per_user must be > 0"},
		{"daily bytes", func(cfg *PackShareConfig) { cfg.DailyBytesPerUser = 0 }, "pack_share.daily_bytes_per_user must be > 0"},
		{"attachment bytes", func(cfg *PackShareConfig) { cfg.MaxAttachmentBytes = 0 }, "pack_share.max_attachment_bytes must be > 0"},
		{"send retries", func(cfg *PackShareConfig) { cfg.SendRetries = 0 }, "pack_share.send_retries must be > 0"},
		{"send timeout", func(cfg *PackShareConfig) { cfg.SendTimeout = 0 }, "pack_share.send_timeout must be > 0"},
		{"retry backoff", func(cfg *PackShareConfig) { cfg.RetryBackoff = 0 }, "pack_share.retry_backoff must be > 0"},
		{"shutdown timeout", func(cfg *PackShareConfig) { cfg.ShutdownTimeout = 0 }, "pack_share.shutdown_timeout must be > 0"},
	}

	assert.NoError(t, validatePackShareConfig(&valid))
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := valid
			tt.mutate(&cfg)
			assert.EqualError(t, validatePackShareConfig(&cfg), tt.message)
		})
	}
}

func TestValidatePackShareConfigRejectsJobTimeoutBelowRetryBudget(t *testing.T) {
	cfg := PackShareConfig{
		Workers:            1,
		PollInterval:       time.Second,
		JobTimeout:         2 * time.Minute,
		DailySendsPerUser:  1,
		DailyBytesPerUser:  1024,
		MaxAttachmentBytes: 512,
		SendRetries:        3,
		SendTimeout:        30 * time.Second,
		RetryBackoff:       2 * time.Second,
		ShutdownTimeout:    10 * time.Second,
	}

	err := validatePackShareConfig(&cfg)
	require.Error(t, err)
	assert.ErrorContains(t, err, "pack_share.job_timeout must be >= 2m6s")
}

func TestLoadRejectsDisabledPackShareQuotaFromEnvironment(t *testing.T) {
	t.Setenv("PACK_SHARE_DAILY_SENDS_PER_USER", "0")

	_, err := Load("../../config/config.dev.yml")
	require.Error(t, err)
	assert.ErrorContains(t, err, "pack_share.daily_sends_per_user must be > 0")
}
