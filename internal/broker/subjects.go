// Package broker provides NATS JetStream messaging for asynchronous job
// processing (TTS generation, ClamAV file scanning, LLM card editing).
package broker

import "context"

// Subjects used for asynchronous job messaging on the AI_JOBS stream.
const (
	SubjectLLMRequest     = "ai.llm.req"
	SubjectLLMResponseFmt = "ai.llm.resp.%s"
	SubjectLLMResponseAll = "ai.llm.resp.*"
	SubjectTTSJobs        = "ai.tts.jobs"
	SubjectTTSDoneFmt     = "ai.tts.done.%s"
	SubjectTTSDoneAll     = "ai.tts.done.*"
	SubjectClamAVJobs     = "ai.clamav.jobs"
)

type (
	// TTSJobHandler processes a single text-to-speech job.
	TTSJobHandler func(ctx context.Context, job TTSJob) error

	// ClamAVJobHandler processes a single ClamAV file scan job.
	ClamAVJobHandler func(ctx context.Context, job ClamAVJob) error
)

// LLMRequest is a request to generate or edit cards via LLM.
type LLMRequest struct {
	RequestID string `json:"request_id"`
	Prompt    string `json:"prompt"`
	Count     int    `json:"count"`
}

// LLMChunk is a streamed JSON-patch chunk from an LLM response.
type LLMChunk struct {
	RequestID string `json:"request_id"`
	JSONPatch string `json:"json_patch"`
	Done      bool   `json:"done"`
}

// TTSJob is a text-to-speech synthesis job.
type TTSJob struct {
	PackID string `json:"pack_id"`
	CardID string `json:"card_id"`
	Text   string `json:"text"`
	Voice  string `json:"voice"`
}

// TTSResult is the result of a TTS job. Currently unused: the worker writes
// the result directly to the database instead of publishing a response.
type TTSResult struct {
	PackID   string `json:"pack_id"`
	CardID   string `json:"card_id"`
	AudioURL string `json:"audio_url"`
	Error    string `json:"error,omitempty"`
}

// ClamAVJob is a request to scan a file for malware.
type ClamAVJob struct {
	FileID   string `json:"file_id"`
	FilePath string `json:"file_path"`
}

// ClamAVResult is the result of a ClamAV scan. Currently unused: the worker
// writes the result directly to the database instead of publishing a response.
type ClamAVResult struct {
	FileID string `json:"file_id"`
	Clean  bool   `json:"clean"`
	Error  string `json:"error,omitempty"`
}
