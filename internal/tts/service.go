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
	CreateOrGetInflightJob(context.Context, uuid.UUID, string, string) (uuid.UUID, bool, error)
	GetFromBank(context.Context, string, string) (*BankEntry, error)
	UpdateStatusTTS(context.Context, uuid.UUID, string) error
	GetOrgID(context.Context, uuid.UUID) (uuid.UUID, error)
	GetJob(context.Context, uuid.UUID, uuid.UUID) (*JobDetails, error)
	GetVoices(context.Context) ([]Voice, error)
	UpsertVoices(context.Context, []Voice) error
	CreateMediaWithSucceededJob(context.Context, uuid.UUID, uuid.UUID, *BankEntry, MediaFileInput) (uuid.UUID, uuid.UUID, error)
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

	userID, err := authctx.UserIDFromCtx(ctx)
	if err != nil {
		return "", fmt.Errorf("tts.CreateAudio: %w", err)
	}
	orgID, err := s.repo.GetOrgID(ctx, userID)
	if err != nil {
		return "", err
	}

	entry, err := s.repo.GetFromBank(ctx, ttsData.Text, ttsData.Voice)
	if err == nil {
		jobID, _, err := s.repo.CreateMediaWithSucceededJob(ctx, orgID, userID, entry, MediaFileInput{
			MinioKey:  entry.MinioKey,
			SHA256:    entry.SHA256,
			SizeBytes: entry.SizeBytes,
			MimeType:  s.mimetype,
			Name:      TruncateName(entry.Text, 50),
		})
		if err != nil {
			return "", ttsError(err)
		}
		return jobID.String(), nil
	}
	if !errors.Is(err, apperr.ErrNotFound) {
		return "", err
	}

	if !s.isValidVoice(ctx, ttsData.Voice) {
		return "", apperr.ErrBadRequest.WithMessage("unknown voice")
	}

	jobId, isJobNew, err := s.repo.CreateOrGetInflightJob(ctx, orgID, ttsData.Text, ttsData.Voice)
	if err != nil {
		return "", err
	}
	if isJobNew {
		err := s.pub.PublishTTSJob(ctx, broker.TTSJob{
			JobId:  jobId.String(),
			OrgID:  orgID.String(),
			UserID: userID.String(),
			Text:   ttsData.Text,
			Voice:  ttsData.Voice,
		})
		if err != nil {
			s.failJob(context.WithoutCancel(ctx), jobId)
			return "", err
		}
	}

	return jobId.String(), nil
}

func (s *Service) GetJob(ctx context.Context, jobID uuid.UUID) (string, string, error) {

	userID, err := authctx.UserIDFromCtx(ctx)
	if err != nil {
		return "", "", fmt.Errorf("tts.GetJob: %w", err)
	}
	orgID, err := s.repo.GetOrgID(ctx, userID)
	if err != nil {
		return "", "", err
	}

	jobDetails, err := s.repo.GetJob(ctx, jobID, orgID)
	if err != nil {
		return "", "", err
	}

	if jobDetails.Status != StatusSucceeded {
		return jobDetails.Status, "", nil
	}

	if jobDetails.MediaID == nil {
		return "", "", fmt.Errorf("tts.GetJob: succeeded job has no media_id")
	}

	return jobDetails.Status, jobDetails.MediaID.String(), nil
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

func TruncateName(s string, maxRunes int) string {
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes]) + "…"
}
