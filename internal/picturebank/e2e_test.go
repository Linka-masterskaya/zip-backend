//go:build e2e

package picturebank

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/cache"
	"github.com/Linka-masterskaya/zip-backend/internal/config"
	"github.com/Linka-masterskaya/zip-backend/internal/media"
	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
	"github.com/Linka-masterskaya/zip-backend/internal/pack"
	"github.com/Linka-masterskaya/zip-backend/internal/testutil"
	"github.com/Linka-masterskaya/zip-backend/migrations"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const picturesE2EJWTSecret = "pictures-e2e-only-secret"

func TestE2E_PicturesBankImportAndArchive(t *testing.T) {
	pool := picturesE2EDatabase(t)
	userID, folderID := picturesE2EUserAndFolder(t, pool)
	objectStorage, cleanupStorage := testutil.NewMinIO(t)
	t.Cleanup(cleanupStorage)
	redisClient, cleanupRedis := testutil.NewRedis(t)
	t.Cleanup(cleanupRedis)
	redisCache, err := cache.NewClient(cache.Config{
		URL: "redis://" + redisClient.Options().Addr, ClientName: "pictures-e2e", PoolSize: 5,
	})
	require.NoError(t, err)

	pictureID := uuid.New()
	imageData := picturesE2EPNG()
	var categoriesCalls, searchCalls, imageCalls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/category/all":
			categoriesCalls.Add(1)
			picturesE2EWriteJSON(t, w, []Category{{ID: "animals", Name: "Животные"}})
		case "/picture/search", "/picture/search/":
			searchCalls.Add(1)
			assert.Equal(t, "кот", r.URL.Query().Get("query"))
			picturesE2EWriteJSON(t, w, []Picture{{
				ID: pictureID.String(), Name: "Кот", MIMEType: "image/png",
				Categories: []Category{{ID: "animals", Name: "Животные"}},
			}})
		case "/picture/" + pictureID.String() + "/buffer":
			imageCalls.Add(1)
			w.Header().Set("Content-Type", "application/octet-stream")
			_, writeErr := w.Write(imageData)
			require.NoError(t, writeErr)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)
	upstreamURL, err := url.Parse(upstream.URL)
	require.NoError(t, err)

	picturesConfig := config.PicturesBankConfig{
		Timeout: 5 * time.Second, RequestsPerSecond: 20, InboundPerMinute: 100,
		MaxConcurrent: 4, CacheTTL: 5 * time.Minute,
		MaxMetadataBytes: 2 * 1024 * 1024, MaxImageBytes: 10 * 1024 * 1024,
	}
	client := newClient(picturesConfig, upstreamURL, upstream.Client(), redisCache)
	mediaService := media.NewService(media.NewRepository(pool), objectStorage)
	mediaHandler := media.NewHandler(mediaService)
	handler := NewHandler(NewService(client, mediaService))
	packRepo := pack.NewRepository(pool)
	packService := pack.NewService(packRepo, nil)
	packHandler := pack.NewHandler(packService)
	contentHandler := pack.NewContentHandler(
		pack.NewContentService(packRepo, objectStorage, mediaService, packService),
	)
	server := picturesE2EServer(
		t, redisCache, handler, mediaHandler, packHandler, contentHandler,
	)
	token := picturesE2EToken(t, userID)

	for range 2 {
		response := picturesE2ERequest(
			t, server, token, http.MethodGet, "/api/v1/pictures/categories", nil,
		)
		picturesE2EJSON[[]Category](t, response, http.StatusOK)
		response = picturesE2ERequest(
			t, server, token, http.MethodGet, "/api/v1/pictures/search?query=кот", nil,
		)
		picturesE2EJSON[[]Picture](t, response, http.StatusOK)
	}
	assert.EqualValues(t, 1, categoriesCalls.Load(), "categories response must be cached")
	assert.EqualValues(t, 1, searchCalls.Load(), "search response must be cached")

	firstImport := picturesE2EJSON[media.Response](
		t,
		picturesE2ERequest(
			t, server, token, http.MethodPost,
			"/api/v1/pictures/"+pictureID.String()+"/import", nil,
		),
		http.StatusCreated,
	)
	secondImport := picturesE2EJSON[media.Response](
		t,
		picturesE2ERequest(
			t, server, token, http.MethodPost,
			"/api/v1/pictures/"+pictureID.String()+"/import", nil,
		),
		http.StatusCreated,
	)
	assert.Equal(t, firstImport.ID, secondImport.ID, "same picture must be deduplicated")
	assert.EqualValues(t, 2, imageCalls.Load(), "image bytes are fetched afresh on each import")

	var mediaCount int
	var storageUsed int64
	require.NoError(t, pool.QueryRow(t.Context(), `SELECT count(*) FROM media_files`).Scan(&mediaCount))
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT storage_used_bytes FROM organizations WHERE id = (
			SELECT org_id FROM users WHERE id = $1
		)`, userID,
	).Scan(&storageUsed))
	assert.Equal(t, 1, mediaCount)
	assert.EqualValues(t, len(imageData), storageUsed, "deduplication must reserve quota once")

	createdPack := picturesE2EJSON[pack.Pack](
		t,
		picturesE2ERequest(t, server, token, http.MethodPost, "/api/v1/packs", map[string]any{
			"title": "Набор с картинкой", "folder_id": folderID,
		}),
		http.StatusCreated,
	)
	configBody := map[string]any{
		"metadata": map[string]any{"version": "2.0"},
		"settings": map[string]any{"columns": 1, "rows": 1},
		"blocks": []any{map[string]any{
			"id": "block-1", "type": "grid",
			"elements": []any{map[string]any{
				"id": "picture-1", "kind": "image",
				"media_id": firstImport.ID, "source_picture_id": pictureID,
			}},
		}},
	}
	picturesE2EJSON[pack.Pack](
		t,
		picturesE2ERequest(
			t, server, token, http.MethodPut,
			"/api/v1/packs/"+createdPack.ID.String()+"/config", configBody,
		),
		http.StatusOK,
	)
	exportResponse := picturesE2ERequest(
		t, server, token, http.MethodGet,
		"/api/v1/packs/"+createdPack.ID.String()+"/export", nil,
	)
	archive := picturesE2EBody(t, exportResponse, http.StatusOK)
	assert.Equal(t, "application/vnd.linka+zip", exportResponse.Header.Get("Content-Type"))
	exportedConfig, exportedMedia := picturesE2EArchive(t, archive)
	assert.Equal(t, imageData, exportedMedia)
	assert.Contains(t, string(exportedConfig), pictureID.String())
	assert.Contains(t, string(exportedConfig), firstImport.ID.String())
	assert.Contains(t, string(exportedConfig), "media/"+firstImport.ID.String()+".png")
}

func TestE2E_LocalPicturesBankUsesOrganizationMinIO(t *testing.T) {
	pool := picturesE2EDatabase(t)
	userID, _ := picturesE2EUserAndFolder(t, pool)
	foreignUserID, _ := picturesE2EUserAndFolder(t, pool)
	objectStorage, cleanupStorage := testutil.NewMinIO(t)
	t.Cleanup(cleanupStorage)
	redisClient, cleanupRedis := testutil.NewRedis(t)
	t.Cleanup(cleanupRedis)
	redisCache, err := cache.NewClient(cache.Config{
		URL:        "redis://" + redisClient.Options().Addr,
		ClientName: "local-pictures-e2e", PoolSize: 5,
	})
	require.NoError(t, err)

	mediaRepo := media.NewRepository(pool)
	mediaService := media.NewService(mediaRepo, objectStorage)
	localClient, err := NewLocalClient(mediaRepo, objectStorage, 10*1024*1024)
	require.NoError(t, err)
	pictureHandler := NewHandler(NewService(localClient, mediaService))
	mediaHandler := media.NewHandler(mediaService)
	packRepo := pack.NewRepository(pool)
	packService := pack.NewService(packRepo, nil)
	server := picturesE2EServer(
		t,
		redisCache,
		pictureHandler,
		mediaHandler,
		pack.NewHandler(packService),
		pack.NewContentHandler(
			pack.NewContentService(packRepo, objectStorage, mediaService, packService),
		),
	)
	token := picturesE2EToken(t, userID)
	foreignToken := picturesE2EToken(t, foreignUserID)
	imageData := picturesE2EPNG()

	uploaded := picturesE2EUploadMedia(t, server, token, "Кот.png", imageData)
	foreign := picturesE2EUploadMedia(t, server, foreignToken, "Чужой кот.png", imageData)
	listed := picturesE2EJSON[[]media.Response](
		t,
		picturesE2ERequest(
			t, server, token, http.MethodGet, "/api/v1/media?type=image&query=кот", nil,
		),
		http.StatusOK,
	)
	require.Len(t, listed, 1, "media catalog must not leak another organization")
	assert.Equal(t, uploaded.ID, listed[0].ID)
	assert.Equal(t, "Кот.png", listed[0].Name)
	assert.NotEmpty(t, listed[0].URL)

	byURL := picturesE2EJSON[media.Response](
		t,
		picturesE2ERequest(
			t, server, token, http.MethodGet,
			"/api/v1/media/"+uploaded.ID.String()+"/url", nil,
		),
		http.StatusOK,
	)
	assert.Equal(t, uploaded.ID, byURL.ID)

	categories := picturesE2EJSON[[]Category](
		t,
		picturesE2ERequest(
			t, server, token, http.MethodGet, "/api/v1/pictures/categories", nil,
		),
		http.StatusOK,
	)
	require.Len(t, categories, 1)
	assert.Equal(t, "local", categories[0].ID)
	pictures := picturesE2EJSON[[]Picture](
		t,
		picturesE2ERequest(
			t, server, token, http.MethodGet, "/api/v1/pictures/search?query=кот", nil,
		),
		http.StatusOK,
	)
	require.Len(t, pictures, 1)
	assert.Equal(t, uploaded.ID.String(), pictures[0].ID)
	assert.Equal(t, "Кот.png", pictures[0].Name)
	foreignContent := picturesE2ERequest(
		t, server, token, http.MethodGet,
		"/api/v1/pictures/"+foreign.ID.String()+"/content", nil,
	)
	picturesE2EBody(t, foreignContent, http.StatusNotFound)

	content := picturesE2ERequest(
		t, server, token, http.MethodGet,
		"/api/v1/pictures/"+uploaded.ID.String()+"/content", nil,
	)
	assert.Equal(t, imageData, picturesE2EBody(t, content, http.StatusOK))
	imported := picturesE2EJSON[media.Response](
		t,
		picturesE2ERequest(
			t, server, token, http.MethodPost,
			"/api/v1/pictures/"+uploaded.ID.String()+"/import", nil,
		),
		http.StatusCreated,
	)
	assert.Equal(t, uploaded.ID, imported.ID, "local import must reuse the existing object")

	var mediaCount int
	var storageUsed int64
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT count(*) FROM media_files WHERE org_id = (SELECT org_id FROM users WHERE id = $1)`,
		userID,
	).Scan(&mediaCount))
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT storage_used_bytes FROM organizations
		 WHERE id = (SELECT org_id FROM users WHERE id = $1)`, userID,
	).Scan(&storageUsed))
	assert.Equal(t, 1, mediaCount)
	assert.EqualValues(t, len(imageData), storageUsed)

	deleted := picturesE2ERequest(
		t, server, token, http.MethodDelete, "/api/v1/media/"+uploaded.ID.String(), nil,
	)
	picturesE2EBody(t, deleted, http.StatusNoContent)
	missing := picturesE2ERequest(
		t, server, token, http.MethodGet,
		"/api/v1/pictures/"+uploaded.ID.String()+"/content", nil,
	)
	picturesE2EBody(t, missing, http.StatusNotFound)
}

func picturesE2EDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, cleanup := testutil.NewPostgres(t)
	t.Cleanup(cleanup)
	sqlDB := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, migrations.Run(sqlDB))
	return pool
}

func picturesE2EUserAndFolder(t *testing.T, pool *pgxpool.Pool) (uuid.UUID, uuid.UUID) {
	t.Helper()
	orgID, userID, folderID := uuid.New(), uuid.New(), uuid.New()
	_, err := pool.Exec(t.Context(), `INSERT INTO organizations (id, name) VALUES ($1, $2)`,
		orgID, "pictures-e2e")
	require.NoError(t, err)
	_, err = pool.Exec(t.Context(),
		`INSERT INTO users (id, org_id, display_name) VALUES ($1, $2, $3)`,
		userID, orgID, "pictures-e2e")
	require.NoError(t, err)
	_, err = pool.Exec(t.Context(), `
		INSERT INTO folders (id, org_id, owner_id, section, kind, name, depth)
		VALUES ($1, $2, $3, 'my', 'folder', 'Pictures E2E', 0)`,
		folderID, orgID, userID)
	require.NoError(t, err)
	return userID, folderID
}

func picturesE2EServer(
	t *testing.T,
	redisCache *cache.Client,
	pictureHandler *Handler,
	mediaHandler *media.Handler,
	packHandler *pack.Handler,
	contentHandler *pack.ContentHandler,
) *httptest.Server {
	t.Helper()
	auth := middleware.NewAuthMW([]byte(picturesE2EJWTSecret))
	rateLimit := middleware.RateLimit(redisCache, "pictures_e2e", 100, time.Minute, nil)
	protected := func(next middleware.AppHandler) http.Handler {
		return rateLimit(middleware.ErrorMiddleware(auth.AuthMiddleware(next)))
	}
	mux := http.NewServeMux()
	mux.Handle("GET /api/v1/pictures/categories", protected(pictureHandler.Categories))
	mux.Handle("GET /api/v1/pictures/search", protected(pictureHandler.Search))
	mux.Handle("GET /api/v1/pictures/{id}/content", protected(pictureHandler.Image))
	mux.Handle("POST /api/v1/pictures/{id}/import", protected(pictureHandler.Import))
	mux.Handle("POST /api/v1/media", protected(mediaHandler.Upload))
	mux.Handle("GET /api/v1/media", protected(mediaHandler.List))
	mux.Handle("GET /api/v1/media/{id}", protected(mediaHandler.Get))
	mux.Handle("GET /api/v1/media/{id}/url", protected(mediaHandler.Get))
	mux.Handle("DELETE /api/v1/media/{id}", protected(mediaHandler.Delete))
	mux.Handle("POST /api/v1/packs", protected(packHandler.CreatePack))
	mux.Handle("PUT /api/v1/packs/{id}/config", protected(contentHandler.SaveConfig))
	mux.Handle("GET /api/v1/packs/{id}/export", protected(contentHandler.Export))
	server := httptest.NewServer(middleware.Chain(
		mux, middleware.RecoveryMiddleware, middleware.RequestIDMiddleware,
	))
	t.Cleanup(server.Close)
	return server
}

func picturesE2EUploadMedia(
	t *testing.T,
	server *httptest.Server,
	token, name string,
	data []byte,
) media.Response {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", name)
	require.NoError(t, err)
	_, err = part.Write(data)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	request, err := http.NewRequestWithContext(
		t.Context(), http.MethodPost, server.URL+"/api/v1/media", &body,
	)
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := server.Client().Do(request)
	require.NoError(t, err)
	return picturesE2EJSON[media.Response](t, response, http.StatusCreated)
}

func picturesE2EToken(t *testing.T, userID uuid.UUID) string {
	t.Helper()
	now := time.Now()
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, middleware.AccessClaims{
		Role: "defectologist",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: userID.String(), Issuer: middleware.JWTIssuer,
			Audience: jwt.ClaimStrings{middleware.JWTAudience},
			IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
		},
	}).SignedString([]byte(picturesE2EJWTSecret))
	require.NoError(t, err)
	return token
}

func picturesE2ERequest(
	t *testing.T,
	server *httptest.Server,
	token, method, path string,
	body any,
) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, reader)
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := server.Client().Do(request)
	require.NoError(t, err)
	return response
}

func picturesE2EJSON[T any](t *testing.T, response *http.Response, status int) T {
	t.Helper()
	data := picturesE2EBody(t, response, status)
	var result T
	require.NoError(t, json.Unmarshal(data, &result), "response: %s", data)
	return result
}

func picturesE2EBody(t *testing.T, response *http.Response, status int) []byte {
	t.Helper()
	defer func() { require.NoError(t, response.Body.Close()) }()
	data, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, status, response.StatusCode, "response: %s", data)
	return data
}

func picturesE2EWriteJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	require.NoError(t, json.NewEncoder(w).Encode(value))
}

func picturesE2EArchive(t *testing.T, data []byte) ([]byte, []byte) {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	var configData, mediaData []byte
	for _, file := range reader.File {
		stream, openErr := file.Open()
		require.NoError(t, openErr)
		content, readErr := io.ReadAll(stream)
		require.NoError(t, readErr)
		require.NoError(t, stream.Close())
		switch {
		case file.Name == "config.json":
			configData = content
		case len(file.Name) > len("media/") && file.Name[:len("media/")] == "media/":
			mediaData = content
		}
	}
	require.NotEmpty(t, configData)
	require.NotEmpty(t, mediaData)
	return configData, mediaData
}

func picturesE2EPNG() []byte {
	return []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
		0x89, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x44, 0x41,
		0x54, 0x08, 0xd7, 0x63, 0xf8, 0xcf, 0xc0, 0xf0,
		0x1f, 0x00, 0x05, 0x00, 0x01, 0xff, 0x89, 0x99,
		0x3d, 0x1d, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45,
		0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
	}
}
