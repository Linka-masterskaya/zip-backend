package pack

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/mailer"
	"github.com/Linka-masterskaya/zip-backend/internal/student"
	"github.com/Linka-masterskaya/zip-backend/pkg/linka"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShareServiceFolderDelegatesToDuplicate(t *testing.T) {
	packID, folderID := uuid.New(), uuid.New()
	copied := &Pack{ID: uuid.New(), FolderID: folderID, Status: "draft"}
	packs := &sharePackFake{duplicateFn: func(
		_ context.Context, gotPackID uuid.UUID, input DuplicateInput,
	) (*Pack, error) {
		assert.Equal(t, packID, gotPackID)
		require.NotNil(t, input.FolderID)
		assert.Equal(t, folderID, *input.FolderID)
		return copied, nil
	}}

	result, err := NewShareService(packs, nil, nil, nil).Share(context.Background(), packID, ShareInput{
		TargetType: ShareTargetFolder,
		TargetID:   folderID,
	})

	require.NoError(t, err)
	assert.Same(t, copied, result.Pack)
	assert.False(t, result.Accepted)
}

func TestShareServiceStudentBuildsLooksArchiveAndSendsAsync(t *testing.T) {
	packID, studentID := uuid.New(), uuid.New()
	content := &shareContentFake{archive: &ExportArchive{
		Stream:   io.NopCloser(bytes.NewReader([]byte("linka archive"))),
		Filename: "Speech pack.linka",
		Size:     13,
	}}
	students := &shareStudentFake{student: &student.Student{
		ID: studentID, Email: "student@example.com", Name: "Анна",
	}}
	calls := make(chan shareMailCall, 1)
	mailSender := &shareMailerFake{calls: calls}

	result, err := NewShareService(&sharePackFake{}, content, students, mailSender).Share(
		context.Background(), packID, ShareInput{TargetType: ShareTargetStudent, TargetID: studentID},
	)

	require.NoError(t, err)
	assert.True(t, result.Accepted)
	assert.Nil(t, result.Pack)
	assert.Equal(t, linka.FormatLooks3, content.format)
	assert.Equal(t, packID, content.packID)
	assert.Equal(t, studentID, students.studentID)

	select {
	case call := <-calls:
		assert.Equal(t, "student@example.com", call.to)
		assert.Equal(t, mailer.PackShare, call.template)
		assert.Equal(t, "Анна", call.data.Username)
		assert.Equal(t, "Speech pack", call.data.PackTitle)
		require.Len(t, call.data.Attachments, 1)
		assert.Equal(t, "Speech pack.linka", call.data.Attachments[0].Filename)
		assert.Equal(t, "application/vnd.linka+zip", call.data.Attachments[0].ContentType)
		assert.Equal(t, []byte("linka archive"), call.attachment)
	case <-time.After(2 * time.Second):
		t.Fatal("share email was not dispatched")
	}
}

func TestShareServiceStudentValidationStopsBeforeExport(t *testing.T) {
	content := &shareContentFake{}
	students := &shareStudentFake{err: apperr.ErrNotFound}

	_, err := NewShareService(&sharePackFake{}, content, students, &shareMailerFake{}).Share(
		context.Background(), uuid.New(), ShareInput{TargetType: ShareTargetStudent, TargetID: uuid.New()},
	)

	assert.Error(t, err)
	assert.Equal(t, uuid.Nil, content.packID)
}

func TestShareServiceRejectsInvalidTarget(t *testing.T) {
	_, err := NewShareService(&sharePackFake{}, nil, nil, nil).Share(
		context.Background(), uuid.New(), ShareInput{TargetType: "unknown", TargetID: uuid.New()},
	)
	assertAppErrorStatus(t, err, 400)
}

type sharePackFake struct {
	duplicateFn func(context.Context, uuid.UUID, DuplicateInput) (*Pack, error)
}

func (f *sharePackFake) Duplicate(ctx context.Context, packID uuid.UUID, input DuplicateInput) (*Pack, error) {
	if f.duplicateFn != nil {
		return f.duplicateFn(ctx, packID, input)
	}
	return &Pack{}, nil
}

type shareContentFake struct {
	packID  uuid.UUID
	format  linka.Format
	archive *ExportArchive
	err     error
}

func (f *shareContentFake) Export(_ context.Context, packID uuid.UUID, format linka.Format) (*ExportArchive, error) {
	f.packID = packID
	f.format = format
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
	calls chan<- shareMailCall
	err   error
}

func (f *shareMailerFake) Send(_ context.Context, to string, tmpl mailer.Template, data mailer.EmailData) error {
	call := shareMailCall{to: to, template: tmpl, data: data}
	if len(data.Attachments) > 0 && data.Attachments[0].Reader != nil {
		call.attachment, _ = io.ReadAll(data.Attachments[0].Reader)
	}
	if f.calls != nil {
		f.calls <- call
	}
	return f.err
}
