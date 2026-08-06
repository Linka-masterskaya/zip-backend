package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"

	"github.com/Linka-masterskaya/zip-backend/internal/broker"
	"github.com/Linka-masterskaya/zip-backend/internal/tts"
	"github.com/google/uuid"
)

type synthesizer interface {
	Synthesize(ctx context.Context, text, voice string) ([]byte, error)
}

type uploader interface {
	PutObject(ctx context.Context, key string, reader io.Reader, size int64, contentType string) error
}

type audioBank interface {
	CompleteJob(ctx context.Context, jobID uuid.UUID, key, digest string, size int64) error
	PutToBank(ctx context.Context, entry *tts.BankEntry) error
}

type TTS struct {
	client  synthesizer
	storage uploader
	repo    audioBank
}

func NewTTS(ttsapi synthesizer, storage uploader, repo audioBank) *TTS {
	return &TTS{client: ttsapi, storage: storage, repo: repo}
}

func (w *TTS) Handle(ctx context.Context, job broker.TTSJob) error {
	audio, err := w.client.Synthesize(ctx, job.Text, job.Voice)
	if err != nil {
		return fmt.Errorf("worker.GenerateAudio: %w", err)
	}

	hash := sha256.Sum256(audio)
	digest := hex.EncodeToString(hash[:])
	audioSize := int64(len(audio))

	keyHash := sha256.Sum256([]byte(job.Text + job.Voice))
	key := "tts/" + hex.EncodeToString(keyHash[:])
	err = w.storage.PutObject(ctx, key, bytes.NewReader(audio), audioSize, "audio/mpeg")
	if err != nil {
		return fmt.Errorf("worker.GenerateAudio: PutObject: %w", err)
	}

	jobID, err := uuid.Parse(job.JobId)
	if err != nil {
		return fmt.Errorf("worker.GenerateAudio: %w", err)
	}
	err = w.repo.CompleteJob(ctx, jobID, key, digest, audioSize)
	if err != nil {
		return fmt.Errorf("worker.GenerateAudio: %w", err)
	}

	err = w.repo.PutToBank(ctx, &tts.BankEntry{
		Text:      job.Text,
		Voice:     job.Voice,
		MinioKey:  key,
		SHA256:    digest,
		SizeBytes: audioSize,
	})
	if err != nil {
		return fmt.Errorf("worker.GenerateAudio: %w", err)
	}

	return nil
}
