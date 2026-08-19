package tts

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/authctx"
	"github.com/Linka-masterskaya/zip-backend/internal/broker"
	"github.com/google/uuid"
)

type repository interface {
	CreateSucceededJob(context.Context, *BankEntry) (uuid.UUID, error)
	CreateOrGetInflightJob(context.Context, string, string) (uuid.UUID, bool, error)
	GetFromBank(context.Context, string, string) (*BankEntry, error)
	UpdateStatusTTS(context.Context, uuid.UUID, string) error
	GetOrgID(context.Context, uuid.UUID) (uuid.UUID, error)
	GetJob(context.Context, uuid.UUID) (*JobDetails, error)
	CreateMediaFile(context.Context, uuid.UUID, uuid.UUID, *JobDetails) (uuid.UUID, error)
	GetVoices(context.Context) ([]Voice, error)
	UpsertVoices(context.Context, []Voice) error
}

type publisher interface {
	PublishTTSJob(context.Context, broker.TTSJob) error
}

type ttsClient interface {
	Voices(context.Context) ([]Voice, error)
}

type Service struct {
	repo       repository
	pub        publisher
	ttsClient  ttsClient
	mimetype   string
	maxTextLen int
}

func NewService(repo repository, pub publisher, ttsClient ttsClient, cfg ServiceConfig) *Service {
	return &Service{
		repo:       repo,
		pub:        pub,
		ttsClient:  ttsClient,
		mimetype:   cfg.MimeType,
		maxTextLen: cfg.MaxTextLen,
	}
}

func (s *Service) CreateAudio(ctx context.Context, ttsData TTSDataRequest) (string, error) {
	ttsData.Text = normalize(ttsData.Text)
	ttsData.Voice = normalize(ttsData.Voice)
	if ttsData.Text == "" || ttsData.Voice == "" {
		return "", apperr.ErrBadRequest
	}
	if len([]rune(ttsData.Text)) > s.maxTextLen {
		return "", apperr.ErrBadRequest.WithMessage("text too long")
	}

	entry, err := s.repo.GetFromBank(ctx, ttsData.Text, ttsData.Voice)
	if err == nil {
		jobID, err := s.repo.CreateSucceededJob(ctx, entry)
		if err != nil {
			return "", err
		}
		return jobID.String(), nil
	}
	if !errors.Is(err, apperr.ErrNotFound) {
		return "", err
	}

	if !s.isValidVoice(ctx, ttsData.Voice) {
		return "", apperr.ErrBadRequest.WithMessage("unknown voice")
	}

	jobId, isJobNew, err := s.repo.CreateOrGetInflightJob(ctx, ttsData.Text, ttsData.Voice)
	if err != nil {
		return "", err
	}
	if isJobNew {
		err := s.pub.PublishTTSJob(ctx, broker.TTSJob{
			JobId: jobId.String(),
			Text:  ttsData.Text,
			Voice: ttsData.Voice,
		})
		if err != nil {
			s.failJob(context.WithoutCancel(ctx), jobId)
			return "", err
		}
	}

	return jobId.String(), nil
}

func (s *Service) GetJob(ctx context.Context, jobID uuid.UUID) (string, string, error) {
	jobDetails, err := s.repo.GetJob(ctx, jobID)
	if err != nil {
		return "", "", err
	}

	if jobDetails.Status != StatusSucceeded {
		return jobDetails.Status, "", nil
	}

	userID, err := authctx.UserIDFromCtx(ctx)
	if err != nil {
		return "", "", fmt.Errorf("tts.GetJob: %w", err)
	}

	orgID, err := s.repo.GetOrgID(ctx, userID)
	if err != nil {
		return "", "", fmt.Errorf("tts.GetJob: %w", err)
	}

	if jobDetails.MinioKey == nil || jobDetails.SHA256 == nil || jobDetails.SizeBytes == nil {
		return "", "", fmt.Errorf("tts.GetJob: succeeded job has missing fields")
	}

	jobDetails.MimeType = &s.mimetype
	mediaID, err := s.repo.CreateMediaFile(ctx, orgID, userID, jobDetails)
	if err != nil {
		return "", "", ttsError(err)
	}

	return jobDetails.Status, mediaID.String(), nil
}

func (s *Service) GetVoices(ctx context.Context) ([]Voice, error) {
	voices, err := s.repo.GetVoices(ctx)
	if err == nil {
		return voices, nil
	}
	slog.WarnContext(ctx, "tts.GetVoices: cache miss", "err", err)

	voices, err = s.ttsClient.Voices(ctx)
	if err != nil {
		return nil, err
	}

	if err := s.repo.UpsertVoices(ctx, voices); err != nil {
		slog.ErrorContext(ctx, "tts.GetVoices: cache write failed", "err", err)
	}

	return voices, nil
}

func (s *Service) failJob(ctx context.Context, jobID uuid.UUID) {
	if err := s.repo.UpdateStatusTTS(ctx, jobID, StatusFailed); err != nil {
		slog.ErrorContext(ctx, "failed to mark tts job", "job_id", jobID, "err", err)
	}
}

func ttsError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrQuotaExceeded) {
		return apperr.ErrPayloadTooLarge.WithMessage("organization storage quota exceeded")
	}
	return err
}

func normalize(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func (s *Service) isValidVoice(ctx context.Context, voice string) bool {
	voices, err := s.GetVoices(ctx)
	if err != nil {
		slog.WarnContext(ctx, "tts.isValidVoice: skipping validation, voices unavailable", "err", err, "voice", voice)
		return true
	}
	for _, v := range voices {
		if v.ID == voice {
			return true
		}
	}
	return false
}
