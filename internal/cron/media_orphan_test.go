package cron

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/media"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeMediaOrphanRepo struct {
	clearCount    int64
	clearErr      error
	batches       []media.OrphanBatchResult
	batchErr      error
	calls         int
	batchSizes    []int
	createdBefore []time.Time
}

func (f *fakeMediaOrphanRepo) ClearSoftDeletedStudentAvatars(context.Context) (int64, error) {
	return f.clearCount, f.clearErr
}

func (f *fakeMediaOrphanRepo) DeleteOrphanBatch(
	_ context.Context,
	batchSize int,
	createdBefore time.Time,
) (media.OrphanBatchResult, error) {
	f.calls++
	f.batchSizes = append(f.batchSizes, batchSize)
	f.createdBefore = append(f.createdBefore, createdBefore)
	if f.calls <= len(f.batches) {
		result := f.batches[f.calls-1]
		if f.calls == len(f.batches) && f.batchErr != nil {
			return result, f.batchErr
		}
		return result, nil
	}
	return media.OrphanBatchResult{}, f.batchErr
}

func TestMediaOrphanScannerScansUntilShortBatch(t *testing.T) {
	repo := &fakeMediaOrphanRepo{
		clearCount: 2,
		batches: []media.OrphanBatchResult{
			{Count: 2, Bytes: 30, Candidates: 2},
			{Count: 2, Bytes: 40, Candidates: 2},
			{Count: 1, Bytes: 50, Candidates: 1},
		},
	}
	scanner := NewMediaOrphanScanner(repo, 2, time.Minute)

	result, err := scanner.Scan(t.Context())

	require.NoError(t, err)
	assert.Equal(t, media.OrphanBatchResult{Count: 5, Bytes: 120}, result)
	assert.Equal(t, 3, repo.calls)
	assert.Equal(t, []int{2, 2, 2}, repo.batchSizes)
	require.Len(t, repo.createdBefore, 3)
	assert.Equal(t, repo.createdBefore[0], repo.createdBefore[1])
	assert.Equal(t, repo.createdBefore[1], repo.createdBefore[2],
		"all batches in one scan must use the same grace cutoff")
}

func TestMediaOrphanScannerContinuesWhenRecheckProtectsCandidate(t *testing.T) {
	repo := &fakeMediaOrphanRepo{
		batches: []media.OrphanBatchResult{
			{Count: 1, Bytes: 30, Candidates: 2},
			{Count: 1, Bytes: 40, Candidates: 1},
		},
	}
	scanner := NewMediaOrphanScanner(repo, 2, time.Minute)

	result, err := scanner.Scan(t.Context())

	require.NoError(t, err)
	assert.Equal(t, int64(2), result.Count)
	assert.Equal(t, int64(70), result.Bytes)
	assert.Equal(t, 2, repo.calls,
		"scanner must continue when a full candidate batch shrinks after the reference recheck")
}

func TestMediaOrphanScannerStopsWhenAvatarCleanupFails(t *testing.T) {
	repo := &fakeMediaOrphanRepo{clearErr: errors.New("db unavailable")}
	scanner := NewMediaOrphanScanner(repo, 100, time.Minute)

	_, err := scanner.Scan(t.Context())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "db unavailable")
	assert.Zero(t, repo.calls)
}

func TestMediaOrphanScannerReturnsPartialTotalsOnBatchFailure(t *testing.T) {
	repo := &fakeMediaOrphanRepo{
		batches: []media.OrphanBatchResult{
			{Count: 2, Bytes: 30, Candidates: 2},
			{Count: 1, Bytes: 10, Candidates: 1},
		},
		batchErr: errors.New("delete failed"),
	}
	scanner := NewMediaOrphanScanner(repo, 2, time.Minute)

	result, err := scanner.Scan(t.Context())

	require.Error(t, err)
	assert.Equal(t, media.OrphanBatchResult{Count: 3, Bytes: 40}, result)
	assert.Contains(t, err.Error(), "after 3 files/40 bytes")
}

type signalingMediaOrphanRepo struct {
	called chan struct{}
}

func (f *signalingMediaOrphanRepo) ClearSoftDeletedStudentAvatars(context.Context) (int64, error) {
	return 0, nil
}

func (f *signalingMediaOrphanRepo) DeleteOrphanBatch(context.Context, int, time.Time) (media.OrphanBatchResult, error) {
	select {
	case f.called <- struct{}{}:
	default:
	}
	return media.OrphanBatchResult{}, nil
}

func TestMediaOrphanScannerRunFiresOnSchedule(t *testing.T) {
	repo := &signalingMediaOrphanRepo{called: make(chan struct{}, 1)}
	scanner := NewMediaOrphanScanner(repo, 10, time.Minute)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		scanner.Run(ctx, time.Millisecond)
	}()

	select {
	case <-repo.called:
		cancel()
	case <-time.After(time.Second):
		cancel()
		t.Fatal("orphan scanner did not run on schedule")
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("orphan scanner did not stop after context cancellation")
	}
}
