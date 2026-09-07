package testutil

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// QueryGate pauses a traced connection after its first successful SELECT.
type QueryGate struct {
	queryCompleted chan struct{}
	continueQuery  chan struct{}
	reachedOnce    sync.Once
	releaseOnce    sync.Once
}

func NewQueryGate() *QueryGate {
	return &QueryGate{
		queryCompleted: make(chan struct{}),
		continueQuery:  make(chan struct{}),
	}
}

func (*QueryGate) TraceQueryStart(
	ctx context.Context,
	_ *pgx.Conn,
	_ pgx.TraceQueryStartData,
) context.Context {
	return ctx
}

func (g *QueryGate) TraceQueryEnd(
	ctx context.Context,
	_ *pgx.Conn,
	data pgx.TraceQueryEndData,
) {
	if data.Err != nil || !data.CommandTag.Select() {
		return
	}

	g.reachedOnce.Do(func() {
		close(g.queryCompleted)
		select {
		case <-g.continueQuery:
		case <-ctx.Done():
		}
	})
}

func (g *QueryGate) Wait(t testing.TB, timeout time.Duration) {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-g.queryCompleted:
	case <-timer.C:
		t.Fatalf("query gate was not reached within %s", timeout)
	}
}

func (g *QueryGate) Release() {
	g.releaseOnce.Do(func() {
		close(g.continueQuery)
	})
}
