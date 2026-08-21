package auth

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type deleterStub struct {
	calls   atomic.Int64
	cutoffs chan time.Time
	err     error
}

func (s *deleterStub) DeleteStaleUnverifiedUsers(
	_ context.Context,
	cutoff time.Time,
) (int64, error) {
	s.calls.Add(1)
	select {
	case s.cutoffs <- cutoff:
	default:
	}
	return 1, s.err
}

func TestRegistrationCleanerDisabledWithoutRetention(t *testing.T) {
	stub := &deleterStub{cutoffs: make(chan time.Time, 1)}

	NewRegistrationCleaner(stub, 0, time.Hour).Run(context.Background())
	NewRegistrationCleaner(stub, time.Hour, 0).Run(context.Background())

	assert.Zero(t, stub.calls.Load(), "выключенный уборщик не должен ходить в базу")
}

func TestRegistrationCleanerRunsImmediatelyAndStopsOnContext(t *testing.T) {
	stub := &deleterStub{cutoffs: make(chan time.Time, 1)}
	retention := 48 * time.Hour
	cleaner := NewRegistrationCleaner(stub, retention, time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		cleaner.Run(ctx)
	}()

	// Первый проход не ждёт тика: иначе освободившийся адрес остаётся занятым
	// ещё час после старта.
	var cutoff time.Time
	select {
	case cutoff = <-stub.cutoffs:
	case <-time.After(2 * time.Second):
		t.Fatal("уборщик не сделал первый проход")
	}
	assert.WithinDuration(t, time.Now().Add(-retention), cutoff, time.Minute)

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("уборщик не остановился по контексту")
	}
}

func TestRegistrationCleanerSurvivesRepositoryError(t *testing.T) {
	stub := &deleterStub{cutoffs: make(chan time.Time, 1), err: errors.New("база недоступна")}
	cleaner := NewRegistrationCleaner(stub, time.Hour, 20*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	cleaner.Run(ctx)

	// Ошибка одного прохода не должна ронять цикл.
	require.Greater(t, stub.calls.Load(), int64(1))
}
