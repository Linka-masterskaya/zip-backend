package app

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCloserRunsInReverseOrder(t *testing.T) {
	var order []string
	var c Closer

	c.Add("first", func(context.Context) error { order = append(order, "first"); return nil })
	c.Add("second", func(context.Context) error { order = append(order, "second"); return nil })
	c.Add("third", func(context.Context) error { order = append(order, "third"); return nil })

	if err := c.Close(context.Background()); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}

	want := []string{"third", "second", "first"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}

func TestCloserContinuesAfterError(t *testing.T) {
	boom := errors.New("boom")
	var closed []string
	var c Closer

	c.Add("bottom", func(context.Context) error { closed = append(closed, "bottom"); return nil })
	c.Add("broken", func(context.Context) error { return boom })
	c.Add("top", func(context.Context) error { closed = append(closed, "top"); return nil })

	err := c.Close(context.Background())
	if !errors.Is(err, boom) {
		t.Fatalf("Close() = %v, want %v", err, boom)
	}
	if len(closed) != 2 || closed[0] != "top" || closed[1] != "bottom" {
		t.Fatalf("closed = %v, want [top bottom]", closed)
	}
}

func TestCloserStopsOnContextCancel(t *testing.T) {
	var closed []string
	var c Closer

	c.Add("never", func(context.Context) error { closed = append(closed, "never"); return nil })
	c.Add("slow", func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	if err := c.Close(ctx); err == nil {
		t.Fatal("Close() = nil, want context deadline error")
	}
	if len(closed) != 0 {
		t.Fatalf("closed = %v, want empty: cancelled context must stop the stack", closed)
	}
}

func TestCloserEmpty(t *testing.T) {
	var c Closer
	if err := c.Close(context.Background()); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}
	if c.Len() != 0 {
		t.Fatalf("Len() = %d, want 0", c.Len())
	}
}
