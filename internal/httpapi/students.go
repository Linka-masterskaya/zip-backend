package httpapi

import (
	"net/http"

	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
	"github.com/Linka-masterskaya/zip-backend/internal/student"
)

// StudentHandlers contains the handlers exposed by the students API.
type StudentHandlers struct {
	Student *student.Handler
}

// RegisterStudentRoutes registers student create, list, update, and delete routes.
func RegisterStudentRoutes(
	mux Mux,
	authMW *middleware.AuthMW,
	rateLimit Middleware,
	handlers StudentHandlers,
) {
	protected := func(next middleware.AppHandler) http.Handler {
		return rateLimit(middleware.ErrorMiddleware(authMW.AuthMiddleware(next)))
	}

	mux.Handle("POST /api/v1/students", protected(handlers.Student.Create))
	mux.Handle("GET /api/v1/students", protected(handlers.Student.List))
	mux.Handle("PATCH /api/v1/students/{id}", protected(handlers.Student.Update))
	mux.Handle("DELETE /api/v1/students/{id}", protected(handlers.Student.Delete))
}
