package pack

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/authctx"
	"github.com/Linka-masterskaya/zip-backend/internal/cache"
	"github.com/Linka-masterskaya/zip-backend/internal/mailer"
	"github.com/Linka-masterskaya/zip-backend/internal/student"
	"github.com/Linka-masterskaya/zip-backend/pkg/linka"
	"github.com/google/uuid"
)

const (
	shareTaskTTL              = 24 * time.Hour
	shareDefaultPollInterval  = 500 * time.Millisecond
	shareDefaultJobTimeout    = 10 * time.Minute
	shareJobLeaseGrace        = time.Minute
	shareOutboxStoreTimeout   = 2 * time.Second
	shareOutboxPruneInterval  = time.Hour
	shareInterruptedRetryWait = time.Second
)

type ShareTargetType string

const (
	ShareTargetFolder  ShareTargetType = "folder"
	ShareTargetStudent ShareTargetType = "student"
)

type ShareTaskStatus string

const (
	ShareTaskQueued     ShareTaskStatus = "queued"
	ShareTaskProcessing ShareTaskStatus = "processing"
	ShareTaskSent       ShareTaskStatus = "sent"
	ShareTaskFailed     ShareTaskStatus = "failed"
)

// ShareInput identifies the destination selected in the share modal.
type ShareInput struct {
	TargetType ShareTargetType
	TargetID   uuid.UUID
}

// ShareTask is the durable delivery status returned to the caller.
type ShareTask struct {
	ID        uuid.UUID       `json:"id"`
	Status    ShareTaskStatus `json:"status"`
	Message   string          `json:"message,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// ShareResult contains either the newly duplicated pack (folder target) or an
// accepted asynchronous email-delivery task (student target).
type ShareResult struct {
	Pack *Pack
	Task *ShareTask
}

type ShareConfig struct {
	Workers            int
	PollInterval       time.Duration
	JobTimeout         time.Duration
	DailySendsPerUser  int64
	DailyBytesPerUser  int64
	MaxAttachmentBytes int64
	SendRetries        int
	SendTimeout        time.Duration
	RetryBackoff       time.Duration
}

type sharePackService interface {
	Duplicate(context.Context, uuid.UUID, DuplicateInput) (*Pack, error)
	Get(context.Context, uuid.UUID) (*Pack, error)
}

type shareContentService interface {
	Export(context.Context, uuid.UUID, linka.Format) (*ExportArchive, error)
}

type shareStudentService interface {
	Get(context.Context, uuid.UUID) (*student.Student, error)
}

type shareMailer interface {
	Send(context.Context, string, mailer.Template, mailer.EmailData) error
}

type shareQuota interface {
	ReserveSend(context.Context, uuid.UUID) (bool, error)
	ReserveBytes(context.Context, uuid.UUID, int64) (bool, error)
}

type redisShareQuota struct {
	cache      *cache.Client
	sendLimit  int64
	bytesLimit int64
	now        func() time.Time
}

func newRedisShareQuota(c *cache.Client, sendLimit, bytesLimit int64) shareQuota {
	if c == nil {
		return nil
	}
	// Invalid limits are rejected during config validation. Keeping the quota
	// object here makes a bypassed validation fail closed rather than silently
	// turning anti-abuse controls off.
	return &redisShareQuota{cache: c, sendLimit: sendLimit, bytesLimit: bytesLimit, now: time.Now}
}

func (q *redisShareQuota) ReserveSend(ctx context.Context, userID uuid.UUID) (bool, error) {
	return q.reserve(ctx, "send", userID, 1, q.sendLimit)
}

func (q *redisShareQuota) ReserveBytes(ctx context.Context, userID uuid.UUID, size int64) (bool, error) {
	return q.reserve(ctx, "bytes", userID, size, q.bytesLimit)
}

func (q *redisShareQuota) reserve(
	ctx context.Context,
	kind string,
	userID uuid.UUID,
	delta, limit int64,
) (bool, error) {
	now := q.now().UTC()
	tomorrow := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
	ttl := tomorrow.Sub(now)
	if ttl <= 0 {
		ttl = time.Second
	}
	key := fmt.Sprintf("pack_share:%s:%s:%s", kind, now.Format("2006-01-02"), userID)
	allowed, _, err := q.cache.ReserveCounter(ctx, key, delta, limit, ttl)
	return allowed, err
}

// ShareService composes existing domain operations. Folder sharing reuses
// Duplicate with title preservation. Student delivery is backed by a durable
// PostgreSQL outbox: HTTP 202 never depends on an in-memory queue surviving a
// deploy, and workers claim jobs with a lease so multiple instances are safe.
type ShareService struct {
	packs    sharePackService
	content  shareContentService
	students shareStudentService
	mailer   shareMailer
	quota    shareQuota
	jobs     shareJobRepository
	cfg      ShareConfig

	workersWG sync.WaitGroup
	stopOnce  sync.Once
	accepting atomic.Bool
	stopping  atomic.Bool
	workerCtx context.Context
	cancel    context.CancelFunc
	stopClaim chan struct{}
	wake      chan struct{}
}

func NewShareService(
	packs sharePackService,
	content shareContentService,
	students shareStudentService,
	mailerSender shareMailer,
) *ShareService {
	return NewShareServiceWithConfig(packs, content, students, mailerSender, nil, ShareConfig{})
}

// NewShareServiceWithConfig keeps unit-level callers simple by using the same
// outbox contract with an in-memory implementation. Production wiring must use
// NewShareServiceWithOutbox so accepted tasks are durable across restarts.
func NewShareServiceWithConfig(
	packs sharePackService,
	content shareContentService,
	students shareStudentService,
	mailerSender shareMailer,
	cacheClient *cache.Client,
	cfg ShareConfig,
) *ShareService {
	return NewShareServiceWithOutbox(
		packs,
		content,
		students,
		mailerSender,
		newMemoryShareJobRepository(),
		cacheClient,
		cfg,
	)
}

// NewShareServiceWithOutbox creates a share service backed by a durable job repository.
func NewShareServiceWithOutbox(
	packs sharePackService,
	content shareContentService,
	students shareStudentService,
	mailerSender shareMailer,
	jobs shareJobRepository,
	cacheClient *cache.Client,
	cfg ShareConfig,
) *ShareService {
	if cfg.Workers <= 0 {
		cfg.Workers = 2
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = shareDefaultPollInterval
	}
	if cfg.JobTimeout <= 0 {
		cfg.JobTimeout = shareDefaultJobTimeout
	}
	if cfg.MaxAttachmentBytes <= 0 {
		cfg.MaxAttachmentBytes = 15 * 1024 * 1024
	}
	if cfg.SendRetries <= 0 {
		cfg.SendRetries = 3
	}
	if cfg.SendTimeout <= 0 {
		cfg.SendTimeout = 30 * time.Second
	}
	if cfg.RetryBackoff <= 0 {
		cfg.RetryBackoff = 2 * time.Second
	}
	if jobs == nil {
		jobs = newMemoryShareJobRepository()
	}

	workerCtx, cancel := context.WithCancel(context.Background())
	s := &ShareService{
		packs:     packs,
		content:   content,
		students:  students,
		mailer:    mailerSender,
		quota:     newRedisShareQuota(cacheClient, cfg.DailySendsPerUser, cfg.DailyBytesPerUser),
		jobs:      jobs,
		cfg:       cfg,
		workerCtx: workerCtx,
		cancel:    cancel,
		stopClaim: make(chan struct{}),
		wake:      make(chan struct{}, 1),
	}
	s.accepting.Store(true)
	for i := 0; i < cfg.Workers; i++ {
		s.workersWG.Add(1)
		go s.worker(i + 1)
	}
	return s
}

// Share shares one accessible pack with the selected target.
func (s *ShareService) Share(ctx context.Context, packID uuid.UUID, input ShareInput) (*ShareResult, error) {
	if packID == uuid.Nil {
		return nil, apperr.ErrBadRequest.WithMessage("pack id must be a valid UUID")
	}
	if input.TargetID == uuid.Nil {
		return nil, apperr.ErrBadRequest.WithMessage("target_id must be a valid UUID")
	}

	switch input.TargetType {
	case ShareTargetFolder:
		if s.packs == nil {
			return nil, fmt.Errorf("pack share duplicate service is not configured")
		}
		duplicated, err := s.packs.Duplicate(ctx, packID, DuplicateInput{
			FolderID:      &input.TargetID,
			PreserveTitle: true,
		})
		if err != nil {
			return nil, err
		}
		return &ShareResult{Pack: duplicated}, nil
	case ShareTargetStudent:
		return s.shareWithStudent(ctx, packID, input.TargetID)
	default:
		return nil, apperr.ErrBadRequest.WithMessage("target_type must be folder or student")
	}
}

func (s *ShareService) shareWithStudent(
	ctx context.Context,
	packID, studentID uuid.UUID,
) (*ShareResult, error) {
	if !s.studentShareReady() {
		return nil, fmt.Errorf("pack share dependencies are not configured")
	}
	if !s.accepting.Load() {
		return nil, apperr.ErrServiceUnavailable.WithMessage("pack share service is shutting down")
	}

	userID, err := authctx.UserIDFromCtx(ctx)
	if err != nil {
		return nil, err
	}

	// Validate ownership before persisting any outbound work. The outbox stores
	// only IDs, never the student's decrypted email address.
	target, err := s.students.Get(ctx, studentID)
	if err != nil {
		return nil, err
	}
	if target == nil {
		return nil, fmt.Errorf("student share target is empty")
	}

	// Email export is intentionally stricter than normal GET /export: only the
	// owner may send a pack outside the organization.
	accessiblePack, err := s.packs.Get(ctx, packID)
	if err != nil {
		return nil, err
	}
	if accessiblePack == nil {
		return nil, fmt.Errorf("pack share source is empty")
	}
	if accessiblePack.OwnerID != userID {
		return nil, apperr.ErrForbidden.WithMessage("only the pack owner may share it by email")
	}

	if err := s.reserveShareSend(ctx, userID); err != nil {
		return nil, err
	}

	// A final shutdown check avoids accepting new work once drain has started.
	// If shutdown starts immediately after this check, the DB insert still makes
	// the accepted job durable for another instance or the next process start.
	if !s.accepting.Load() {
		return nil, apperr.ErrServiceUnavailable.WithMessage("pack share service is shutting down")
	}

	now := time.Now().UTC()
	job := shareJobRecord{
		ID:            uuid.New(),
		OwnerID:       userID,
		PackID:        packID,
		StudentID:     studentID,
		RequestID:     authctx.RequestIDFromCtx(ctx),
		Status:        ShareTaskQueued,
		NextAttemptAt: now,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.jobs.EnqueueShareJob(ctx, job); err != nil {
		return nil, apperr.ErrServiceUnavailable.
			WithError(err).
			WithMessage("pack share outbox is unavailable")
	}
	s.signalWork()

	task := shareTaskFromJob(&job)
	return &ShareResult{Task: &task}, nil
}

func (s *ShareService) reserveShareSend(ctx context.Context, userID uuid.UUID) error {
	if s.quota == nil {
		return nil
	}
	allowed, err := s.quota.ReserveSend(ctx, userID)
	if err != nil {
		return fmt.Errorf("reserve pack share quota: %w", err)
	}
	if !allowed {
		return apperr.ErrTooManyRequests.WithMessage("daily pack share email limit exceeded")
	}
	return nil
}

func (s *ShareService) studentShareReady() bool {
	return s.students != nil && s.content != nil && s.mailer != nil && s.packs != nil && s.jobs != nil
}

func (s *ShareService) signalWork() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *ShareService) worker(workerID int) {
	defer s.workersWG.Done()
	nextPrune := time.Now()

	for {
		if s.stopping.Load() {
			return
		}
		if workerID == 1 && time.Now().After(nextPrune) {
			s.pruneOutbox()
			nextPrune = time.Now().Add(shareOutboxPruneInterval)
		}

		job, err := s.jobs.ClaimShareJob(s.workerCtx, s.jobLease())
		if err != nil {
			if s.workerCtx.Err() != nil {
				return
			}
			slog.Error("claim pack share outbox job", "worker", workerID, "err", err)
			if !s.waitForWork() {
				return
			}
			continue
		}
		if job == nil {
			if !s.waitForWork() {
				return
			}
			continue
		}
		if s.stopping.Load() {
			// Shutdown raced with the DB claim. Return the durable lease instead
			// of starting new expensive work after drain has begun.
			s.requeueInterrupted(job, context.Canceled)
			return
		}

		s.processJobSafely(workerID, job)
	}
}

func (s *ShareService) jobLease() time.Duration {
	return s.cfg.JobTimeout + shareJobLeaseGrace
}

func (s *ShareService) waitForWork() bool {
	timer := time.NewTimer(s.cfg.PollInterval)
	defer timer.Stop()
	select {
	case <-s.stopClaim:
		return false
	case <-s.workerCtx.Done():
		return false
	case <-s.wake:
		return true
	case <-timer.C:
		return true
	}
}

func (s *ShareService) pruneOutbox() {
	ctx, cancel := context.WithTimeout(context.Background(), shareOutboxStoreTimeout)
	defer cancel()
	deleted, err := s.jobs.PruneShareJobs(ctx, time.Now().UTC().Add(-shareTaskTTL))
	if err != nil {
		slog.Warn("prune pack share outbox", "err", err)
		return
	}
	if deleted > 0 {
		slog.Debug("pruned pack share outbox", "jobs", deleted)
	}
}

func (s *ShareService) processJobSafely(workerID int, job *shareJobRecord) {
	defer func() {
		if recovered := recover(); recovered != nil {
			s.failJob(job, "delivery failed")
			slog.Error("pack share worker panic recovered",
				"worker", workerID,
				"task_id", job.ID,
				"pack_id", job.PackID,
				"student_id", job.StudentID,
				"panic", recovered,
				"stack", string(debug.Stack()),
			)
		}
	}()
	s.processJob(job)
}

func (s *ShareService) processJob(job *shareJobRecord) {
	baseCtx := authctx.SetUserIDToCtx(s.workerCtx, job.OwnerID)
	if job.RequestID != "" {
		baseCtx = authctx.SetRequestIDToCtx(baseCtx, job.RequestID)
	}
	ctx, cancel := context.WithTimeout(baseCtx, s.cfg.JobTimeout)
	defer cancel()

	target, accessiblePack, message, err := s.loadShareJobTargets(ctx, job)
	if err != nil {
		s.handleJobError(ctx, job, message, err)
		return
	}

	archive, message, err := s.exportShareJobArchive(ctx, job)
	if err != nil {
		s.handleJobError(ctx, job, message, err)
		return
	}
	defer s.closeShareArchive(ctx, job, archive)

	seeker, message, err := s.prepareShareArchive(ctx, job, archive)
	if err != nil {
		s.handleJobError(ctx, job, message, err)
		return
	}

	if err = s.sendShareJobEmail(ctx, job, target, accessiblePack, archive, seeker); err != nil {
		s.handleJobError(ctx, job, "email delivery failed after retries", err)
		return
	}
	if err := s.completeJob(job); err != nil {
		slog.ErrorContext(ctx, "persist sent pack share job", "task_id", job.ID, "err", err)
	}
}

func (s *ShareService) loadShareJobTargets(
	ctx context.Context,
	job *shareJobRecord,
) (*student.Student, *Pack, string, error) {
	// Re-read the student and pack under the original user's authorization. This
	// means a deleted/reassigned student or changed pack ownership cannot bypass
	// the checks merely because a job was queued earlier.
	target, err := s.students.Get(ctx, job.StudentID)
	if err != nil {
		return nil, nil, "student is no longer available", err
	}
	if target == nil {
		return nil, nil, "student is no longer available", errors.New("empty student share target")
	}

	accessiblePack, err := s.packs.Get(ctx, job.PackID)
	if err != nil {
		return nil, nil, "pack is no longer available", err
	}
	if accessiblePack == nil {
		return nil, nil, "pack is no longer available", errors.New("empty pack share source")
	}
	if accessiblePack.OwnerID != job.OwnerID {
		return nil, nil, "pack is no longer owned by the sender", apperr.ErrForbidden
	}
	return target, accessiblePack, "", nil
}

func (s *ShareService) exportShareJobArchive(
	ctx context.Context,
	job *shareJobRecord,
) (*ExportArchive, string, error) {
	// N5 compatibility requires the Linka Looks 3.0 representation. Export is
	// deliberately inside the worker so POST /share returns before archive build.
	archive, err := s.content.Export(ctx, job.PackID, linka.FormatLooks3)
	if err != nil {
		return nil, "archive export failed", err
	}
	if archive == nil || archive.Stream == nil {
		return nil, "archive export failed", errors.New("empty archive")
	}
	return archive, "", nil
}

func (s *ShareService) closeShareArchive(ctx context.Context, job *shareJobRecord, archive *ExportArchive) {
	if err := archive.Stream.Close(); err != nil {
		slog.WarnContext(ctx, "close shared pack archive", "task_id", job.ID, "pack_id", job.PackID, "err", err)
	}
}

func (s *ShareService) prepareShareArchive(
	ctx context.Context,
	job *shareJobRecord,
	archive *ExportArchive,
) (io.ReadSeeker, string, error) {
	if archive.Size > s.cfg.MaxAttachmentBytes {
		return nil, "archive exceeds email attachment limit; use direct export", fmt.Errorf(
			"archive size %d exceeds %d",
			archive.Size,
			s.cfg.MaxAttachmentBytes,
		)
	}
	if s.quota != nil {
		allowed, err := s.quota.ReserveBytes(ctx, job.OwnerID, archive.Size)
		if err != nil {
			return nil, "delivery quota check failed", err
		}
		if !allowed {
			return nil, "daily pack share volume limit exceeded", errors.New("daily byte quota exceeded")
		}
	}

	seeker, ok := archive.Stream.(io.ReadSeeker)
	if !ok {
		return nil, "archive is not retryable", errors.New("archive stream does not implement io.ReadSeeker")
	}
	return seeker, "", nil
}

func (s *ShareService) sendShareJobEmail(
	ctx context.Context,
	job *shareJobRecord,
	target *student.Student,
	accessiblePack *Pack,
	archive *ExportArchive,
	seeker io.ReadSeeker,
) error {
	var sendErr error
	for attempt := 1; attempt <= s.cfg.SendRetries; attempt++ {
		if _, sendErr = seeker.Seek(0, io.SeekStart); sendErr != nil {
			return sendErr
		}

		mailCtx, mailCancel := context.WithTimeout(ctx, s.cfg.SendTimeout)
		sendErr = s.mailer.Send(mailCtx, target.Email, mailer.PackShare, mailer.EmailData{
			Username:  target.Name,
			PackTitle: accessiblePack.Title,
			Attachments: []mailer.Attachment{{
				Filename:    archive.Filename,
				ContentType: "application/vnd.linka+zip",
				Reader:      archive.Stream,
			}},
		})
		mailCancel()
		if sendErr == nil {
			slog.InfoContext(ctx, "pack share email sent",
				"task_id", job.ID,
				"pack_id", job.PackID,
				"student_id", job.StudentID,
				"attempt", attempt,
			)
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if attempt < s.cfg.SendRetries {
			if err := waitShareRetry(ctx, s.cfg.RetryBackoff*time.Duration(attempt)); err != nil {
				return err
			}
		}
	}
	return sendErr
}

func waitShareRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (s *ShareService) handleJobError(ctx context.Context, job *shareJobRecord, message string, err error) {
	if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		s.requeueInterrupted(job, err)
		return
	}
	s.failJob(job, message)
	slog.ErrorContext(ctx, "pack share job failed",
		"task_id", job.ID,
		"pack_id", job.PackID,
		"student_id", job.StudentID,
		"err", err,
	)
}

func (s *ShareService) completeJob(job *shareJobRecord) error {
	ctx, cancel := context.WithTimeout(context.Background(), shareOutboxStoreTimeout)
	defer cancel()
	return s.jobs.CompleteShareJob(ctx, job.ID, job.LeaseToken)
}

func (s *ShareService) failJob(job *shareJobRecord, message string) {
	ctx, cancel := context.WithTimeout(context.Background(), shareOutboxStoreTimeout)
	defer cancel()
	if err := s.jobs.FailShareJob(ctx, job.ID, job.LeaseToken, message); err != nil {
		slog.Error("persist failed pack share job", "task_id", job.ID, "message", message, "err", err)
	}
}

func (s *ShareService) requeueInterrupted(job *shareJobRecord, cause error) {
	ctx, cancel := context.WithTimeout(context.Background(), shareOutboxStoreTimeout)
	defer cancel()
	if err := s.jobs.RequeueShareJob(
		ctx,
		job.ID,
		job.LeaseToken,
		"delivery interrupted; retrying",
		shareInterruptedRetryWait,
	); err != nil {
		slog.Error("requeue interrupted pack share job", "task_id", job.ID, "cause", cause, "err", err)
		return
	}
	slog.Info("pack share job requeued", "task_id", job.ID, "cause", cause)
}

func (s *ShareService) GetTask(ctx context.Context, taskID uuid.UUID) (*ShareTask, error) {
	if taskID == uuid.Nil {
		return nil, apperr.ErrBadRequest.WithMessage("task id must be a valid UUID")
	}
	userID, err := authctx.UserIDFromCtx(ctx)
	if err != nil {
		return nil, err
	}
	job, err := s.jobs.GetShareJob(ctx, taskID)
	if errors.Is(err, errShareJobNotFound) {
		return nil, apperr.ErrNotFound.WithMessage("pack share task not found")
	}
	if err != nil {
		return nil, apperr.ErrServiceUnavailable.
			WithError(err).
			WithMessage("pack share outbox is unavailable")
	}
	if job.OwnerID != userID {
		return nil, apperr.ErrNotFound.WithMessage("pack share task not found")
	}
	if isTerminalShareStatus(job.Status) && job.UpdatedAt.Before(time.Now().UTC().Add(-shareTaskTTL)) {
		return nil, apperr.ErrNotFound.WithMessage("pack share task not found")
	}
	task := shareTaskFromJob(job)
	return &task, nil
}

func shareTaskFromJob(job *shareJobRecord) ShareTask {
	return ShareTask{
		ID:        job.ID,
		Status:    job.Status,
		Message:   job.Message,
		CreatedAt: job.CreatedAt,
		UpdatedAt: job.UpdatedAt,
	}
}

func isTerminalShareStatus(status ShareTaskStatus) bool {
	return status == ShareTaskSent || status == ShareTaskFailed
}

// Shutdown stops accepting new work and stops claiming queued jobs. Workers
// already processing a job are given the caller's drain budget. If it expires,
// their context is cancelled and they requeue the leased job in the durable
// outbox; an ungraceful process death is recovered by lease expiry instead.
func (s *ShareService) Shutdown(ctx context.Context) error {
	s.accepting.Store(false)
	s.stopping.Store(true)
	s.stopOnce.Do(func() { close(s.stopClaim) })

	done := make(chan struct{})
	go func() {
		s.workersWG.Wait()
		close(done)
	}()

	select {
	case <-done:
		s.cancel()
		return nil
	case <-ctx.Done():
		s.cancel()
		select {
		case <-done:
			return ctx.Err()
		case <-time.After(shareOutboxStoreTimeout):
			// The DB lease still guarantees recovery if a dependency ignores
			// cancellation and the process is terminated after this point.
			return ctx.Err()
		}
	}
}
