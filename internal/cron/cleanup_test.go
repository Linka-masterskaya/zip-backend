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
	cleanupOldJobsFn  func(ctx context.Context, cutoff time.Time) error
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

func (f *fakeCleanupRepo) CleanupOldJobs(ctx context.Context, cutoff time.Time) error {
	f.deleteJobsCalled = true
	if f.cleanupOldJobsFn != nil {
		return f.cleanupOldJobsFn(ctx, cutoff)
	}
	return nil
}

type fakeStorage struct {
	removeObjectFn func(ctx context.Context, key string) error
	removedKeys    []string
}

func (f *fakeStorage) RemoveObject(ctx context.Context, key string) error {
	if f.removeObjectFn != nil {
		return f.removeObjectFn(ctx, key)
	}
	f.removedKeys = append(f.removedKeys, key)
	return nil
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
	stor := &fakeStorage{}

	c := NewTTSCleaner(repo, stor, 24*time.Hour, 72*time.Hour, 100)
	err := c.Cleanup(context.Background())

	require.NoError(t, err)
	assert.True(t, repo.deleteJobsCalled)
	assert.True(t, repo.deleteBankCalled)
	assert.Equal(t, keys, stor.removedKeys)
}

func TestCleanupDeletesOldJobs(t *testing.T) {
	var gotCutoff time.Time
	repo := &fakeCleanupRepo{
		cleanupOldJobsFn: func(_ context.Context, cutoff time.Time) error {
			gotCutoff = cutoff
			return nil
		},
	}
	stor := &fakeStorage{}

	jobsTTL := 72 * time.Hour
	c := NewTTSCleaner(repo, stor, 24*time.Hour, jobsTTL, 100)

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
	stor := &fakeStorage{}

	c := NewTTSCleaner(repo, stor, 24*time.Hour, 72*time.Hour, 100)
	err := c.Cleanup(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "db timeout")
}

func TestCleanupMinioPartialFailure(t *testing.T) {
	keys := []string{"tts/aaa", "tts/bbb", "tts/ccc"}

	repo := &fakeCleanupRepo{
		getOldAudioFn: func(_ context.Context, _ time.Duration, _ int) ([]string, error) {
			return keys, nil
		},
		deleteFromBankFn: func(_ context.Context, k []string) error {
			assert.Equal(t, []string{"tts/aaa", "tts/ccc"}, k)
			return nil
		},
	}
	stor := &fakeStorage{
		removeObjectFn: func(_ context.Context, key string) error {
			if key == "tts/bbb" {
				return fmt.Errorf("minio unavailable")
			}
			return nil
		},
	}

	c := NewTTSCleaner(repo, stor, 24*time.Hour, 72*time.Hour, 100)
	err := c.Cleanup(context.Background())

	require.NoError(t, err)
	assert.True(t, repo.deleteBankCalled)
}

func TestCleanupEmptyKeys(t *testing.T) {
	repo := &fakeCleanupRepo{
		getOldAudioFn: func(_ context.Context, _ time.Duration, _ int) ([]string, error) {
			return nil, nil
		},
	}
	stor := &fakeStorage{}

	c := NewTTSCleaner(repo, stor, 24*time.Hour, 72*time.Hour, 100)
	err := c.Cleanup(context.Background())

	require.NoError(t, err)
	assert.False(t, repo.deleteBankCalled)
}

func TestCleanupDeleteJobsErrorContinues(t *testing.T) {
	keys := []string{"tts/aaa"}

	repo := &fakeCleanupRepo{
		cleanupOldJobsFn: func(_ context.Context, _ time.Time) error {
			return fmt.Errorf("jobs table locked")
		},
		getOldAudioFn: func(_ context.Context, _ time.Duration, _ int) ([]string, error) {
			return keys, nil
		},
	}
	stor := &fakeStorage{}

	c := NewTTSCleaner(repo, stor, 24*time.Hour, 72*time.Hour, 100)
	err := c.Cleanup(context.Background())

	require.NoError(t, err)
	assert.True(t, repo.deleteBankCalled)
	assert.Equal(t, keys, stor.removedKeys)
}
