// Package httpapi wires feature handlers to the public HTTP API.
package httpapi

import (
	"net/http"

	"github.com/Linka-masterskaya/zip-backend/internal/folder"
	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
	"github.com/Linka-masterskaya/zip-backend/internal/pack"
	"github.com/Linka-masterskaya/zip-backend/internal/student"
)

// P1Handlers contains the handlers exposed by the P1 API.
type P1Handlers struct {
	Pack    *pack.Handler
	Folder  *folder.Handler
	Student *student.Handler
}

// RegisterP1Routes registers folders, packs, publication, and students routes.
func RegisterP1Routes(
	mux *http.ServeMux,
	authMW *middleware.AuthMW,
	rateLimit func(http.Handler) http.Handler,
	handlers P1Handlers,
) {
	protected := func(next middleware.AppHandler) http.Handler {
		return rateLimit(middleware.ErrorMiddleware(authMW.AuthMiddleware(next)))
	}

	mux.Handle("POST /api/v1/packs", protected(handlers.Pack.CreatePack))
	mux.Handle("GET /api/v1/packs/{id}", protected(handlers.Pack.GetPack))
	mux.Handle("GET /api/v1/packs", protected(handlers.Pack.ListPacks))
	mux.Handle("PATCH /api/v1/packs/{id}", protected(handlers.Pack.UpdatePack))
	mux.Handle("DELETE /api/v1/packs/{id}", protected(handlers.Pack.DeletePack))
	mux.Handle("POST /api/v1/packs/{id}/move", protected(handlers.Pack.MovePack))
	mux.Handle("POST /api/v1/packs/{id}/publication", protected(handlers.Pack.PublishPack))
	mux.Handle("DELETE /api/v1/packs/{id}/publication", protected(handlers.Pack.UnpublishPack))

	mux.Handle("POST /api/v1/folders", protected(handlers.Folder.Create))
	mux.Handle("GET /api/v1/folders", protected(handlers.Folder.List))
	mux.Handle("GET /api/v1/folders/{id}/contents", protected(handlers.Folder.Contents))
	mux.Handle("PATCH /api/v1/folders/{id}", protected(handlers.Folder.Rename))
	mux.Handle("POST /api/v1/folders/{id}/move", protected(handlers.Folder.Move))
	mux.Handle("DELETE /api/v1/folders/{id}", protected(handlers.Folder.Delete))

	mux.Handle("POST /api/v1/students", protected(handlers.Student.Create))
	mux.Handle("GET /api/v1/students", protected(handlers.Student.List))
	mux.Handle("PATCH /api/v1/students/{id}", protected(handlers.Student.Update))
	mux.Handle("DELETE /api/v1/students/{id}", protected(handlers.Student.Delete))
}
