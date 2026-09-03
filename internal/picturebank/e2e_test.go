//go:build e2e

package picturebank

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
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

type e2ePictureResponse struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	MIMEType   string   `json:"mimeType"`
	Categories []string `json:"categories"`
	URL        string   `json:"url"`
}

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
	categoryID := uuid.New()
	imageData := picturesE2EPNG()
	var categoriesCalls, searchCalls, imageCalls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/category/all":
			categoriesCalls.Add(1)
			picturesE2EWriteJSON(t, w, []Category{{ID: categoryID.String(), Name: "Животные"}})
		case "/picture/search", "/picture/search/":
			searchCalls.Add(1)
			assert.Equal(t, "кот", r.URL.Query().Get("query"))
			picturesE2EWriteJSON(t, w, []Picture{{
				ID: pictureID.String(), Name: "Кот", MIMEType: "image/png",
				Categories: []Category{{ID: categoryID.String(), Name: "Животные"}},
			}})
		case "/picture/category/" + categoryID.String():
			picturesE2EWriteJSON(t, w, []Picture{{
				ID: pictureID.String(), Name: "Кот", MIMEType: "image/png",
				Categories: []Category{{ID: categoryID.String(), Name: "Животные"}},
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
	picturesService := NewService(client)
	handler := NewHandler(picturesService)
	packRepo := pack.NewRepository(pool)
	packService := pack.NewService(packRepo, nil)
	packHandler := pack.NewHandler(packService)
	contentHandler := pack.NewContentHandler(
		pack.NewContentService(
			packRepo, objectStorage, mediaService, packService,
			func(ctx context.Context, id uuid.UUID) ([]byte, string, error) {
				image, loadErr := picturesService.Image(ctx, id.String())
				if loadErr != nil {
					return nil, "", loadErr
				}
				return image.Data, image.ContentType, nil
			},
		),
	)
	server := picturesE2EServer(t, redisCache, handler, packHandler, contentHandler)
	token := picturesE2EToken(t, userID)

	for range 2 {
		response := picturesE2ERequest(
			t, server, token, http.MethodGet, "/api/v1/pictures/categories", nil,
		)
		picturesE2EJSON[[]Category](t, response, http.StatusOK)

		response = picturesE2ERequest(
			t, server, token, http.MethodGet, "/api/v1/pictures/search?query=кот", nil,
		)
		searchPictures := picturesE2EJSON[[]e2ePictureResponse](t, response, http.StatusOK)
		require.Len(t, searchPictures, 1)
		assert.Equal(t, pictureID.String(), searchPictures[0].ID)
		assert.Equal(t, "Кот", searchPictures[0].Name)
		assert.Equal(t, []string{"Животные"}, searchPictures[0].Categories)
		assert.Equal(t, "/api/v1/pictures/"+pictureID.String()+"/content", searchPictures[0].URL)
	}

	categoryResponse := picturesE2ERequest(
		t, server, token, http.MethodGet, "/api/v1/pictures/category/"+categoryID.String()+"/list", nil,
	)
	categoryPictures := picturesE2EJSON[[]e2ePictureResponse](t, categoryResponse, http.StatusOK)
	require.Len(t, categoryPictures, 1)
	assert.Equal(t, pictureID.String(), categoryPictures[0].ID)
	assert.Equal(t, "Кот", categoryPictures[0].Name)
	assert.Equal(t, []string{"Животные"}, categoryPictures[0].Categories)
	assert.Equal(t, "/api/v1/pictures/"+pictureID.String()+"/content", categoryPictures[0].URL)
	assert.EqualValues(t, 1, categoriesCalls.Load(), "categories response must be cached")
	assert.EqualValues(t, 1, searchCalls.Load(), "search response must be cached")

	firstImport := picturesE2EJSON[PictureReference](
		t,
		picturesE2ERequest(
			t, server, token, http.MethodPost,
			"/api/v1/pictures/"+pictureID.String()+"/import", nil,
		),
		http.StatusCreated,
	)
	secondImport := picturesE2EJSON[PictureReference](
		t,
		picturesE2ERequest(
			t, server, token, http.MethodPost,
			"/api/v1/pictures/"+pictureID.String()+"/import", nil,
		),
		http.StatusCreated,
	)
	assert.Equal(t, firstImport.SourcePictureID, secondImport.SourcePictureID)
	assert.Equal(t, pictureID, firstImport.SourcePictureID)
	assert.EqualValues(t, 0, imageCalls.Load(), "reference creation must not fetch image bytes")

	var mediaCount int
	var storageUsed int64
	require.NoError(t, pool.QueryRow(t.Context(), `SELECT count(*) FROM media_files`).Scan(&mediaCount))
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT storage_used_bytes FROM organizations WHERE id = (
			SELECT org_id FROM users WHERE id = $1
		)`, userID,
	).Scan(&storageUsed))
	assert.Zero(t, mediaCount, "Pictures Bank references must not create local media")
	assert.Zero(t, storageUsed, "Pictures Bank references must not consume organization quota")

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
				"source_picture_id": pictureID,
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
	assert.NotContains(t, string(exportedConfig), "media_id")
	assert.Contains(t, string(exportedConfig), "media/picture-"+pictureID.String()+".png")
	assert.EqualValues(t, 1, imageCalls.Load(), "export resolves external bytes on demand")
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
	packHandler *pack.Handler,
	contentHandler *pack.ContentHandler,
) *httptest.Server {
	t.Helper()
	auth := middleware.NewAuthMW([]byte(picturesE2EJWTSecret))

	policy := middleware.EndpointPolicy{
		IPLimit:        100,
		IPWindow:       time.Minute,
		IdentityLimit:  100,
		IdentityWindow: time.Minute,
		GlobalLimit:    10000,
		GlobalWindow:   time.Minute,
	}
	rateLimit := middleware.RateLimit(redisCache, "pictures_e2e", policy, nil)

	protected := func(next middleware.AppHandler) http.Handler {
		return rateLimit(middleware.ErrorMiddleware(auth.AuthMiddleware(next)))
	}
	mux := http.NewServeMux()
	mux.Handle("GET /api/v1/pictures/categories", protected(pictureHandler.Categories))
	mux.Handle("GET /api/v1/pictures/search", protected(pictureHandler.Search))
	mux.Handle("GET /api/v1/pictures/{id}/content", protected(pictureHandler.Image))
	mux.Handle("POST /api/v1/pictures/{id}/import", protected(pictureHandler.Import))
	mux.Handle("GET /api/v1/pictures/category/{categoryId}/list", protected(pictureHandler.PicturesByCategory))
	mux.Handle("POST /api/v1/packs", protected(packHandler.CreatePack))
	mux.Handle("PUT /api/v1/packs/{id}/config", protected(contentHandler.SaveConfig))
	mux.Handle("GET /api/v1/packs/{id}/export", protected(contentHandler.Export))
	server := httptest.NewServer(middleware.Chain(
		mux, middleware.RecoveryMiddleware, middleware.RequestIDMiddleware,
	))
	t.Cleanup(server.Close)
	return server
}

func picturesE2EToken(t *testing.T, userID uuid.UUID) string {
	t.Helper()
	now := time.Now()
	jwtToken := jwt.NewWithClaims(jwt.SigningMethodHS256, middleware.AccessClaims{
		Role: "defectologist",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: userID.String(), Issuer: middleware.JWTIssuer,
			Audience: jwt.ClaimStrings{middleware.JWTAudience},
			IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
		},
	})
	jwtToken.Header["typ"] = "access"
	token, err := jwtToken.SignedString([]byte(picturesE2EJWTSecret))
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
