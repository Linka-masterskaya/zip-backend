package cron

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeCleanupRepo struct {
	getOldAudioFn    func(ctx context.Context, ttl time.Duration, limit int) ([]string, error)
	deleteFromBankFn func(ctx context.Context, keys []string) error
	deleteOldJobsFn  func(ctx context.Context, cutoff time.Time) error
	deleteBankCalled bool
	deleteJobsCalled bool
}

func (f *fakeCleanupRepo) GetOldAudio(ctx context.Context, ttl time.Duration, limit int) ([]string, error) {
	if f.getOldAudioFn != nil {
		return f.getOldAudioFn(ctx, ttl, limit)
	}
	return nil, nil
}

func (f *fakeCleanupRepo) DeleteFromBank(ctx context.Context, keys []string) error {
	f.deleteBankCalled = true
	if f.deleteFromBankFn != nil {
		return f.deleteFromBankFn(ctx, keys)
	}
	return nil
}

func (f *fakeCleanupRepo) DeleteOldJobs(ctx context.Context, cutoff time.Time) error {
	f.deleteJobsCalled = true
	if f.deleteOldJobsFn != nil {
		return f.deleteOldJobsFn(ctx, cutoff)
	}
	return nil
}

type fakeStorageReaper struct {
	reapFn       func(ctx context.Context, limit int) (int, error)
	called       bool
	gotGrace     time.Duration
	gotLimit     int
	removedCount int
}

func (f *fakeStorageReaper) ReapUnreferenced(ctx context.Context, grace time.Duration, limit int) (int, error) {
	f.called = true
	f.gotGrace = grace
	f.gotLimit = limit
	if f.reapFn != nil {
		return f.reapFn(ctx, limit)
	}
	return f.removedCount, nil
}

func TestCleanupOK(t *testing.T) {
	keys := []string{"tts/aaa", "tts/bbb"}

	repo := &fakeCleanupRepo{
		getOldAudioFn: func(_ context.Context, _ time.Duration, _ int) ([]string, error) {
			return keys, nil
		},
		deleteFromBankFn: func(_ context.Context, k []string) error {
			assert.Equal(t, keys, k)
			return nil
		},
	}
	reaper := &fakeStorageReaper{removedCount: 2}

	c := NewTTSCleaner(repo, reaper, 24*time.Hour, 72*time.Hour, 5*time.Minute, 100)
	err := c.Cleanup(context.Background())

	require.NoError(t, err)
	assert.True(t, repo.deleteJobsCalled)
	assert.True(t, repo.deleteBankCalled)
	assert.True(t, reaper.called)
	assert.Equal(t, 5*time.Minute, reaper.gotGrace)
	assert.Equal(t, 100, reaper.gotLimit)
}

func TestCleanupDeletesOldJobs(t *testing.T) {
	var gotCutoff time.Time
	repo := &fakeCleanupRepo{
		deleteOldJobsFn: func(_ context.Context, cutoff time.Time) error {
			gotCutoff = cutoff
			return nil
		},
	}
	reaper := &fakeStorageReaper{}

	jobsTTL := 72 * time.Hour
	c := NewTTSCleaner(repo, reaper, 24*time.Hour, jobsTTL, 5*time.Minute, 100)

	before := time.Now().Add(-jobsTTL)
	err := c.Cleanup(context.Background())
	after := time.Now().Add(-jobsTTL)

	require.NoError(t, err)
	assert.True(t, gotCutoff.After(before) || gotCutoff.Equal(before))
	assert.True(t, gotCutoff.Before(after) || gotCutoff.Equal(after))
}

func TestCleanupGetOldAudioError(t *testing.T) {
	repo := &fakeCleanupRepo{
		getOldAudioFn: func(_ context.Context, _ time.Duration, _ int) ([]string, error) {
			return nil, fmt.Errorf("db timeout")
		},
	}
	reaper := &fakeStorageReaper{}

	c := NewTTSCleaner(repo, reaper, 24*time.Hour, 72*time.Hour, 5*time.Minute, 100)
	err := c.Cleanup(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "db timeout")
	assert.False(t, reaper.called)
}

func TestCleanupReaperPartialFailureIsNonFatal(t *testing.T) {
	keys := []string{"tts/aaa", "tts/bbb", "tts/ccc"}

	repo := &fakeCleanupRepo{
		getOldAudioFn: func(_ context.Context, _ time.Duration, _ int) ([]string, error) {
			return keys, nil
		},
		deleteFromBankFn: func(_ context.Context, k []string) error {
			assert.Equal(t, keys, k)
			return nil
		},
	}
	reaper := &fakeStorageReaper{
		reapFn: func(_ context.Context, _ int) (int, error) {
			return 2, fmt.Errorf("minio unavailable")
		},
	}

	c := NewTTSCleaner(repo, reaper, 24*time.Hour, 72*time.Hour, 5*time.Minute, 100)
	err := c.Cleanup(context.Background())

	require.NoError(t, err)
	assert.True(t, repo.deleteBankCalled)
	assert.True(t, reaper.called)
}

func TestCleanupEmptyBankStillRunsStorageReaper(t *testing.T) {
	repo := &fakeCleanupRepo{
		getOldAudioFn: func(_ context.Context, _ time.Duration, _ int) ([]string, error) {
			return nil, nil
		},
	}
	reaper := &fakeStorageReaper{}

	c := NewTTSCleaner(repo, reaper, 24*time.Hour, 72*time.Hour, 5*time.Minute, 100)
	err := c.Cleanup(context.Background())

	require.NoError(t, err)
	assert.False(t, repo.deleteBankCalled)
	assert.True(t, reaper.called)
}

func TestCleanupDeleteJobsErrorContinues(t *testing.T) {
	keys := []string{"tts/aaa"}

	repo := &fakeCleanupRepo{
		deleteOldJobsFn: func(_ context.Context, _ time.Time) error {
			return fmt.Errorf("jobs table locked")
		},
		getOldAudioFn: func(_ context.Context, _ time.Duration, _ int) ([]string, error) {
			return keys, nil
		},
	}
	reaper := &fakeStorageReaper{}

	c := NewTTSCleaner(repo, reaper, 24*time.Hour, 72*time.Hour, 5*time.Minute, 100)
	err := c.Cleanup(context.Background())

	require.NoError(t, err)
	assert.True(t, repo.deleteBankCalled)
	assert.True(t, reaper.called)
}
