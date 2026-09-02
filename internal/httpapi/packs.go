package httpapi

import (
	"net/http"

	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
	"github.com/Linka-masterskaya/zip-backend/internal/pack"
)

// PackHandlers contains the handlers exposed by the packs API.
type PackHandlers struct {
	Pack     *pack.Handler
	Share    *pack.ShareHandler
	Content  *pack.ContentHandler
	Favorite *pack.FavoriteHandler
}

// RegisterPackRoutes registers pack CRUD, publication, config, import/export,
// student assignment, and version history routes.
func RegisterPackRoutes(
	mux Mux,
	authMW *middleware.AuthMW,
	rateLimit Middleware,
	handlers PackHandlers,
) {
	protected := func(next middleware.AppHandler) http.Handler {
		return rateLimit(middleware.ErrorMiddleware(authMW.AuthMiddleware(next)))
	}

	mux.Handle("POST /api/v1/packs", protected(handlers.Pack.CreatePack))
	mux.Handle("POST /api/v1/packs/{id}/duplicate", protected(handlers.Pack.DuplicatePack))
	mux.Handle("POST /api/v1/packs/{id}/share", protected(handlers.Share.SharePack))
	mux.Handle("GET /api/v1/pack-share-tasks/{id}", protected(handlers.Share.GetShareTask))
	mux.Handle("GET /api/v1/packs/{id}", protected(handlers.Pack.GetPack))
	mux.Handle("GET /api/v1/packs", protected(handlers.Pack.ListPacks))
	mux.Handle("PATCH /api/v1/packs/{id}", protected(handlers.Pack.UpdatePack))
	mux.Handle("DELETE /api/v1/packs/{id}", protected(handlers.Pack.DeletePack))
	mux.Handle("POST /api/v1/packs/{id}/move", protected(handlers.Pack.MovePack))
	mux.Handle("POST /api/v1/packs/{id}/publication", protected(handlers.Pack.PublishPack))
	mux.Handle("DELETE /api/v1/packs/{id}/publication", protected(handlers.Pack.UnpublishPack))
	mux.Handle("PUT /api/v1/packs/{id}/config", protected(handlers.Content.SaveConfig))
	mux.Handle("GET /api/v1/packs/{id}/export", protected(handlers.Content.Export))
	mux.Handle("GET /api/v1/adaptations/{id}/export", protected(handlers.Content.ExportAdaptation))
	mux.Handle("POST /api/v1/packs/import", protected(handlers.Content.Import))
	mux.Handle("POST /api/v1/packs/{id}/students", protected(handlers.Content.Assign))
	mux.Handle("DELETE /api/v1/packs/{id}/students/{student_id}", protected(handlers.Content.Unassign))
	mux.Handle("GET /api/v1/packs/{id}/adaptations", protected(handlers.Content.ListAdaptations))
	mux.Handle("GET /api/v1/adaptations/{id}", protected(handlers.Content.GetAdaptation))
	mux.Handle("PUT /api/v1/adaptations/{id}/config", protected(handlers.Content.UpdateAdaptationConfig))
	mux.Handle("PUT /api/v1/packs/{id}/favorite", protected(handlers.Favorite.PutFavorite))
	mux.Handle("DELETE /api/v1/packs/{id}/favorite", protected(handlers.Favorite.DeleteFavorite))
	mux.Handle("GET /api/v1/favorites/packs", protected(handlers.Favorite.ListFavorites))
}
