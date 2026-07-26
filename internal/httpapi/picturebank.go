package httpapi

import (
	"net/http"

	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
	"github.com/Linka-masterskaya/zip-backend/internal/picturebank"
)

func RegisterPictureBankRoutes(
	mux *http.ServeMux,
	authMW *middleware.AuthMW,
	rateLimit func(http.Handler) http.Handler,
	handler *picturebank.Handler,
) {
	protected := func(next middleware.AppHandler) http.Handler {
		return rateLimit(middleware.ErrorMiddleware(authMW.AuthMiddleware(next)))
	}
	mux.Handle("GET /api/v1/pictures/categories", protected(handler.Categories))
	mux.Handle("GET /api/v1/pictures/search", protected(handler.Search))
	mux.Handle("GET /api/v1/pictures/{id}/content", protected(handler.Image))
	mux.Handle("POST /api/v1/pictures/{id}/import", protected(handler.Import))
}
