package settings

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strings"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/authctx"
	"github.com/Linka-masterskaya/zip-backend/internal/tts"
	"github.com/google/uuid"
)

type repository interface {
	Get(context.Context, uuid.UUID) (json.RawMessage, error)
	Put(context.Context, uuid.UUID, json.RawMessage) (json.RawMessage, error)
	ListTemplates(context.Context, uuid.UUID) ([]Template, error)
	CreateTemplate(context.Context, uuid.UUID, string, json.RawMessage) (*Template, error)
	DeleteTemplate(context.Context, uuid.UUID, uuid.UUID) error
}

type voiceCatalog interface {
	GetVoices(context.Context) ([]tts.Voice, error)
}

var hexColorPattern = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

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
	return s.repo.Put(ctx, userID, body)
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
	object, err := decodeSettingsObject(body)
	if err != nil {
		return err
	}

	if err := validateAllowedKeys(object); err != nil {
		return err
	}

	if rawColors, ok := object[keyColors]; ok {
		if err := validateColors(rawColors); err != nil {
			return err
		}
	}

	if rawBorderWidth, ok := object[keyBorderWidth]; ok {
		if err := validateBorderWidth(rawBorderWidth); err != nil {
			return err
		}
	}

	return s.validateVoiceSetting(ctx, object)
}

func decodeSettingsObject(body json.RawMessage) (map[string]json.RawMessage, error) {
	if len(body) > MaxDocumentSize {
		return nil, apperr.ErrPayloadTooLarge.WithMessage("settings document too large")
	}

	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, apperr.ErrBadRequest.WithMessage("settings must be a JSON object")
	}

	var object map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	if err := dec.Decode(&object); err != nil {
		return nil, apperr.ErrBadRequest.WithMessage("invalid settings JSON")
	}
	if object == nil {
		return nil, apperr.ErrBadRequest.WithMessage("settings must be a JSON object")
	}

	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, apperr.ErrBadRequest.WithMessage("invalid settings JSON")
	}

	return object, nil
}

func validateAllowedKeys(object map[string]json.RawMessage) error {
	for key := range object {
		if _, ok := allowedTopLevelKeys[key]; !ok {
			return apperr.ErrBadRequest.WithMessage(fmt.Sprintf("unsupported settings key: %s", key))
		}
	}

	return nil
}

func (s *Service) validateVoiceSetting(ctx context.Context, object map[string]json.RawMessage) error {
	rawVoice, ok := object[keyVoice]
	if !ok {
		return nil
	}

	var voice string
	if err := json.Unmarshal(rawVoice, &voice); err != nil || strings.TrimSpace(voice) == "" {
		return apperr.ErrBadRequest.WithMessage("voice must be a non-empty string")
	}

	return s.validateVoice(ctx, voice)
}

func (s *Service) validateVoice(ctx context.Context, voice string) error {
	if s.voices == nil {
		return nil
	}
	voices, err := s.voices.GetVoices(ctx)
	if err != nil {
		// The acceptance criterion is conditional: validate only while a voice
		// list is available. TTS outages must not make settings unwritable, but
		// the degraded validation path must be observable.
		slog.WarnContext(ctx, "settings voice validation skipped: catalog unavailable", "err", err)
		return nil
	}
	for _, candidate := range voices {
		if candidate.ID == voice {
			return nil
		}
	}
	return apperr.ErrBadRequest.WithMessage("unknown voice")
}

func validateColors(raw json.RawMessage) error {
	var colors map[string]string
	if err := json.Unmarshal(raw, &colors); err != nil || colors == nil {
		return apperr.ErrBadRequest.WithMessage("colors must be an object of #RRGGBB strings")
	}
	for name, value := range colors {
		if !hexColorPattern.MatchString(value) {
			return apperr.ErrBadRequest.WithMessage(fmt.Sprintf("invalid color %s: expected #RRGGBB", name))
		}
	}
	return nil
}

func validateBorderWidth(raw json.RawMessage) error {
	var width *int
	if err := json.Unmarshal(raw, &width); err != nil || width == nil {
		return apperr.ErrBadRequest.WithMessage("border_width must be an integer")
	}
	if *width < 0 || *width > MaxBorderWidth {
		return apperr.ErrBadRequest.WithMessage(fmt.Sprintf("border_width must be between 0 and %d", MaxBorderWidth))
	}
	return nil
}
