// Package broker provides NATS JetStream messaging for asynchronous job
// processing (TTS generation, ClamAV file scanning, LLM card editing).
package broker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/nats-io/nats.go/jetstream"
)

// Publisher publishes asynchronous job requests (LLM, TTS, ClamAV) to the AI_JOBS stream.
type Publisher struct {
	js jetstream.JetStream
}

// NewPublisher creates a Publisher.
func NewPublisher(js jetstream.JetStream) *Publisher {
	return &Publisher{js: js}
}

// PublishLLMRequest publishes a request to generate or edit cards via LLM.
func (p *Publisher) PublishLLMRequest(ctx context.Context, req LLMRequest) error {
	payload, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("PublishLLMRequest: marshal: %w", err)
	}

	ack, err := p.js.Publish(ctx, SubjectLLMRequest, payload)
	if err != nil {
		return fmt.Errorf("PublishLLMRequest: publish: %w", err)
	}

	if ack.Duplicate {
		slog.Warn("duplicate LLM request detected", "request_id", req.RequestID)
	}

	return nil
}

// PublishTTSJob publishes a text-to-speech synthesis job.
func (p *Publisher) PublishTTSJob(ctx context.Context, job TTSJob) error {
	payload, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("PublishTTSJob: marshal: %w", err)
	}

	ack, err := p.js.Publish(ctx, SubjectTTSJobs, payload)
	if err != nil {
		return fmt.Errorf("PublishTTSJob: publish: %w", err)
	}

	if ack.Duplicate {
		slog.Warn("duplicate TTS job detected", "pack_id", job.PackID, "card_id", job.CardID)
	}

	return nil
}

// PublishClamAVJob publishes a ClamAV file scan job.
func (p *Publisher) PublishClamAVJob(ctx context.Context, job ClamAVJob) error {
	payload, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("PublishClamAVJob: marshal: %w", err)
	}

	ack, err := p.js.Publish(ctx, SubjectClamAVJobs, payload)
	if err != nil {
		return fmt.Errorf("PublishClamAVJob: publish: %w", err)
	}

	if ack.Duplicate {
		slog.Warn("duplicate ClamAV job detected", "file_id", job.FileID)
	}

	return nil
}
