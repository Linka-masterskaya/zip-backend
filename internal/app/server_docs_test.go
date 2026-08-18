package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRegisterDocsRoutesRespectsFlag(t *testing.T) {
	tests := []struct {
		name       string
		enabled    bool
		wantStatus int
	}{
		{name: "disabled", enabled: false, wantStatus: http.StatusNotFound},
		{name: "enabled", enabled: true, wantStatus: http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := http.NewServeMux()
			registerDocsRoutes(mux, tt.enabled)

			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/docs", nil)
			res := httptest.NewRecorder()
			mux.ServeHTTP(res, req)

			if res.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", res.Code, tt.wantStatus)
			}
		})
	}
}
