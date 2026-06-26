package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
)

func TestErrorMiddleware_AppError(t *testing.T) {
	handler := AppHandler(func(_ http.ResponseWriter, _ *http.Request) error {
		return apperr.ErrNotFound.WithMessage("pack not found")
	})

	mw := RequestIDMiddleware(ErrorMiddleware(handler))

	req, err := http.NewRequestWithContext(context.Background(), "GET", "/test", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("X-Request-Id", "abc-123")
	rec := httptest.NewRecorder()

	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", rec.Code)
	}

	var resp JSONErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Error.Code != "NOT_FOUND" {
		t.Errorf("expected code NOT_FOUND, got %s", resp.Error.Code)
	}
	if resp.Error.Message != "pack not found" {
		t.Errorf("expected message 'pack not found', got %s", resp.Error.Message)
	}
	if resp.Error.RequestID != "abc-123" {
		t.Errorf("expected request_id 'abc-123', got %s", resp.Error.RequestID)
	}
}

func TestRecoveryMiddleware(t *testing.T) {
	handler := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("something went critically wrong")
	})

	mw := RecoveryMiddleware(handler)

	req, err := http.NewRequestWithContext(context.Background(), "GET", "/test", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	rec := httptest.NewRecorder()

	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", rec.Code)
	}

	var resp JSONErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Error.Code != "INTERNAL" {
		t.Errorf("expected code INTERNAL, got %s", resp.Error.Code)
	}

	if resp.Error.Message != "internal server error" {
		t.Errorf("expected default message, got %s", resp.Error.Message)
	}
}
