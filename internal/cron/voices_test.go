package cron

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Linka-masterskaya/zip-backend/internal/tts"
)

type fakeFetcher struct {
	voicesFn func(ctx context.Context) ([]tts.Voice, error)
}

func (f *fakeFetcher) Voices(ctx context.Context) ([]tts.Voice, error) {
	if f.voicesFn != nil {
		return f.voicesFn(ctx)
	}
	return nil, nil
}

type fakeVoicesStore struct {
	upsertFn func(ctx context.Context, voices []tts.Voice) error
	called   bool
}

func (f *fakeVoicesStore) UpsertVoices(ctx context.Context, voices []tts.Voice) error {
	f.called = true
	if f.upsertFn != nil {
		return f.upsertFn(ctx, voices)
	}
	return nil
}

func TestRefreshOK(t *testing.T) {
	voices := []tts.Voice{{ID: "alena", Name: "Алёна", LangCode: "ru-RU"}}

	fetcher := &fakeFetcher{
		voicesFn: func(_ context.Context) ([]tts.Voice, error) {
			return voices, nil
		},
	}
	store := &fakeVoicesStore{
		upsertFn: func(_ context.Context, v []tts.Voice) error {
			assert.Equal(t, voices, v)
			return nil
		},
	}

	r := NewVoiceRefresher(fetcher, store)
	err := r.Refresh(context.Background())

	require.NoError(t, err)
	assert.True(t, store.called)
}

func TestRefreshFetchError(t *testing.T) {
	fetcher := &fakeFetcher{
		voicesFn: func(_ context.Context) ([]tts.Voice, error) {
			return nil, fmt.Errorf("connection refused")
		},
	}
	store := &fakeVoicesStore{}

	r := NewVoiceRefresher(fetcher, store)
	err := r.Refresh(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "connection refused")
	assert.False(t, store.called)
}

func TestRefreshUpsertError(t *testing.T) {
	fetcher := &fakeFetcher{
		voicesFn: func(_ context.Context) ([]tts.Voice, error) {
			return []tts.Voice{{ID: "alena"}}, nil
		},
	}
	store := &fakeVoicesStore{
		upsertFn: func(_ context.Context, _ []tts.Voice) error {
			return fmt.Errorf("db write failed")
		},
	}

	r := NewVoiceRefresher(fetcher, store)
	err := r.Refresh(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "db write failed")
}
