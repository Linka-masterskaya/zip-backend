package reqctx

import (
	"context"
	"testing"
)

func TestRequestIDRoundtrip(t *testing.T) {
	ctx := PutRequestID(context.Background(), "abc-123")
	got := GetRequestID(ctx)
	if got != "abc-123" {
		t.Errorf("want abc-123, got %s", got)
	}
}

func TestGetRequestID_Missing(t *testing.T) {
	got := GetRequestID(context.Background())
	if got != "" {
		t.Errorf("expected empty string, got %s", got)
	}
}
