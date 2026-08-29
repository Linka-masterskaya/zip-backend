package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Linka-masterskaya/zip-backend/internal/broker"
	"github.com/Linka-masterskaya/zip-backend/internal/tts"
)

type fakeSynthesizer struct {
	synthesizeFn func(ctx context.Context, text, voice string) ([]byte, error)
}

func (f *fakeSynthesizer) Synthesize(ctx context.Context, text, voice string) ([]byte, error) {
	return f.synthesizeFn(ctx, text, voice)
}

type fakeUploader struct {
	putObjectFn func(ctx context.Context, key string, reader io.Reader, size int64, contentType string) error
	called      bool
}

func (f *fakeUploader) PutObject(ctx context.Context, key string, reader io.Reader, size int64, contentType string) error {
	f.called = true
	if f.putObjectFn != nil {
		return f.putObjectFn(ctx, key, reader, size, contentType)
	}
	return nil
}

type fakeAudioBank struct {
	createMediaAndCompleteJobFn func(ctx context.Context, jobID, orgID, userID uuid.UUID, input tts.MediaFileInput) (uuid.UUID, error)
	putToBankFn                 func(ctx context.Context, entry *tts.BankEntry) error
	updateStatusFn              func(context.Context, uuid.UUID, string) error
	mediaJobCalled              bool
	bankCalled                  bool
}

func (f *fakeAudioBank) PutToBank(ctx context.Context, entry *tts.BankEntry) error {
	f.bankCalled = true
	if f.putToBankFn != nil {
		return f.putToBankFn(ctx, entry)
	}
	return nil
}

func (f *fakeAudioBank) UpdateStatusTTS(ctx context.Context, jobID uuid.UUID, status string) error {
	if f.updateStatusFn != nil {
		return f.updateStatusFn(ctx, jobID, status)
	}
	return nil
}

func (f *fakeAudioBank) CreateMediaAndCompleteJob(ctx context.Context, jobID, orgID, userID uuid.UUID, input tts.MediaFileInput) (uuid.UUID, error) {
	f.mediaJobCalled = true
	if f.createMediaAndCompleteJobFn != nil {
		return f.createMediaAndCompleteJobFn(ctx, jobID, orgID, userID, input)
	}
	return uuid.New(), nil
}

func testJob() broker.TTSJob {
	return broker.TTSJob{
		JobId:  uuid.New().String(),
		OrgID:  uuid.New().String(),
		UserID: uuid.New().String(),
		Text:   "привет",
		Voice:  "alena",
	}
}

func TestHandleOK(t *testing.T) {
	audio := []byte("fake-mp3")
	job := testJob()

	synth := &fakeSynthesizer{
		synthesizeFn: func(_ context.Context, text, voice string) ([]byte, error) {
			assert.Equal(t, job.Text, text)
			assert.Equal(t, job.Voice, voice)
			return audio, nil
		},
	}
	stor := &fakeUploader{}
	repo := &fakeAudioBank{}

	w := NewTTS(synth, stor, repo, "audio/mpeg")
	err := w.Handle(context.Background(), job, false)

	require.NoError(t, err)
	assert.True(t, stor.called)
	assert.True(t, repo.mediaJobCalled)
	assert.True(t, repo.bankCalled)
}

func TestHandleOKVerifiesKeyAndDigest(t *testing.T) {
	audio := []byte("fake-mp3")
	job := testJob()

	expectedHash := sha256.Sum256(audio)
	expectedDigest := hex.EncodeToString(expectedHash[:])

	data, _ := json.Marshal([2]string{job.Voice, job.Text})
	keyHash := sha256.Sum256(data)
	expectedKey := "tts/" + hex.EncodeToString(keyHash[:])

	synth := &fakeSynthesizer{
		synthesizeFn: func(_ context.Context, _, _ string) ([]byte, error) {
			return audio, nil
		},
	}
	stor := &fakeUploader{
		putObjectFn: func(_ context.Context, key string, _ io.Reader, size int64, ct string) error {
			assert.Equal(t, expectedKey, key)
			assert.Equal(t, int64(len(audio)), size)
			assert.Equal(t, "audio/mpeg", ct)
			return nil
		},
	}
	repo := &fakeAudioBank{
		createMediaAndCompleteJobFn: func(_ context.Context, _, _, _ uuid.UUID, input tts.MediaFileInput) (uuid.UUID, error) {
			assert.Equal(t, expectedKey, input.MinioKey)
			assert.Equal(t, expectedDigest, input.SHA256)
			assert.Equal(t, int64(len(audio)), input.SizeBytes)
			assert.Equal(t, "audio/mpeg", input.MimeType)
			assert.Equal(t, "привет", input.Name)
			return uuid.New(), nil
		},
		putToBankFn: func(_ context.Context, entry *tts.BankEntry) error {
			assert.Equal(t, job.Text, entry.Text)
			assert.Equal(t, job.Voice, entry.Voice)
			assert.Equal(t, expectedKey, entry.MinioKey)
			assert.Equal(t, expectedDigest, entry.SHA256)
			return nil
		},
	}

	w := NewTTS(synth, stor, repo, "audio/mpeg")
	err := w.Handle(context.Background(), job, false)
	require.NoError(t, err)
}

func TestHandleSynthesizeError(t *testing.T) {
	synth := &fakeSynthesizer{
		synthesizeFn: func(_ context.Context, _, _ string) ([]byte, error) {
			return nil, fmt.Errorf("connection refused")
		},
	}
	stor := &fakeUploader{}
	repo := &fakeAudioBank{}

	w := NewTTS(synth, stor, repo, "audio/mpeg")
	err := w.Handle(context.Background(), testJob(), false)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "connection refused")
	assert.False(t, stor.called)
	assert.False(t, repo.mediaJobCalled)
}

func TestHandlePutObjectError(t *testing.T) {
	synth := &fakeSynthesizer{
		synthesizeFn: func(_ context.Context, _, _ string) ([]byte, error) {
			return []byte("audio"), nil
		},
	}
	stor := &fakeUploader{
		putObjectFn: func(_ context.Context, _ string, _ io.Reader, _ int64, _ string) error {
			return fmt.Errorf("storage unavailable")
		},
	}
	repo := &fakeAudioBank{}

	w := NewTTS(synth, stor, repo, "audio/mpeg")
	err := w.Handle(context.Background(), testJob(), false)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "PutObject")
	assert.False(t, repo.mediaJobCalled)
}

func TestHandleCompleteJobError(t *testing.T) {
	synth := &fakeSynthesizer{
		synthesizeFn: func(_ context.Context, _, _ string) ([]byte, error) {
			return []byte("audio"), nil
		},
	}
	stor := &fakeUploader{}
	repo := &fakeAudioBank{
		createMediaAndCompleteJobFn: func(_ context.Context, _, _, _ uuid.UUID, _ tts.MediaFileInput) (uuid.UUID, error) {
			return uuid.Nil, fmt.Errorf("db connection lost")
		},
	}

	w := NewTTS(synth, stor, repo, "audio/mpeg")
	err := w.Handle(context.Background(), testJob(), false)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "db connection lost")
	assert.False(t, repo.bankCalled)
}

func TestHandleInvalidJobID(t *testing.T) {
	var synthCalled bool
	synth := &fakeSynthesizer{
		synthesizeFn: func(_ context.Context, _, _ string) ([]byte, error) {
			synthCalled = true
			return []byte("audio"), nil
		},
	}
	stor := &fakeUploader{}
	var markFailedCalled bool
	repo := &fakeAudioBank{
		updateStatusFn: func(_ context.Context, _ uuid.UUID, _ string) error {
			markFailedCalled = true
			return nil
		},
	}

	job := testJob()
	job.JobId = "not-a-uuid"

	w := NewTTS(synth, stor, repo, "audio/mpeg")
	err := w.Handle(context.Background(), job, false)

	require.NoError(t, err, "bad job id должен ACK'аться без ошибки")
	assert.False(t, synthCalled, "не должен синтезировать при bad job id")
	assert.False(t, markFailedCalled, "без job id нечего фейлить")
}

func TestHandlePutToBankError(t *testing.T) {
	synth := &fakeSynthesizer{
		synthesizeFn: func(_ context.Context, _, _ string) ([]byte, error) {
			return []byte("audio"), nil
		},
	}
	stor := &fakeUploader{}
	repo := &fakeAudioBank{
		putToBankFn: func(_ context.Context, _ *tts.BankEntry) error {
			return fmt.Errorf("duplicate entry")
		},
	}

	w := NewTTS(synth, stor, repo, "audio/mpeg")
	err := w.Handle(context.Background(), testJob(), false)

	require.NoError(t, err, "ошибка банка не должна фейлить job")
	assert.True(t, stor.called)
	assert.True(t, repo.mediaJobCalled)
}

func TestHandleCreateMediaFileError(t *testing.T) {
	synth := &fakeSynthesizer{
		synthesizeFn: func(_ context.Context, _, _ string) ([]byte, error) {
			return []byte("audio"), nil
		},
	}
	stor := &fakeUploader{}
	repo := &fakeAudioBank{
		createMediaAndCompleteJobFn: func(_ context.Context, _, _, _ uuid.UUID, _ tts.MediaFileInput) (uuid.UUID, error) {
			return uuid.Nil, fmt.Errorf("quota exceeded")
		},
	}
	w := NewTTS(synth, stor, repo, "audio/mpeg")
	err := w.Handle(context.Background(), testJob(), false)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "quota exceeded")
	assert.True(t, repo.mediaJobCalled)
}

func TestHandleCreateMediaFileQuotaExceeded(t *testing.T) {
	synth := &fakeSynthesizer{
		synthesizeFn: func(_ context.Context, _, _ string) ([]byte, error) {
			return []byte("audio"), nil
		},
	}
	stor := &fakeUploader{}
	var failedStatus string
	repo := &fakeAudioBank{
		createMediaAndCompleteJobFn: func(_ context.Context, _, _, _ uuid.UUID, _ tts.MediaFileInput) (uuid.UUID, error) {
			return uuid.Nil, tts.ErrQuotaExceeded
		},
		updateStatusFn: func(_ context.Context, _ uuid.UUID, status string) error {
			failedStatus = status
			return nil
		},
	}

	w := NewTTS(synth, stor, repo, "audio/mpeg")
	err := w.Handle(context.Background(), testJob(), false)

	require.NoError(t, err, "quota exceeded — permanent, ACK без ошибки")
	assert.Equal(t, tts.StatusFailed, failedStatus)
	assert.True(t, repo.mediaJobCalled)
	assert.True(t, repo.bankCalled)
}

func TestHandleInvalidOrgID(t *testing.T) {
	var synthCalled bool
	synth := &fakeSynthesizer{
		synthesizeFn: func(_ context.Context, _, _ string) ([]byte, error) {
			synthCalled = true
			return []byte("audio"), nil
		},
	}
	stor := &fakeUploader{}
	var failedStatus string
	repo := &fakeAudioBank{
		updateStatusFn: func(_ context.Context, _ uuid.UUID, status string) error {
			failedStatus = status
			return nil
		},
	}

	job := testJob()
	job.OrgID = "not-a-uuid"

	w := NewTTS(synth, stor, repo, "audio/mpeg")
	err := w.Handle(context.Background(), job, false)

	require.NoError(t, err, "bad org id должен ACK'аться без ошибки")
	assert.False(t, synthCalled, "не должен синтезировать при bad org id")
	assert.Equal(t, tts.StatusFailed, failedStatus, "должен пометить job как failed")
}

func TestHandleInvalidUserID(t *testing.T) {
	var synthCalled bool
	synth := &fakeSynthesizer{
		synthesizeFn: func(_ context.Context, _, _ string) ([]byte, error) {
			synthCalled = true
			return []byte("audio"), nil
		},
	}
	stor := &fakeUploader{}
	var failedStatus string
	repo := &fakeAudioBank{
		updateStatusFn: func(_ context.Context, _ uuid.UUID, status string) error {
			failedStatus = status
			return nil
		},
	}

	job := testJob()
	job.UserID = "not-a-uuid"

	w := NewTTS(synth, stor, repo, "audio/mpeg")
	err := w.Handle(context.Background(), job, false)

	require.NoError(t, err, "bad user id должен ACK'аться без ошибки")
	assert.False(t, synthCalled, "не должен синтезировать при bad user id")
	assert.Equal(t, tts.StatusFailed, failedStatus, "должен пометить job как failed")
}

func TestHandleTruncatesName(t *testing.T) {
	longText := strings.Repeat("а", 100)

	synth := &fakeSynthesizer{
		synthesizeFn: func(_ context.Context, _, _ string) ([]byte, error) {
			return []byte("audio"), nil
		},
	}
	stor := &fakeUploader{}
	var gotName string
	repo := &fakeAudioBank{
		createMediaAndCompleteJobFn: func(_ context.Context, _, _, _ uuid.UUID, input tts.MediaFileInput) (uuid.UUID, error) {
			gotName = input.Name
			return uuid.New(), nil
		},
	}

	job := testJob()
	job.Text = longText

	w := NewTTS(synth, stor, repo, "audio/mpeg")
	err := w.Handle(context.Background(), job, false)

	require.NoError(t, err)
	assert.Equal(t, 51, len([]rune(gotName)), "50 символов + …")
}
