package httpapi

import (
	"net/http"

	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
)

// Mux is the subset of *http.ServeMux used to register routes. Narrowing the
// dependency lets tests record the full pattern set without a real server.
type Mux interface {
	Handle(pattern string, handler http.Handler)
}

// Middleware is an HTTP middleware, re-exported for route registration signatures.
type Middleware = middleware.Middleware
