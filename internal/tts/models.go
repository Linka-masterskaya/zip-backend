package tts

import (
	"errors"
)

var ErrQuotaExceeded = errors.New("storage quota exceeded")

const (
	StatusPending    = "pending"
	StatusInProgress = "in_progress"
	StatusSucceeded  = "succeeded"
	StatusFailed     = "failed"
)

type TTSDataRequest struct {
	Text  string `json:"text"`
	Voice string `json:"voice"`
}

type TTSResponse struct {
	JobID string `json:"job_id"`
}

type BankEntry struct {
	Text      string
	Voice     string
	MinioKey  string
	SHA256    string
	SizeBytes int64
}

type TTSJobResponse struct {
	Status  string  `json:"status"`
	MediaID *string `json:"media_id,omitempty"`
}

type JobDetails struct {
	Status    string
	MinioKey  *string
	SHA256    *string
	SizeBytes *int64
	MimeType  *string
}

type ServiceConfig struct {
	MaxTextLen int
	MimeType   string
}

type Voice struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	LangCode string `json:"lang_code"`
}

type VoiceResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type VoicesResponse struct {
	Voices []VoiceResponse `json:"voices"`
}
