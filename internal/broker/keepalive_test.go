package broker

import (
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"
)

type fakeMsg struct {
	jetstream.Msg // остальные методы не реализованы: вызов упадёт паникой

	mu sync.Mutex
	n  int
}

func (m *fakeMsg) InProgress() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.n++
	return nil
}

func (m *fakeMsg) calls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.n
}

func TestKeepAliveNoopOnZeroInterval(t *testing.T) {
	stop := keepAlive(nil, 0)
	require.NotPanics(t, stop)
}

func TestKeepAliveStopsGoroutine(t *testing.T) {
	msg := &fakeMsg{}

	stop := keepAlive(msg, 20*time.Millisecond)
	time.Sleep(70 * time.Millisecond)
	stop()

	got := msg.calls()
	require.Positive(t, got, "keepAlive did not ping at all")

	time.Sleep(70 * time.Millisecond)
	require.Equal(t, got, msg.calls(), "keepAlive kept pinging after stop")
}
