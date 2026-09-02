package pack

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/authctx"
	"github.com/Linka-masterskaya/zip-backend/internal/mailer"
	"github.com/Linka-masterskaya/zip-backend/internal/student"
	"github.com/Linka-masterskaya/zip-backend/pkg/linka"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShareServiceFolderDelegatesToDuplicateWithoutCopySuffix(t *testing.T) {
	packID, folderID := uuid.New(), uuid.New()
	copied := &Pack{ID: uuid.New(), FolderID: folderID, Status: "draft", Title: "Original"}
	packs := &sharePackFake{duplicateFn: func(
		_ context.Context, gotPackID uuid.UUID, input DuplicateInput,
	) (*Pack, error) {
		assert.Equal(t, packID, gotPackID)
		require.NotNil(t, input.FolderID)
		assert.Equal(t, folderID, *input.FolderID)
		assert.True(t, input.PreserveTitle)
		return copied, nil
	}}
	service := NewShareService(packs, nil, nil, nil)
	shutdownShareService(t, service)

	result, err := service.Share(context.Background(), packID, ShareInput{
		TargetType: ShareTargetFolder,
		TargetID:   folderID,
	})

	require.NoError(t, err)
	assert.Same(t, copied, result.Pack)
	assert.Nil(t, result.Task)
}

func TestShareServiceStudentQueuesExportAndUsesOriginalPackTitle(t *testing.T) {
	userID, packID, studentID := uuid.New(), uuid.New(), uuid.New()
	content := &shareContentFake{archive: testArchive("Speech _ pack.linka", []byte("linka archive"))}
	students := &shareStudentFake{student: &student.Student{
		ID: studentID, Email: "student@example.com", Name: "Анна",
	}}
	packs := &sharePackFake{pack: &Pack{ID: packID, OwnerID: userID, Title: "Speech / pack"}}
	calls := make(chan shareMailCall, 1)
	mailSender := &shareMailerFake{calls: calls}
	service := NewShareService(packs, content, students, mailSender)
	shutdownShareService(t, service)

	ctx := authctx.SetUserIDToCtx(context.Background(), userID)
	result, err := service.Share(ctx, packID, ShareInput{TargetType: ShareTargetStudent, TargetID: studentID})

	require.NoError(t, err)
	require.NotNil(t, result.Task)
	assert.Equal(t, ShareTaskQueued, result.Task.Status)
	assert.Nil(t, result.Pack)

	select {
	case call := <-calls:
		assert.Equal(t, "student@example.com", call.to)
		assert.Equal(t, mailer.PackShare, call.template)
		assert.Equal(t, "Анна", call.data.Username)
		assert.Equal(t, "Speech / pack", call.data.PackTitle)
		require.Len(t, call.data.Attachments, 1)
		assert.Equal(t, "Speech _ pack.linka", call.data.Attachments[0].Filename)
		assert.Equal(t, "application/vnd.linka+zip", call.data.Attachments[0].ContentType)
		assert.Equal(t, []byte("linka archive"), call.attachment)
	case <-time.After(2 * time.Second):
		t.Fatal("share email was not dispatched")
	}

	assert.Equal(t, linka.FormatLooks3, content.format)
	assert.Equal(t, packID, content.packID)
	assert.Equal(t, studentID, students.studentID)
	require.Eventually(t, func() bool {
		task, err := service.GetTask(ctx, result.Task.ID)
		return err == nil && task.Status == ShareTaskSent
	}, 2*time.Second, 10*time.Millisecond)
}

func TestShareServiceStudentReturnsBeforeExportRuns(t *testing.T) {
	userID, packID, studentID := uuid.New(), uuid.New(), uuid.New()
	block := make(chan struct{})
	content := &shareContentFake{exportFn: func(_ context.Context, _ uuid.UUID, _ linka.Format) (*ExportArchive, error) {
		<-block
		return testArchive("pack.linka", []byte("data")), nil
	}}
	service := NewShareService(
		&sharePackFake{pack: &Pack{ID: packID, OwnerID: userID, Title: "Pack"}},
		content,
		&shareStudentFake{student: &student.Student{ID: studentID, Email: "student@example.com"}},
		&shareMailerFake{},
	)
	shutdownShareService(t, service)

	ctx := authctx.SetUserIDToCtx(context.Background(), userID)
	done := make(chan error, 1)
	go func() {
		_, err := service.Share(ctx, packID, ShareInput{TargetType: ShareTargetStudent, TargetID: studentID})
		done <- err
	}()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(250 * time.Millisecond):
		t.Fatal("Share waited for archive export")
	}
	close(block)
}

func TestShareServiceRejectsPublishedForeignPackForEmail(t *testing.T) {
	userID := uuid.New()
	service := NewShareService(
		&sharePackFake{pack: &Pack{ID: uuid.New(), OwnerID: uuid.New(), Title: "Foreign"}},
		&shareContentFake{},
		&shareStudentFake{student: &student.Student{ID: uuid.New(), Email: "student@example.com"}},
		&shareMailerFake{},
	)
	shutdownShareService(t, service)

	ctx := authctx.SetUserIDToCtx(context.Background(), userID)
	_, err := service.Share(ctx, uuid.New(), ShareInput{TargetType: ShareTargetStudent, TargetID: uuid.New()})
	assertAppErrorStatus(t, err, 403)
}

func TestShareServiceStudentValidationStopsBeforeExport(t *testing.T) {
	userID := uuid.New()
	content := &shareContentFake{}
	students := &shareStudentFake{err: apperr.ErrNotFound}
	service := NewShareService(&sharePackFake{}, content, students, &shareMailerFake{})
	shutdownShareService(t, service)

	ctx := authctx.SetUserIDToCtx(context.Background(), userID)
	_, err := service.Share(ctx, uuid.New(), ShareInput{TargetType: ShareTargetStudent, TargetID: uuid.New()})

	assert.Error(t, err)
	assert.Equal(t, uuid.Nil, content.packID)
}

func TestShareServiceRecoversMailerPanic(t *testing.T) {
	userID, packID, studentID := uuid.New(), uuid.New(), uuid.New()
	service := NewShareServiceWithConfig(
		&sharePackFake{pack: &Pack{ID: packID, OwnerID: userID, Title: "Pack"}},
		&shareContentFake{archive: testArchive("Pack.linka", []byte("archive"))},
		&shareStudentFake{student: &student.Student{ID: studentID, Email: "student@example.com"}},
		panickingShareMailer{},
		nil,
		ShareConfig{Workers: 1, SendRetries: 1, RetryBackoff: time.Millisecond},
	)
	shutdownShareService(t, service)

	ctx := authctx.SetUserIDToCtx(context.Background(), userID)
	result, err := service.Share(ctx, packID, ShareInput{TargetType: ShareTargetStudent, TargetID: studentID})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		task, getErr := service.GetTask(ctx, result.Task.ID)
		return getErr == nil && task.Status == ShareTaskFailed
	}, time.Second, 10*time.Millisecond)
}

func TestShareServiceShutdownRejectsInFlightEnqueue(t *testing.T) {
	userID, packID, studentID := uuid.New(), uuid.New(), uuid.New()
	getStarted := make(chan struct{})
	releaseGet := make(chan struct{})
	packs := &sharePackFake{getFn: func(_ context.Context, gotPackID uuid.UUID) (*Pack, error) {
		assert.Equal(t, packID, gotPackID)
		close(getStarted)
		<-releaseGet
		return &Pack{ID: packID, OwnerID: userID, Title: "Pack"}, nil
	}}
	service := NewShareServiceWithConfig(
		packs,
		&shareContentFake{archive: testArchive("Pack.linka", []byte("archive"))},
		&shareStudentFake{student: &student.Student{ID: studentID, Email: "student@example.com"}},
		&shareMailerFake{},
		nil,
		ShareConfig{Workers: 1, SendRetries: 1, RetryBackoff: time.Millisecond},
	)

	ctx := authctx.SetUserIDToCtx(context.Background(), userID)
	shareDone := make(chan error, 1)
	go func() {
		_, err := service.Share(ctx, packID, ShareInput{TargetType: ShareTargetStudent, TargetID: studentID})
		shareDone <- err
	}()

	select {
	case <-getStarted:
	case <-time.After(time.Second):
		t.Fatal("Share did not reach pack validation")
	}

	shutdownDone := make(chan error, 1)
	go func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		shutdownDone <- service.Shutdown(shutdownCtx)
	}()

	require.Eventually(t, func() bool { return !service.accepting.Load() }, time.Second, time.Millisecond)
	close(releaseGet)

	select {
	case err := <-shareDone:
		assertAppErrorStatus(t, err, 503)
	case <-time.After(time.Second):
		t.Fatal("Share did not return after shutdown")
	}

	select {
	case err := <-shutdownDone:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("Shutdown did not finish")
	}
}

func TestShareServiceRetriesMailer(t *testing.T) {
	userID, packID, studentID := uuid.New(), uuid.New(), uuid.New()
	calls := make(chan shareMailCall, 3)
	mailerFake := &shareMailerFake{calls: calls, errors: []error{errors.New("smtp down"), nil}}
	service := NewShareServiceWithConfig(
		&sharePackFake{pack: &Pack{ID: packID, OwnerID: userID, Title: "Pack"}},
		&shareContentFake{archive: testArchive("Pack.linka", []byte("archive"))},
		&shareStudentFake{student: &student.Student{ID: studentID, Email: "student@example.com"}},
		mailerFake,
		nil,
		ShareConfig{Workers: 1, SendRetries: 2, RetryBackoff: time.Millisecond},
	)
	shutdownShareService(t, service)

	ctx := authctx.SetUserIDToCtx(context.Background(), userID)
	result, err := service.Share(ctx, packID, ShareInput{TargetType: ShareTargetStudent, TargetID: studentID})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		task, getErr := service.GetTask(ctx, result.Task.ID)
		return getErr == nil && task.Status == ShareTaskSent
	}, time.Second, 10*time.Millisecond)
	assert.Equal(t, 2, mailerFake.callCount())
}

func TestShareServiceRejectsInvalidTarget(t *testing.T) {
	service := NewShareService(&sharePackFake{}, nil, nil, nil)
	shutdownShareService(t, service)
	_, err := service.Share(context.Background(), uuid.New(), ShareInput{
		TargetType: "unknown", TargetID: uuid.New(),
	})
	assertAppErrorStatus(t, err, 400)
}

func shutdownShareService(t *testing.T, service *ShareService) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = service.Shutdown(ctx)
	})
}

type sharePackFake struct {
	duplicateFn func(context.Context, uuid.UUID, DuplicateInput) (*Pack, error)
	getFn       func(context.Context, uuid.UUID) (*Pack, error)
	pack        *Pack
	err         error
}

func (f *sharePackFake) Duplicate(ctx context.Context, packID uuid.UUID, input DuplicateInput) (*Pack, error) {
	if f.duplicateFn != nil {
		return f.duplicateFn(ctx, packID, input)
	}
	return &Pack{}, nil
}

func (f *sharePackFake) Get(ctx context.Context, packID uuid.UUID) (*Pack, error) {
	if f.getFn != nil {
		return f.getFn(ctx, packID)
	}
	if f.err != nil {
		return nil, f.err
	}
	if f.pack != nil {
		return f.pack, nil
	}
	return &Pack{}, nil
}

type shareContentFake struct {
	mu       sync.Mutex
	packID   uuid.UUID
	format   linka.Format
	archive  *ExportArchive
	err      error
	exportFn func(context.Context, uuid.UUID, linka.Format) (*ExportArchive, error)
}

func (f *shareContentFake) Export(ctx context.Context, packID uuid.UUID, format linka.Format) (*ExportArchive, error) {
	f.mu.Lock()
	f.packID = packID
	f.format = format
	f.mu.Unlock()
	if f.exportFn != nil {
		return f.exportFn(ctx, packID, format)
	}
	return f.archive, f.err
}

type shareStudentFake struct {
	studentID uuid.UUID
	student   *student.Student
	err       error
}

func (f *shareStudentFake) Get(_ context.Context, studentID uuid.UUID) (*student.Student, error) {
	f.studentID = studentID
	return f.student, f.err
}

type shareMailCall struct {
	to         string
	template   mailer.Template
	data       mailer.EmailData
	attachment []byte
}

type shareMailerFake struct {
	mu     sync.Mutex
	calls  chan<- shareMailCall
	err    error
	errors []error
	count  int
}

func (f *shareMailerFake) Send(_ context.Context, to string, tmpl mailer.Template, data mailer.EmailData) error {
	call := shareMailCall{to: to, template: tmpl, data: data}
	if len(data.Attachments) > 0 && data.Attachments[0].Reader != nil {
		call.attachment, _ = io.ReadAll(data.Attachments[0].Reader)
	}
	if f.calls != nil {
		f.calls <- call
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.count++
	if len(f.errors) > 0 {
		err := f.errors[0]
		f.errors = f.errors[1:]
		return err
	}
	return f.err
}

func (f *shareMailerFake) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.count
}

type panickingShareMailer struct{}

func (panickingShareMailer) Send(context.Context, string, mailer.Template, mailer.EmailData) error {
	panic("smtp panic")
}

type readSeekCloser struct{ *bytes.Reader }

func (readSeekCloser) Close() error { return nil }

func testArchive(name string, data []byte) *ExportArchive {
	return &ExportArchive{
		Stream:   readSeekCloser{bytes.NewReader(data)},
		Filename: name,
		Size:     int64(len(data)),
	}
}

func TestShareServiceProcessesQueuedOutboxJobAfterRestart(t *testing.T) {
	userID, packID, studentID := uuid.New(), uuid.New(), uuid.New()
	jobs := newMemoryShareJobRepository()
	now := time.Now().UTC()
	require.NoError(t, jobs.EnqueueShareJob(t.Context(), shareJobRecord{
		ID:            uuid.New(),
		OwnerID:       userID,
		PackID:        packID,
		StudentID:     studentID,
		Status:        ShareTaskQueued,
		NextAttemptAt: now,
		CreatedAt:     now,
		UpdatedAt:     now,
	}))

	calls := make(chan shareMailCall, 1)
	service := NewShareServiceWithOutbox(
		&sharePackFake{pack: &Pack{ID: packID, OwnerID: userID, Title: "Recovered pack"}},
		&shareContentFake{archive: testArchive("Recovered pack.linka", []byte("archive"))},
		&shareStudentFake{student: &student.Student{ID: studentID, Email: "student@example.com", Name: "Anna"}},
		&shareMailerFake{calls: calls},
		jobs,
		nil,
		ShareConfig{Workers: 1, PollInterval: time.Millisecond, JobTimeout: time.Minute, SendRetries: 1, SendTimeout: time.Second, RetryBackoff: time.Millisecond},
	)
	shutdownShareService(t, service)

	select {
	case call := <-calls:
		assert.Equal(t, "student@example.com", call.to)
		assert.Equal(t, "Recovered pack", call.data.PackTitle)
	case <-time.After(time.Second):
		t.Fatal("queued durable outbox job was not resumed on service startup")
	}
}

func TestShareServiceShutdownRequeuesInterruptedOutboxJob(t *testing.T) {
	userID, packID, studentID := uuid.New(), uuid.New(), uuid.New()
	jobs := newMemoryShareJobRepository()
	mailerFake := &cancelAwareShareMailer{started: make(chan struct{})}
	service := NewShareServiceWithOutbox(
		&sharePackFake{pack: &Pack{ID: packID, OwnerID: userID, Title: "Pack"}},
		&shareContentFake{archive: testArchive("Pack.linka", []byte("archive"))},
		&shareStudentFake{student: &student.Student{ID: studentID, Email: "student@example.com"}},
		mailerFake,
		jobs,
		nil,
		ShareConfig{Workers: 1, PollInterval: time.Millisecond, JobTimeout: time.Minute, SendRetries: 1, SendTimeout: time.Minute, RetryBackoff: time.Millisecond},
	)

	ctx := authctx.SetUserIDToCtx(context.Background(), userID)
	result, err := service.Share(ctx, packID, ShareInput{TargetType: ShareTargetStudent, TargetID: studentID})
	require.NoError(t, err)

	select {
	case <-mailerFake.started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start email delivery")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err = service.Shutdown(shutdownCtx)
	assert.ErrorIs(t, err, context.DeadlineExceeded)

	require.Eventually(t, func() bool {
		job, getErr := jobs.GetShareJob(context.Background(), result.Task.ID)
		return getErr == nil && job.Status == ShareTaskQueued
	}, time.Second, 10*time.Millisecond, "interrupted durable job must be requeued instead of lost")
}

func TestShareServiceTaskIsHiddenFromAnotherOwnerWithOutbox(t *testing.T) {
	ownerID, otherID, packID, studentID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	jobs := newMemoryShareJobRepository()
	service := NewShareServiceWithOutbox(
		&sharePackFake{pack: &Pack{ID: packID, OwnerID: ownerID, Title: "Pack"}},
		&shareContentFake{archive: testArchive("Pack.linka", []byte("archive"))},
		&shareStudentFake{student: &student.Student{ID: studentID, Email: "student@example.com"}},
		&shareMailerFake{},
		jobs,
		nil,
		ShareConfig{Workers: 1, PollInterval: time.Second, JobTimeout: time.Minute, SendRetries: 1, SendTimeout: time.Second, RetryBackoff: time.Millisecond},
	)
	shutdownShareService(t, service)

	ctx := authctx.SetUserIDToCtx(context.Background(), ownerID)
	result, err := service.Share(ctx, packID, ShareInput{TargetType: ShareTargetStudent, TargetID: studentID})
	require.NoError(t, err)

	otherCtx := authctx.SetUserIDToCtx(context.Background(), otherID)
	_, err = service.GetTask(otherCtx, result.Task.ID)
	assertAppErrorStatus(t, err, 404)
}

type cancelAwareShareMailer struct {
	started chan struct{}
	once    sync.Once
}

func (m *cancelAwareShareMailer) Send(ctx context.Context, _ string, _ mailer.Template, _ mailer.EmailData) error {
	m.once.Do(func() { close(m.started) })
	<-ctx.Done()
	return ctx.Err()
}

func TestShareServiceDailySendQuotaRejectsBeforeOutbox(t *testing.T) {
	userID, packID, studentID := uuid.New(), uuid.New(), uuid.New()
	jobs := newMemoryShareJobRepository()
	service := NewShareServiceWithOutbox(
		&sharePackFake{pack: &Pack{ID: packID, OwnerID: userID, Title: "Pack"}},
		&shareContentFake{archive: testArchive("Pack.linka", []byte("archive"))},
		&shareStudentFake{student: &student.Student{ID: studentID, Email: "student@example.com"}},
		&shareMailerFake{},
		jobs,
		nil,
		ShareConfig{Workers: 1, PollInterval: time.Second, JobTimeout: time.Minute, SendRetries: 1, SendTimeout: time.Second, RetryBackoff: time.Millisecond},
	)
	service.quota = &shareQuotaFake{sendAllowed: false, bytesAllowed: true}
	shutdownShareService(t, service)

	ctx := authctx.SetUserIDToCtx(context.Background(), userID)
	_, err := service.Share(ctx, packID, ShareInput{TargetType: ShareTargetStudent, TargetID: studentID})
	assertAppErrorStatus(t, err, 429)
}

func TestShareServiceDailyBytesQuotaFailsAcceptedTask(t *testing.T) {
	userID, packID, studentID := uuid.New(), uuid.New(), uuid.New()
	mailerFake := &shareMailerFake{}
	service := NewShareServiceWithConfig(
		&sharePackFake{pack: &Pack{ID: packID, OwnerID: userID, Title: "Pack"}},
		&shareContentFake{archive: testArchive("Pack.linka", []byte("archive"))},
		&shareStudentFake{student: &student.Student{ID: studentID, Email: "student@example.com"}},
		mailerFake,
		nil,
		ShareConfig{Workers: 1, PollInterval: time.Millisecond, JobTimeout: time.Minute, SendRetries: 1, SendTimeout: time.Second, RetryBackoff: time.Millisecond},
	)
	service.quota = &shareQuotaFake{sendAllowed: true, bytesAllowed: false}
	shutdownShareService(t, service)

	ctx := authctx.SetUserIDToCtx(context.Background(), userID)
	result, err := service.Share(ctx, packID, ShareInput{TargetType: ShareTargetStudent, TargetID: studentID})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		task, getErr := service.GetTask(ctx, result.Task.ID)
		return getErr == nil && task.Status == ShareTaskFailed && task.Message == "daily pack share volume limit exceeded"
	}, time.Second, 10*time.Millisecond)
	assert.Zero(t, mailerFake.callCount())
}

func TestShareServiceOversizedArchiveFailsWithoutSending(t *testing.T) {
	userID, packID, studentID := uuid.New(), uuid.New(), uuid.New()
	mailerFake := &shareMailerFake{}
	service := NewShareServiceWithConfig(
		&sharePackFake{pack: &Pack{ID: packID, OwnerID: userID, Title: "Pack"}},
		&shareContentFake{archive: testArchive("Pack.linka", []byte("12345"))},
		&shareStudentFake{student: &student.Student{ID: studentID, Email: "student@example.com"}},
		mailerFake,
		nil,
		ShareConfig{Workers: 1, PollInterval: time.Millisecond, JobTimeout: time.Minute, MaxAttachmentBytes: 4, SendRetries: 1, SendTimeout: time.Second, RetryBackoff: time.Millisecond},
	)
	shutdownShareService(t, service)

	ctx := authctx.SetUserIDToCtx(context.Background(), userID)
	result, err := service.Share(ctx, packID, ShareInput{TargetType: ShareTargetStudent, TargetID: studentID})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		task, getErr := service.GetTask(ctx, result.Task.ID)
		return getErr == nil && task.Status == ShareTaskFailed && task.Message == "archive exceeds email attachment limit; use direct export"
	}, time.Second, 10*time.Millisecond)
	assert.Zero(t, mailerFake.callCount())
}

type shareQuotaFake struct {
	sendAllowed  bool
	bytesAllowed bool
	err          error
}

func (f *shareQuotaFake) ReserveSend(context.Context, uuid.UUID) (bool, error) {
	return f.sendAllowed, f.err
}

func (f *shareQuotaFake) ReserveBytes(context.Context, uuid.UUID, int64) (bool, error) {
	return f.bytesAllowed, f.err
}
