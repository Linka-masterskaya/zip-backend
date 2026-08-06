package tts

import (
	"context"
	"strings"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/broker"
	"github.com/google/uuid"
)

type repository interface {
	Create(context.Context) error
	CreateOrGetInflightJob(context.Context, string, string) (uuid.UUID, bool, error)
	GetMediaIDFromBank(context.Context, string, string) (uuid.UUID, error)
}

type publisher interface {
	PublishTTSJob(context.Context, broker.TTSJob) error
}

type Service struct {
	repo   repository
	pub    *broker.Publisher
	voices map[string]struct{}
}

func NewService(repo repository, voicesList []string) *Service {

	// решение спорное, возможно,нужно вынести маппинг в конфиг?
	voices := make(map[string]struct{}, len(voicesList))
	for _, o := range voicesList {
		o := strings.TrimSpace(o)
		voices[o] = struct{}{}
	}

	return &Service{
		repo:   repo,
		voices: voices,
	}
}

func (s *Service) CreateAudio(ctx context.Context, ttsData TTSData) (string, error) {
	if ttsData.Text == "" || ttsData.Voice == "" {
		return "", apperr.ErrBadRequest
	}
	if _, ok := s.voices[ttsData.Voice]; !ok {
		return "", apperr.ErrBadRequest.WithMessage("tts.CreateAudio: current voice not supported")
	}

	// если такая пара текст + голос уже есть - сразу возвращаем
	ttsId, err := s.repo.GetMediaIDFromBank(ctx, ttsData.Text, ttsData.Voice)
	if err != nil {
		return "", err
	}
	if ttsId != uuid.Nil {
		return ttsId.String(), nil
	}

	// если нет - начинаем генерировать
	jobId, isJobNew, err := s.repo.CreateOrGetInflightJob(ctx, ttsData.Text, ttsData.Voice)
	if err != nil {
		return "", err
	}

	//пишем в тиблицу джобов

	s.pub.PublishTTSJob(ctx, broker.TTSJob{
		JobId: jobId.String(),
		Text:  ttsData.Text,
		Voice: ttsData.Voice,
	})

	// а вот сохранение, все же, возможно лучше для адина. нужно согласовать
	// когда они наборы создают в библиотеку - сохраняем аудио
	return jobId.String(), nil
}
