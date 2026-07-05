package pack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
)

func TestHandler_ErrorMiddleware_Internal(t *testing.T) {
	handler := middleware.AppHandler(func(w http.ResponseWriter, r *http.Request) error {
		return http.ErrNotSupported
	})

	mw := middleware.ErrorMiddleware(handler)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "/packs/123", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	rec := httptest.NewRecorder()

	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected status %d, got %d", http.StatusInternalServerError, rec.Code)
	}

	var errorResponse map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &errorResponse); err != nil {
		t.Fatalf("failed to unmarshal error response: %v", err)
	}
}

func TestHandler_ErrorMiddleware_Panic(t *testing.T) {
	handler := middleware.AppHandler(func(w http.ResponseWriter, r *http.Request) error {
		panic("критическая ошибка внутри хендлера")
	})

	mw := middleware.RecoveryMiddleware(middleware.ErrorMiddleware(handler))

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "/packs", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	rec := httptest.NewRecorder()

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("паника не была перехвачена мидлварью: %v", r)
		}
	}()

	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("ожидался статус 500, получено %d", rec.Code)
	}
}
