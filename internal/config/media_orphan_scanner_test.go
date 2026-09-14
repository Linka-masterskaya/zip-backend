package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMediaOrphanScannerConfigLoadsFromFile(t *testing.T) {
	cfg, err := Load("../../config/config.dev.yml")
	require.NoError(t, err)

	assert.Equal(t, time.Hour, cfg.Cron.MediaOrphanScanner.Interval)
	assert.Equal(t, time.Minute, cfg.Cron.MediaOrphanScanner.GracePeriod)
	assert.Equal(t, 500, cfg.Cron.MediaOrphanScanner.BatchSize)
}

func TestValidateMediaOrphanScannerConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     MediaOrphanScannerCron
		wantErr string
	}{
		{
			name: "valid",
			cfg: MediaOrphanScannerCron{
				Interval:    time.Hour,
				GracePeriod: time.Minute,
				BatchSize:   500,
			},
		},
		{
			name: "zero interval",
			cfg: MediaOrphanScannerCron{
				Interval:    0,
				GracePeriod: time.Minute,
				BatchSize:   500,
			},
			wantErr: "cron.media_orphan_scanner.interval must be > 0",
		},
		{
			name: "zero grace period",
			cfg: MediaOrphanScannerCron{
				Interval:    time.Hour,
				GracePeriod: 0,
				BatchSize:   500,
			},
			wantErr: "cron.media_orphan_scanner.grace_period must be > 0",
		},
		{
			name: "zero batch size",
			cfg: MediaOrphanScannerCron{
				Interval:    time.Hour,
				GracePeriod: time.Minute,
				BatchSize:   0,
			},
			wantErr: "cron.media_orphan_scanner.batch_size must be > 0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateMediaOrphanScannerConfig(&tt.cfg)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, tt.wantErr)
		})
	}
}
