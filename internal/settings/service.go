package settings

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/authctx"
	"github.com/Linka-masterskaya/zip-backend/internal/tts"
	"github.com/google/uuid"
)

type repository interface {
	Get(context.Context, uuid.UUID) (json.RawMessage, error)
	Put(context.Context, uuid.UUID, json.RawMessage) error
	ListTemplates(context.Context, uuid.UUID) ([]Template, error)
	CreateTemplate(context.Context, uuid.UUID, string, json.RawMessage) (*Template, error)
	DeleteTemplate(context.Context, uuid.UUID, uuid.UUID) error
}

type voiceCatalog interface {
	GetVoices(context.Context) ([]tts.Voice, error)
}

type Service struct {
	repo   repository
	voices voiceCatalog
}

func NewService(repo repository, voices voiceCatalog) *Service {
	return &Service{repo: repo, voices: voices}
}

func (s *Service) Get(ctx context.Context) (json.RawMessage, error) {
	userID, err := authctx.UserIDFromCtx(ctx)
	if err != nil {
		return nil, err
	}
	return s.repo.Get(ctx, userID)
}

func (s *Service) Put(ctx context.Context, body json.RawMessage) (json.RawMessage, error) {
	userID, err := authctx.UserIDFromCtx(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.validate(ctx, body); err != nil {
		return nil, err
	}
	if err := s.repo.Put(ctx, userID, body); err != nil {
		return nil, err
	}
	return body, nil
}

func (s *Service) ListTemplates(ctx context.Context) ([]Template, error) {
	userID, err := authctx.UserIDFromCtx(ctx)
	if err != nil {
		return nil, err
	}
	return s.repo.ListTemplates(ctx, userID)
}

func (s *Service) CreateTemplate(ctx context.Context, name string, body json.RawMessage) (*Template, error) {
	userID, err := authctx.UserIDFromCtx(ctx)
	if err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, apperr.ErrBadRequest.WithMessage("template name is required")
	}
	if len([]rune(name)) > MaxTemplateName {
		return nil, apperr.ErrBadRequest.WithMessage("template name too long")
	}
	if err := s.validate(ctx, body); err != nil {
		return nil, err
	}
	return s.repo.CreateTemplate(ctx, userID, name, body)
}

func (s *Service) DeleteTemplate(ctx context.Context, templateID uuid.UUID) error {
	userID, err := authctx.UserIDFromCtx(ctx)
	if err != nil {
		return err
	}
	return s.repo.DeleteTemplate(ctx, userID, templateID)
}

func (s *Service) validate(ctx context.Context, body json.RawMessage) error {
	if len(body) > MaxDocumentSize {
		return apperr.ErrPayloadTooLarge.WithMessage("settings document too large")
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return apperr.ErrBadRequest.WithMessage("settings must be a JSON object")
	}

	var object map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	if err := dec.Decode(&object); err != nil {
		return apperr.ErrBadRequest.WithMessage("invalid settings JSON")
	}
	if object == nil {
		return apperr.ErrBadRequest.WithMessage("settings must be a JSON object")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return apperr.ErrBadRequest.WithMessage("invalid settings JSON")
	}

	for key := range object {
		if _, ok := allowedTopLevelKeys[key]; !ok {
			return apperr.ErrBadRequest.WithMessage(fmt.Sprintf("unsupported settings key: %s", key))
		}
	}

	if rawVoice, ok := object[keyVoice]; ok {
		var voice string
		if err := json.Unmarshal(rawVoice, &voice); err != nil || strings.TrimSpace(voice) == "" {
			return apperr.ErrBadRequest.WithMessage("voice must be a non-empty string")
		}
		if err := s.validateVoice(ctx, voice); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) validateVoice(ctx context.Context, voice string) error {
	if s.voices == nil {
		return nil
	}
	voices, err := s.voices.GetVoices(ctx)
	if err != nil {
		// The acceptance criterion is conditional: validate only while a voice
		// list is available. TTS outages must not make settings unwritable.
		return nil
	}
	for _, candidate := range voices {
		if candidate.ID == voice {
			return nil
		}
	}
	return apperr.ErrBadRequest.WithMessage("unknown voice")
}
