package pack

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/authctx"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServiceCreateBuildsEmptyLinkaConfig(t *testing.T) {
	userID := uuid.New()
	folderID := uuid.New()
	repo := &fakePackRepository{}
	repo.createFn = func(_ context.Context, gotUserID uuid.UUID, input CreateInput) (*Pack, error) {
		assert.Equal(t, userID, gotUserID)
		assert.Equal(t, folderID, input.FolderID)
		assert.Equal(t, "New pack", input.Title)
		assert.JSONEq(t, `{"metadata":{"version":"2.0"},"settings":{"columns":1,"rows":1},"blocks":[]}`, string(input.Config))
		return &Pack{ID: uuid.New(), Config: input.Config}, nil
	}

	result, err := NewService(repo, nil).Create(packContext(userID), "  New pack  ", folderID)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, json.Valid(result.Config))
}

func TestServiceGetListDeleteAndMoveDelegateUserScope(t *testing.T) {
	userID := uuid.New()
	packID := uuid.New()
	folderID := uuid.New()
	repo := &fakePackRepository{}
	repo.getFn = func(_ context.Context, gotUserID, gotPackID uuid.UUID) (*Pack, error) {
		assert.Equal(t, userID, gotUserID)
		assert.Equal(t, packID, gotPackID)
		return &Pack{ID: packID}, nil
	}
	repo.listFn = func(_ context.Context, gotUserID, gotFolderID uuid.UUID) ([]*Pack, error) {
		assert.Equal(t, userID, gotUserID)
		assert.Equal(t, folderID, gotFolderID)
		return []*Pack{{ID: packID}}, nil
	}
	repo.deleteFn = func(_ context.Context, gotUserID, gotPackID uuid.UUID) error {
		assert.Equal(t, userID, gotUserID)
		assert.Equal(t, packID, gotPackID)
		return nil
	}
	repo.moveFn = func(_ context.Context, gotUserID, gotPackID, gotFolderID uuid.UUID) (*Pack, error) {
		assert.Equal(t, userID, gotUserID)
		assert.Equal(t, packID, gotPackID)
		assert.Equal(t, folderID, gotFolderID)
		return &Pack{ID: packID, FolderID: folderID}, nil
	}

	service := NewService(repo, nil)
	ctx := packContext(userID)
	_, err := service.Get(ctx, packID)
	require.NoError(t, err)
	listed, err := service.List(ctx, folderID)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.NoError(t, service.Delete(ctx, packID))
	moved, err := service.Move(ctx, packID, folderID)
	require.NoError(t, err)
	assert.Equal(t, folderID, moved.FolderID)
}

func TestServiceUpdateAllowsClearingNullableMetadata(t *testing.T) {
	userID := uuid.New()
	packID := uuid.New()
	repo := &fakePackRepository{}
	repo.updateFn = func(_ context.Context, gotUserID, gotPackID uuid.UUID, input UpdateInput) (*Pack, error) {
		assert.Equal(t, userID, gotUserID)
		assert.Equal(t, packID, gotPackID)
		require.NotNil(t, input.FilterMetadata)
		assert.True(t, input.FilterMetadata.AgeMin.Set)
		assert.Nil(t, input.FilterMetadata.AgeMin.Value)
		assert.True(t, input.FilterMetadata.Difficulty.Set)
		assert.Nil(t, input.FilterMetadata.Difficulty.Value)
		assert.True(t, input.Notes.Set)
		assert.Nil(t, input.Notes.Value)
		return &Pack{ID: packID}, nil
	}
	input := UpdateInput{
		FilterMetadata: &FilterMetadataPatch{
			AgeMin:     NullablePatch[int]{Set: true},
			Difficulty: NullablePatch[string]{Set: true},
		},
		Notes: NullablePatch[string]{Set: true},
	}

	_, err := NewService(repo, nil).Update(packContext(userID), packID, input)

	require.NoError(t, err)
}

func TestServiceUpdateRejectsInvalidMetadata(t *testing.T) {
	ageMin := 8
	ageMax := 5
	invalidDifficulty := "expert"
	tests := []struct {
		name     string
		metadata *FilterMetadataPatch
	}{
		{
			name: "age range",
			metadata: &FilterMetadataPatch{
				AgeMin: NullablePatch[int]{Set: true, Value: &ageMin},
				AgeMax: NullablePatch[int]{Set: true, Value: &ageMax},
			},
		},
		{
			name: "difficulty",
			metadata: &FilterMetadataPatch{
				Difficulty: NullablePatch[string]{Set: true, Value: &invalidDifficulty},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewService(&fakePackRepository{}, nil).Update(
				packContext(uuid.New()), uuid.New(), UpdateInput{FilterMetadata: test.metadata},
			)
			assertAppErrorStatus(t, err, apperr.ErrBadRequest.HTTPStatus)
		})
	}
}

func TestServiceMapsRepositoryErrors(t *testing.T) {
	tests := []struct {
		name       string
		repoErr    error
		httpStatus int
	}{
		{name: "not found", repoErr: ErrPackNotFound, httpStatus: apperr.ErrNotFound.HTTPStatus},
		{name: "folder", repoErr: ErrFolderNotAllowed, httpStatus: apperr.ErrForbidden.HTTPStatus},
		{name: "metadata", repoErr: ErrInvalidPackMetadata, httpStatus: apperr.ErrBadRequest.HTTPStatus},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := &fakePackRepository{getFn: func(context.Context, uuid.UUID, uuid.UUID) (*Pack, error) {
				return nil, test.repoErr
			}}
			_, err := NewService(repo, nil).Get(packContext(uuid.New()), uuid.New())
			assertAppErrorStatus(t, err, test.httpStatus)
		})
	}
}

func TestServiceRequiresAuthenticatedUser(t *testing.T) {
	_, err := NewService(&fakePackRepository{}, nil).Get(context.Background(), uuid.New())
	assert.ErrorIs(t, err, apperr.ErrUnauthorized)
}

func packContext(userID uuid.UUID) context.Context {
	return authctx.SetUserIDToCtx(context.Background(), userID)
}

func assertAppErrorStatus(t *testing.T, err error, status int) {
	t.Helper()
	var appErr *apperr.AppError
	require.Error(t, err)
	require.True(t, errors.As(err, &appErr))
	assert.Equal(t, status, appErr.HTTPStatus)
}

type fakePackRepository struct {
	createFn func(context.Context, uuid.UUID, CreateInput) (*Pack, error)
	getFn    func(context.Context, uuid.UUID, uuid.UUID) (*Pack, error)
	listFn   func(context.Context, uuid.UUID, uuid.UUID) ([]*Pack, error)
	updateFn func(context.Context, uuid.UUID, uuid.UUID, UpdateInput) (*Pack, error)
	deleteFn func(context.Context, uuid.UUID, uuid.UUID) error
	moveFn   func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (*Pack, error)
}

func (f *fakePackRepository) Create(ctx context.Context, userID uuid.UUID, input CreateInput) (*Pack, error) {
	if f.createFn != nil {
		return f.createFn(ctx, userID, input)
	}
	return &Pack{}, nil
}

func (f *fakePackRepository) Get(ctx context.Context, userID, packID uuid.UUID) (*Pack, error) {
	if f.getFn != nil {
		return f.getFn(ctx, userID, packID)
	}
	return &Pack{}, nil
}

func (f *fakePackRepository) List(ctx context.Context, userID, folderID uuid.UUID) ([]*Pack, error) {
	if f.listFn != nil {
		return f.listFn(ctx, userID, folderID)
	}
	return []*Pack{}, nil
}

func (f *fakePackRepository) Update(ctx context.Context, userID, packID uuid.UUID, input UpdateInput) (*Pack, error) {
	if f.updateFn != nil {
		return f.updateFn(ctx, userID, packID, input)
	}
	return &Pack{}, nil
}

func (f *fakePackRepository) Delete(ctx context.Context, userID, packID uuid.UUID) error {
	if f.deleteFn != nil {
		return f.deleteFn(ctx, userID, packID)
	}
	return nil
}

func (f *fakePackRepository) Move(ctx context.Context, userID, packID, folderID uuid.UUID) (*Pack, error) {
	if f.moveFn != nil {
		return f.moveFn(ctx, userID, packID, folderID)
	}
	return &Pack{}, nil
}
