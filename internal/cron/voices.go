package cron

import (
	"context"
	"log/slog"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/tts"
)

type voicesFetcher interface {
	Voices(context.Context) ([]tts.Voice, error)
}

type voicesStore interface {
	UpsertVoices(context.Context, []tts.Voice) error
}

type VoiceRefresher struct {
	client voicesFetcher
	repo   voicesStore
}

func NewVoiceRefresher(client voicesFetcher, repo voicesStore) *VoiceRefresher {
	return &VoiceRefresher{client: client, repo: repo}
}

func (v *VoiceRefresher) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := v.Refresh(ctx); err != nil {
				slog.ErrorContext(ctx, "cron.RefreshVoices: update error", "err", err)
			}
		case <-ctx.Done():
			return
		}
	}
}

func (v *VoiceRefresher) Refresh(ctx context.Context) error {
	voices, err := v.client.Voices(ctx)
	if err != nil {
		return err
	}

	err = v.repo.UpsertVoices(ctx, voices)
	if err != nil {
		return err
	}

	return nil
}
