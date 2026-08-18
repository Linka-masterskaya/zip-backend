package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/broker"
	"github.com/Linka-masterskaya/zip-backend/internal/tts"
	"github.com/Linka-masterskaya/zip-backend/internal/ttsapi"
	"github.com/google/uuid"
)

type synthesizer interface {
	Synthesize(context.Context, string, string) ([]byte, error)
}

type uploader interface {
	PutObject(context.Context, string, io.Reader, int64, string) error
}

type audioBank interface {
	CompleteJob(context.Context, uuid.UUID, string, string, int64) error
	PutToBank(context.Context, *tts.BankEntry) error
	UpdateStatusTTS(ctx context.Context, jobID uuid.UUID, status string) error
}

type TTS struct {
	client  synthesizer
	storage uploader
	repo    audioBank
}

func NewTTS(ttsapi synthesizer, storage uploader, repo audioBank) *TTS {
	return &TTS{client: ttsapi, storage: storage, repo: repo}
}

func (w *TTS) Handle(ctx context.Context, job broker.TTSJob, isLastAttempt bool) error {
	jobID, err := uuid.Parse(job.JobId)
	if err != nil {
		slog.ErrorContext(ctx, "worker.Handle: bad job id in message", "job_id", job.JobId, "err", err)
		return nil
	}

	audio, err := w.client.Synthesize(ctx, job.Text, job.Voice)
	if err != nil {
		if errors.Is(err, ttsapi.ErrBadRequest) || errors.Is(err, ttsapi.ErrTooLarge) {
			if failErr := w.markFailedWithRetry(ctx, jobID); failErr != nil {
				slog.ErrorContext(ctx, "worker.Handle: mark failed exhausted retries, job stuck pending",
					"job_id", jobID, "synth_err", err, "db_err", failErr)
			}
			return nil
		}
		return w.handleRetryable(ctx, jobID, "Synthesize", isLastAttempt, err)
	}

	hash := sha256.Sum256(audio)
	digest := hex.EncodeToString(hash[:])
	audioSize := int64(len(audio))

	data, err := json.Marshal([2]string{job.Voice, job.Text})
	if err != nil {
		return fmt.Errorf("worker.Handle: marshal key: %w", err)
	}
	keyHash := sha256.Sum256(data)
	key := "tts/" + hex.EncodeToString(keyHash[:])
	err = w.storage.PutObject(ctx, key, bytes.NewReader(audio), audioSize, "audio/mpeg")
	if err != nil {
		return w.handleRetryable(ctx, jobID, "PutObject", isLastAttempt, err)
	}

	err = w.repo.CompleteJob(ctx, jobID, key, digest, audioSize)
	if err != nil {
		return w.handleRetryable(ctx, jobID, "CompleteJob", isLastAttempt, err)
	}

	err = w.repo.PutToBank(ctx, &tts.BankEntry{
		Text:      job.Text,
		Voice:     job.Voice,
		MinioKey:  key,
		SHA256:    digest,
		SizeBytes: audioSize,
	})
	if err != nil {
		return w.handleRetryable(ctx, jobID, "PutToBank", isLastAttempt, err)
	}

	return nil
}

func (w *TTS) markFailedWithRetry(ctx context.Context, jobID uuid.UUID) error {
	backoffs := []time.Duration{100 * time.Millisecond, 500 * time.Millisecond, 2 * time.Second}
	ctx = context.WithoutCancel(ctx)
	var lastErr error
	for i, delay := range backoffs {
		if i > 0 {
			time.Sleep(delay)
		}
		lastErr = w.repo.UpdateStatusTTS(ctx, jobID, tts.StatusFailed)
		if lastErr == nil {
			return nil
		}
	}
	return lastErr
}

func (w *TTS) handleRetryable(ctx context.Context, jobID uuid.UUID, opName string, isLastAttempt bool, opErr error) error {
	if !isLastAttempt {
		return fmt.Errorf("worker.Handle: %s: %w", opName, opErr)
	}
	if failErr := w.markFailedWithRetry(ctx, jobID); failErr != nil {
		slog.ErrorContext(ctx, "worker.Handle: last attempt, mark failed failed",
			"job_id", jobID, "op", opName, "op_err", opErr, "db_err", failErr)
		return nil
	}
	slog.WarnContext(ctx, "worker.Handle: job marked failed after final delivery",
		"job_id", jobID, "op", opName, "op_err", opErr)
	return nil
}
