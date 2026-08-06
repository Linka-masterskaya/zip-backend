package authctx

import (
	"context"
	"testing"
)

func TestRequestIDFromCtx(t *testing.T) {
	t.Run("returns request ID from context", func(t *testing.T) {
		const want = "abc-123"

		ctx := SetRequestIDToCtx(context.Background(), want)

		got := RequestIDFromCtx(ctx)

		if got != want {
			t.Fatalf("expected request ID %q, got %q", want, got)
		}
	})

	t.Run("returns empty string when request ID is absent", func(t *testing.T) {
		got := RequestIDFromCtx(context.Background())

		if got != "" {
			t.Fatalf("expected empty request ID, got %q", got)
		}
	})
}
