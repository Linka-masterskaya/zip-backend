package tts

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/authctx"
	"github.com/Linka-masterskaya/zip-backend/internal/broker"
)

type fakeRepo struct {
	getFromBankFn         func(context.Context, string, string) (*BankEntry, error)
	createSucceededJobFn  func(context.Context, uuid.UUID, *BankEntry, uuid.UUID) (uuid.UUID, error)
	createOrGetInflightFn func(context.Context, uuid.UUID, string, string) (uuid.UUID, bool, error)
	updateStatusFn        func(context.Context, uuid.UUID, string) error
	getOrgIDFn            func(context.Context, uuid.UUID) (uuid.UUID, error)
	getJobFn              func(context.Context, uuid.UUID) (*JobDetails, error)
	createMediaFileFn     func(context.Context, uuid.UUID, uuid.UUID, MediaFileInput) (uuid.UUID, error)
}

func (f *fakeRepo) GetFromBank(ctx context.Context, text, voice string) (*BankEntry, error) {
	if f.getFromBankFn != nil {
		return f.getFromBankFn(ctx, text, voice)
	}
	return nil, apperr.ErrNotFound
}

func (f *fakeRepo) CreateSucceededJob(ctx context.Context, orgID uuid.UUID, entry *BankEntry, mediaID uuid.UUID) (uuid.UUID, error) {
	if f.createSucceededJobFn != nil {
		return f.createSucceededJobFn(ctx, orgID, entry, mediaID)
	}
	return uuid.New(), nil
}

func (f *fakeRepo) CreateOrGetInflightJob(ctx context.Context, orgID uuid.UUID, text, voice string) (uuid.UUID, bool, error) {
	if f.createOrGetInflightFn != nil {
		return f.createOrGetInflightFn(ctx, orgID, text, voice)
	}
	return uuid.New(), true, nil
}

func (f *fakeRepo) UpdateStatusTTS(ctx context.Context, id uuid.UUID, status string) error {
	if f.updateStatusFn != nil {
		return f.updateStatusFn(ctx, id, status)
	}
	return nil
}

func (f *fakeRepo) GetOrgID(ctx context.Context, userID uuid.UUID) (uuid.UUID, error) {
	if f.getOrgIDFn != nil {
		return f.getOrgIDFn(ctx, userID)
	}
	return uuid.New(), nil
}

func (f *fakeRepo) GetJob(ctx context.Context, jobID uuid.UUID) (*JobDetails, error) {
	if f.getJobFn != nil {
		return f.getJobFn(ctx, jobID)
	}
	return &JobDetails{Status: StatusPending}, nil
}

func (f *fakeRepo) CreateMediaFile(ctx context.Context, orgID, userID uuid.UUID, input MediaFileInput) (uuid.UUID, error) {
	if f.createMediaFileFn != nil {
		return f.createMediaFileFn(ctx, orgID, userID, input)
	}
	return uuid.New(), nil
}

func (f *fakeRepo) GetVoices(_ context.Context) ([]Voice, error) {
	return nil, fmt.Errorf("no cache")
}

func (f *fakeRepo) UpsertVoices(_ context.Context, _ []Voice) error {
	return nil
}

type fakePub struct {
	publishFn func(context.Context, broker.TTSJob) error
}

func (f *fakePub) PublishTTSJob(ctx context.Context, job broker.TTSJob) error {
	if f.publishFn != nil {
		return f.publishFn(ctx, job)
	}
	return nil
}

type fakeClient struct {
	voicesFn func(context.Context) ([]Voice, error)
}

func (f *fakeClient) Voices(ctx context.Context) ([]Voice, error) {
	if f.voicesFn != nil {
		return f.voicesFn(ctx)
	}
	return nil, nil
}

func testService(repo *fakeRepo, pub *fakePub, client *fakeClient) *Service {
	return NewService(repo, pub, client, ServiceConfig{
		MaxTextLen: 100,
		MimeType:   "audio/mpeg",
	})
}

func TestCreateAudioBankHit(t *testing.T) {
	expectedJobID := uuid.New()
	repo := &fakeRepo{
		getFromBankFn: func(_ context.Context, text, voice string) (*BankEntry, error) {
			return &BankEntry{Text: text, Voice: voice, MinioKey: "tts/abc"}, nil
		},
		createSucceededJobFn: func(_ context.Context, _ uuid.UUID, _ *BankEntry, _ uuid.UUID) (uuid.UUID, error) {
			return expectedJobID, nil
		},
	}
	pub := &fakePub{
		publishFn: func(_ context.Context, _ broker.TTSJob) error {
			t.Fatal("should not publish when bank hit")
			return nil
		},
	}

	svc := testService(repo, pub, &fakeClient{
		voicesFn: func(_ context.Context) ([]Voice, error) {
			return []Voice{{ID: "alena"}}, nil
		},
	})
	ctx := authctx.SetUserIDToCtx(context.Background(), uuid.New())
	jobID, err := svc.CreateAudio(ctx, TTSDataRequest{Text: "привет", Voice: "alena"})

	require.NoError(t, err)
	assert.Equal(t, expectedJobID.String(), jobID)
}

func TestCreateAudioBankMissNewJob(t *testing.T) {
	expectedJobID := uuid.New()
	var published bool
	repo := &fakeRepo{
		createOrGetInflightFn: func(_ context.Context, _ uuid.UUID, _, _ string) (uuid.UUID, bool, error) {
			return expectedJobID, true, nil
		},
	}
	pub := &fakePub{
		publishFn: func(_ context.Context, job broker.TTSJob) error {
			published = true
			assert.Equal(t, expectedJobID.String(), job.JobId)
			assert.NotEmpty(t, job.OrgID)
			assert.NotEmpty(t, job.UserID)
			assert.Equal(t, "привет", job.Text)
			assert.Equal(t, "alena", job.Voice)
			return nil
		},
	}

	svc := testService(repo, pub, &fakeClient{
		voicesFn: func(_ context.Context) ([]Voice, error) {
			return []Voice{{ID: "alena"}}, nil
		},
	})
	ctx := authctx.SetUserIDToCtx(context.Background(), uuid.New())
	jobID, err := svc.CreateAudio(ctx, TTSDataRequest{Text: "привет", Voice: "alena"})

	require.NoError(t, err)
	assert.Equal(t, expectedJobID.String(), jobID)
	assert.True(t, published, "должен опубликовать в NATS")
}

func TestCreateAudioEmptyText(t *testing.T) {
	svc := testService(&fakeRepo{}, &fakePub{}, &fakeClient{})

	_, err := svc.CreateAudio(context.Background(), TTSDataRequest{Text: "", Voice: "alena"})
	require.Error(t, err)
	assert.ErrorIs(t, err, apperr.ErrBadRequest)
}

func TestCreateAudioEmptyVoice(t *testing.T) {
	svc := testService(&fakeRepo{}, &fakePub{}, &fakeClient{})

	_, err := svc.CreateAudio(context.Background(), TTSDataRequest{Text: "привет", Voice: ""})
	require.Error(t, err)
	assert.ErrorIs(t, err, apperr.ErrBadRequest)
}

func TestCreateAudioTextTooLong(t *testing.T) {
	svc := testService(&fakeRepo{}, &fakePub{}, &fakeClient{})
	longText := string(make([]rune, 101))

	_, err := svc.CreateAudio(context.Background(), TTSDataRequest{Text: longText, Voice: "alena"})
	var appErr *apperr.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, "BAD_REQUEST", appErr.Code)
	assert.Equal(t, "text too long", appErr.Message)
}

func TestGetJobPending(t *testing.T) {
	repo := &fakeRepo{
		getJobFn: func(_ context.Context, _ uuid.UUID) (*JobDetails, error) {
			return &JobDetails{Status: StatusPending}, nil
		},
	}

	svc := testService(repo, &fakePub{}, &fakeClient{})
	status, mediaID, err := svc.GetJob(context.Background(), uuid.New())

	require.NoError(t, err)
	assert.Equal(t, StatusPending, status)
	assert.Empty(t, mediaID)
}

func TestGetJobSucceeded(t *testing.T) {
	expectedMediaID := uuid.New()

	repo := &fakeRepo{
		getJobFn: func(_ context.Context, _ uuid.UUID) (*JobDetails, error) {
			return &JobDetails{
				Status:  StatusSucceeded,
				MediaID: &expectedMediaID,
			}, nil
		},
	}

	svc := testService(repo, &fakePub{}, &fakeClient{})
	status, mediaID, err := svc.GetJob(context.Background(), uuid.New())

	require.NoError(t, err)
	assert.Equal(t, StatusSucceeded, status)
	assert.Equal(t, expectedMediaID.String(), mediaID)
}

func TestGetVoices(t *testing.T) {
	expected := []Voice{
		{ID: "alena", Name: "Алёна", LangCode: "ru-RU"},
		{ID: "john", Name: "Джон", LangCode: "en-US"},
	}
	client := &fakeClient{
		voicesFn: func(_ context.Context) ([]Voice, error) {
			return expected, nil
		},
	}

	svc := testService(&fakeRepo{}, &fakePub{}, client)
	voices, err := svc.GetVoices(context.Background())

	require.NoError(t, err)
	assert.Equal(t, expected, voices)
}
func TestCreateAudioInflightJob(t *testing.T) {
	expectedJobID := uuid.New()
	repo := &fakeRepo{
		createOrGetInflightFn: func(_ context.Context, _ uuid.UUID, _, _ string) (uuid.UUID, bool, error) {
			return expectedJobID, false, nil
		},
	}
	pub := &fakePub{
		publishFn: func(_ context.Context, _ broker.TTSJob) error {
			t.Fatal("should not publish for existing inflight job")
			return nil
		},
	}

	svc := testService(repo, pub, &fakeClient{
		voicesFn: func(_ context.Context) ([]Voice, error) {
			return []Voice{{ID: "alena"}}, nil
		},
	})
	ctx := authctx.SetUserIDToCtx(context.Background(), uuid.New())
	jobID, err := svc.CreateAudio(ctx, TTSDataRequest{Text: "привет", Voice: "alena"})

	require.NoError(t, err)
	assert.Equal(t, expectedJobID.String(), jobID)
}

func TestGetVoicesError(t *testing.T) {
	client := &fakeClient{
		voicesFn: func(_ context.Context) ([]Voice, error) {
			return nil, fmt.Errorf("connection refused")
		},
	}

	svc := testService(&fakeRepo{}, &fakePub{}, client)
	voices, err := svc.GetVoices(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "connection refused")
	assert.Nil(t, voices)
}

func TestCreateAudioBankHitCreatesMedia(t *testing.T) {
	expectedJobID := uuid.New()
	expectedMediaID := uuid.New()
	var mediaCreated bool
	repo := &fakeRepo{
		getFromBankFn: func(_ context.Context, text, voice string) (*BankEntry, error) {
			return &BankEntry{Text: text, Voice: voice, MinioKey: "tts/abc", SHA256: "d", SizeBytes: 100}, nil
		},
		createMediaFileFn: func(_ context.Context, _, _ uuid.UUID, input MediaFileInput) (uuid.UUID, error) {
			mediaCreated = true
			assert.Equal(t, "tts/abc", input.MinioKey)
			assert.Equal(t, "audio/mpeg", input.MimeType)
			return expectedMediaID, nil
		},
		createSucceededJobFn: func(_ context.Context, _ uuid.UUID, _ *BankEntry, mediaID uuid.UUID) (uuid.UUID, error) {
			assert.Equal(t, expectedMediaID, mediaID, "должен передать mediaID из CreateMediaFile")
			return expectedJobID, nil
		},
	}
	ctx := authctx.SetUserIDToCtx(context.Background(), uuid.New())
	svc := testService(repo, &fakePub{}, &fakeClient{
		voicesFn: func(_ context.Context) ([]Voice, error) {
			return []Voice{{ID: "alena"}}, nil
		},
	})
	jobID, err := svc.CreateAudio(ctx, TTSDataRequest{Text: "привет", Voice: "alena"})

	require.NoError(t, err)
	assert.Equal(t, expectedJobID.String(), jobID)
	assert.True(t, mediaCreated)
}
