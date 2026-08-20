package httpapi

import (
	"net/http"

	"github.com/Linka-masterskaya/zip-backend/internal/folder"
	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
)

// FolderHandlers contains the handlers exposed by the folders API.
type FolderHandlers struct {
	Folder *folder.Handler
}

// RegisterFolderRoutes registers folder create, list, contents, rename,
// move, and delete routes.
func RegisterFolderRoutes(
	mux Mux,
	authMW *middleware.AuthMW,
	rateLimit Middleware,
	handlers FolderHandlers,
) {
	protected := func(next middleware.AppHandler) http.Handler {
		return rateLimit(middleware.ErrorMiddleware(authMW.AuthMiddleware(next)))
	}

	mux.Handle("POST /api/v1/folders", protected(handlers.Folder.Create))
	mux.Handle("GET /api/v1/folders", protected(handlers.Folder.List))
	mux.Handle("GET /api/v1/sections/{section}/contents", protected(handlers.Folder.Contents))
	mux.Handle("PATCH /api/v1/folders/{id}", protected(handlers.Folder.Rename))
	mux.Handle("POST /api/v1/folders/{id}/move", protected(handlers.Folder.Move))
	mux.Handle("DELETE /api/v1/folders/{id}", protected(handlers.Folder.Delete))
}
