package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRegisterRoutes(t *testing.T) {
	mux := http.NewServeMux()
	RegisterRoutes(mux)

	tests := []struct {
		path        string
		contentType string
		contains    string
	}{
		{docsPath, "text/html", "SwaggerUIBundle"},
		{openAPIPath, "application/yaml", "openapi: 3.1.1"},
		{assetsPath + "swagger-ui.css", "text/css", ".swagger-ui"},
		{assetsPath + "swagger-ui-bundle.js", "text/javascript", "SwaggerUIBundle"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, tt.path, nil)
			res := httptest.NewRecorder()
			mux.ServeHTTP(res, req)
			if res.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", res.Code)
			}
			if got := res.Header().Get("Content-Type"); !strings.Contains(got, tt.contentType) {
				t.Fatalf("Content-Type = %q, want %q", got, tt.contentType)
			}
			if !strings.Contains(res.Body.String(), tt.contains) {
				t.Fatalf("response does not contain %q", tt.contains)
			}
		})
	}
}

func TestUnknownSwaggerAssetIsNotFound(t *testing.T) {
	mux := http.NewServeMux()
	RegisterRoutes(mux)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, assetsPath+"unknown.js", nil)
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", res.Code)
	}
}
