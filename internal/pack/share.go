package pack

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/mailer"
	"github.com/Linka-masterskaya/zip-backend/internal/student"
	"github.com/Linka-masterskaya/zip-backend/pkg/linka"
	"github.com/google/uuid"
)

const shareMailTimeout = 30 * time.Second

type ShareTargetType string

const (
	ShareTargetFolder  ShareTargetType = "folder"
	ShareTargetStudent ShareTargetType = "student"
)

// ShareInput identifies the destination selected in the share modal.
type ShareInput struct {
	TargetType ShareTargetType
	TargetID   uuid.UUID
}

// ShareResult contains either the newly duplicated pack (folder target) or an
// accepted asynchronous email delivery (student target).
type ShareResult struct {
	Pack     *Pack
	Accepted bool
}

type sharePackService interface {
	Duplicate(context.Context, uuid.UUID, DuplicateInput) (*Pack, error)
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

// ShareService composes existing domain operations instead of duplicating
// their persistence rules: folder sharing delegates to Duplicate, while
// student sharing delegates to the validated Looks-compatible export.
type ShareService struct {
	packs    sharePackService
	content  shareContentService
	students shareStudentService
	mailer   shareMailer
}

func NewShareService(
	packs sharePackService,
	content shareContentService,
	students shareStudentService,
	mailerSender shareMailer,
) *ShareService {
	return &ShareService{packs: packs, content: content, students: students, mailer: mailerSender}
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
		duplicated, err := s.packs.Duplicate(ctx, packID, DuplicateInput{FolderID: &input.TargetID})
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
	if s.students == nil || s.content == nil || s.mailer == nil {
		return nil, fmt.Errorf("pack share dependencies are not configured")
	}

	// Check ownership/access before doing the relatively expensive archive build.
	target, err := s.students.Get(ctx, studentID)
	if err != nil {
		return nil, err
	}
	if target == nil {
		return nil, fmt.Errorf("student share target is empty")
	}

	// N5 compatibility requires the Linka Looks 3.0 representation. Using the
	// default linka-2 export here can produce an archive that supported Looks
	// versions open as an empty/invalid set.
	archive, err := s.content.Export(ctx, packID, linka.FormatLooks3)
	if err != nil {
		return nil, err
	}
	if archive == nil || archive.Stream == nil {
		return nil, fmt.Errorf("pack share export returned an empty archive")
	}

	backgroundCtx := context.WithoutCancel(ctx)
	go s.sendStudentShare(backgroundCtx, packID, target, archive)
	return &ShareResult{Accepted: true}, nil
}

func (s *ShareService) sendStudentShare(
	ctx context.Context,
	packID uuid.UUID,
	target *student.Student,
	archive *ExportArchive,
) {
	if archive == nil || archive.Stream == nil || target == nil {
		slog.ErrorContext(ctx, "pack share async state is invalid", "pack_id", packID)
		return
	}
	defer func() {
		if err := archive.Stream.Close(); err != nil {
			slog.WarnContext(ctx, "close shared pack archive", "pack_id", packID, "student_id", target.ID, "err", err)
		}
	}()

	mailCtx, cancel := context.WithTimeout(ctx, shareMailTimeout)
	defer cancel()

	packTitle := strings.TrimSuffix(archive.Filename, ".linka")
	err := s.mailer.Send(mailCtx, target.Email, mailer.PackShare, mailer.EmailData{
		Username:  target.Name,
		PackTitle: packTitle,
		Attachments: []mailer.Attachment{{
			Filename:    archive.Filename,
			ContentType: "application/vnd.linka+zip",
			Reader:      archive.Stream,
		}},
	})
	if err != nil {
		// Do not log the recipient email. SMTPSender records template/status metrics;
		// this structured event provides the domain identifiers for investigation.
		slog.ErrorContext(ctx, "pack share email send failed", "pack_id", packID, "student_id", target.ID, "err", err)
		return
	}
	slog.InfoContext(ctx, "pack share email sent", "pack_id", packID, "student_id", target.ID)
}
