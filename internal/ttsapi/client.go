package ttsapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/tts"
)

var (
	ErrBadRequest  = errors.New("ttsapi: bad request")
	ErrTooLarge    = errors.New("ttsapi: audio too large")
	ErrUnavailable = errors.New("ttsapi: service unavailable")
)

type Client struct {
	httpClient *http.Client
	apiURL     string
	semaphore  chan struct{}
}

type ttsRequest struct {
	Text  string `json:"text"`
	Voice string `json:"voice"`
}

func NewClient(apiurl string, timeout time.Duration, maxConcurrent int) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: timeout},
		apiURL:     apiurl,
		semaphore:  make(chan struct{}, maxConcurrent),
	}
}

func (t *Client) Synthesize(ctx context.Context, text, voice string) ([]byte, error) {
	data, err := json.Marshal(ttsRequest{Text: text, Voice: voice})
	if err != nil {
		return nil, fmt.Errorf("ttsapi.Synthesize: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		t.apiURL+"/tts", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("ttsapi.Synthesize request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	body, status, err := t.do(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("ttsapi.Synthesize: %w", err)
	}

	switch status {
	case http.StatusOK:
	case http.StatusBadRequest:
		return nil, ErrBadRequest
	case http.StatusRequestEntityTooLarge:
		return nil, ErrTooLarge
	case http.StatusBadGateway:
		return nil, ErrUnavailable
	default:
		return nil, fmt.Errorf("ttsapi.Synthesize: unexpected status %d", status)
	}

	if len(body) == 0 {
		return nil, fmt.Errorf("ttsapi.Synthesize: empty audio")
	}

	return body, nil
}

type ttsVoice struct {
	LangCode string `json:"lang_code"`
	ID       string `json:"id"`
	Name     string `json:"name"`
}

func (t *Client) Voices(ctx context.Context) ([]tts.Voice, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.apiURL+"/voices", nil)
	if err != nil {
		return nil, fmt.Errorf("ttsapi.Voices: %w", err)
	}

	body, status, err := t.do(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("ttsapi.Voices: %w", err)
	}

	if status != http.StatusOK {
		return nil, fmt.Errorf("ttsapi.Voices: unexpected status %d", status)
	}

	var voicesFromAPI []ttsVoice
	err = json.Unmarshal(body, &voicesFromAPI)
	if err != nil {
		return nil, fmt.Errorf("ttsapi.Voices: %w", err)
	}
	if len(voicesFromAPI) == 0 {
		return nil, fmt.Errorf("ttsapi.Voices: empty voice list")
	}

	voicesToService := make([]tts.Voice, len(voicesFromAPI))
	for i, v := range voicesFromAPI {
		voicesToService[i].ID = v.ID
		voicesToService[i].Name = v.Name
		voicesToService[i].LangCode = v.LangCode
	}

	return voicesToService, nil
}

func (t *Client) do(ctx context.Context, req *http.Request) ([]byte, int, error) {
	select {
	case t.semaphore <- struct{}{}:
		defer func() { <-t.semaphore }()
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	}

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			slog.WarnContext(ctx, "ttsapi: close response body", "err", closeErr)
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, err
	}

	return body, resp.StatusCode, nil
}
