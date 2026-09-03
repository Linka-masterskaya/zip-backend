//go:build e2e

package picturebank

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/cache"
	"github.com/Linka-masterskaya/zip-backend/internal/config"
	"github.com/Linka-masterskaya/zip-backend/internal/media"
	"github.com/Linka-masterskaya/zip-backend/internal/pack"
	"github.com/Linka-masterskaya/zip-backend/internal/testutil"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestE2E_LocalPicturesBankImportAndArchive(t *testing.T) {
	pool := picturesE2EDatabase(t)
	userID, folderID := picturesE2EUserAndFolder(t, pool)
	objectStorage, cleanupStorage := testutil.NewMinIO(t)
	t.Cleanup(cleanupStorage)
	redisClient, cleanupRedis := testutil.NewRedis(t)
	t.Cleanup(cleanupRedis)
	redisCache, err := cache.NewClient(cache.Config{
		URL: "redis://" + redisClient.Options().Addr, ClientName: "pictures-local-e2e", PoolSize: 5,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, redisCache.Close()) })

	picturesConfig := config.PicturesBankConfig{
		URL: "", MaxImageBytes: 10 * 1024 * 1024, CacheTTL: 5 * time.Minute,
	}
	seeder, err := NewSeeder(pool, objectStorage, picturesConfig.MaxImageBytes)
	require.NoError(t, err)
	pictureID := uuid.New()
	categoryID := uuid.New()
	imageData := picturesE2EPNG()
	createdID, err := seeder.Add(t.Context(), SeedInput{
		ID: pictureID, Category: categoryID.String(), Title: "Кот", Data: imageData,
	})
	require.NoError(t, err)
	assert.Equal(t, pictureID, createdID)

	literalSearchID := uuid.New()
	_, err = seeder.Add(t.Context(), SeedInput{
		ID: literalSearchID, Category: categoryID.String(), Title: "Скидка 100%_off", Data: imageData,
	})
	require.NoError(t, err)
	_, err = seeder.Add(t.Context(), SeedInput{
		ID: uuid.New(), Category: categoryID.String(), Title: "Скидка 100Xoff", Data: imageData,
	})
	require.NoError(t, err)

	var minioKey string
	require.NoError(t, pool.QueryRow(t.Context(), `
		SELECT minio_key FROM picture_bank_images WHERE id = $1
	`, pictureID).Scan(&minioKey))
	assert.True(t, strings.HasPrefix(minioKey, LocalObjectPrefix+"/"))
	assert.False(t, strings.HasPrefix(minioKey, "media/"))

	source, err := NewSource(true, picturesConfig, nil, LocalDependencies{
		DB: pool, Storage: objectStorage,
	})
	require.NoError(t, err)
	mediaService := media.NewService(media.NewRepository(pool), objectStorage)
	picturesService := NewService(source)
	handler := NewHandler(picturesService, picturesConfig.CacheTTL)
	packRepo := pack.NewRepository(pool)
	packService := pack.NewService(packRepo, nil)
	packHandler := pack.NewHandler(packService)
	contentHandler := pack.NewContentHandler(
		pack.NewContentService(
			packRepo, objectStorage, mediaService, packService,
			func(ctx context.Context, id uuid.UUID) ([]byte, string, error) {
				image, loadErr := picturesService.Image(ctx, id.String())
				if errors.Is(loadErr, ErrPictureNotFound) {
					return nil, "", pack.ErrMissingMediaReference
				}
				if loadErr != nil {
					return nil, "", loadErr
				}
				return image.Data, image.ContentType, nil
			},
		),
	)
	server := picturesE2EServer(t, redisCache, handler, packHandler, contentHandler)
	token := picturesE2EToken(t, userID)

	assertPicturesE2ERequiresAuth(t, server.URL, pictureID, categoryID)

	categoriesResponse := picturesE2ERequest(
		t, server, token, http.MethodGet, "/api/v1/pictures/categories", nil,
	)
	categories := picturesE2EJSON[[]Category](t, categoriesResponse, http.StatusOK)
	require.Equal(t, []Category{{ID: categoryID.String(), Name: categoryID.String()}}, categories)

	searchResponse := picturesE2ERequest(
		t, server, token, http.MethodGet, "/api/v1/pictures/search?query=кот", nil,
	)
	searchPictures := picturesE2EJSON[[]e2ePictureResponse](t, searchResponse, http.StatusOK)

	require.Len(t, searchPictures, 1)
	assert.Equal(t, pictureID.String(), searchPictures[0].ID)
	assert.Equal(t, "Кот", searchPictures[0].Name)
	assert.Equal(t, "image/png", searchPictures[0].MIMEType)
	assert.Equal(t, []string{categoryID.String()}, searchPictures[0].Categories)
	assert.Equal(t, "/api/v1/pictures/"+pictureID.String()+"/content", searchPictures[0].URL)

	literalSearchResponse := picturesE2ERequest(
		t, server, token, http.MethodGet,
		"/api/v1/pictures/search?query="+url.QueryEscape("100%_off"), nil,
	)
	literalPictures := picturesE2EJSON[[]e2ePictureResponse](t, literalSearchResponse, http.StatusOK)
	require.Len(t, literalPictures, 1, "percent and underscore must be searched literally")
	assert.Equal(t, literalSearchID.String(), literalPictures[0].ID)
	assert.Equal(t, []string{categoryID.String()}, literalPictures[0].Categories)
	assert.Contains(t, literalPictures[0].URL, "/api/v1/pictures/")

	categoryResponse := picturesE2ERequest(
		t, server, token, http.MethodGet, "/api/v1/pictures/category/"+categoryID.String()+"/list", nil,
	)
	categoryPictures := picturesE2EJSON[[]e2ePictureResponse](t, categoryResponse, http.StatusOK)
	require.Len(t, categoryPictures, 3)
	var found bool
	for _, p := range categoryPictures {
		if p.ID == pictureID.String() {
			found = true
			assert.Equal(t, "Кот", p.Name)
			assert.Equal(t, []string{categoryID.String()}, p.Categories)
			assert.Equal(t, "/api/v1/pictures/"+pictureID.String()+"/content", p.URL)
			break
		}
	}
	assert.True(t, found, "Ожидалось найти засиженную картинку 'Кот' в списке категории")

	contentResponse := picturesE2ERequest(
		t, server, token, http.MethodGet, "/api/v1/pictures/"+pictureID.String()+"/content", nil,
	)
	content := picturesE2EBody(t, contentResponse, http.StatusOK)
	assert.Equal(t, "image/png", contentResponse.Header.Get("Content-Type"))
	assert.Equal(t, imageData, content)

	importResponse := picturesE2ERequest(
		t, server, token, http.MethodPost, "/api/v1/pictures/"+pictureID.String()+"/import", nil,
	)
	importBody := picturesE2EBody(t, importResponse, http.StatusCreated)
	assert.NotContains(t, string(importBody), "http://")
	assert.NotContains(t, string(importBody), "https://")
	assert.NotContains(t, string(importBody), LocalObjectPrefix)
	var reference PictureReference
	require.NoError(t, jsonUnmarshal(importBody, &reference))
	assert.Equal(t, pictureID, reference.SourcePictureID)
	assert.Equal(t, "/api/v1/pictures/"+pictureID.String()+"/content", reference.ContentURL)

	var mediaCount int
	var storageUsed int64
	require.NoError(t, pool.QueryRow(t.Context(), `SELECT count(*) FROM media_files`).Scan(&mediaCount))
	require.NoError(t, pool.QueryRow(t.Context(), `
		SELECT storage_used_bytes FROM organizations WHERE id = (
			SELECT org_id FROM users WHERE id = $1
		)
	`, userID).Scan(&storageUsed))
	assert.Zero(t, mediaCount, "local Pictures Bank import must not create user media")
	assert.Zero(t, storageUsed, "system Pictures Bank must not consume organization quota")

	createdPack := picturesE2EJSON[pack.Pack](
		t,
		picturesE2ERequest(t, server, token, http.MethodPost, "/api/v1/packs", map[string]any{
			"title": "Набор с локальной картинкой", "folder_id": folderID,
		}),
		http.StatusCreated,
	)
	configBody := map[string]any{
		"metadata": map[string]any{"version": "2.0"},
		"settings": map[string]any{"columns": 1, "rows": 1},
		"blocks": []any{map[string]any{
			"id": "block-1", "type": "grid",
			"elements": []any{map[string]any{
				"id": "picture-1", "kind": "image", "source_picture_id": pictureID,
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
	exportedConfig, exportedMedia := picturesE2EArchive(t, archive)
	assert.Equal(t, imageData, exportedMedia)
	assert.Contains(t, string(exportedConfig), pictureID.String())
	assert.NotContains(t, string(exportedConfig), LocalObjectPrefix)
	assert.Contains(t, string(exportedConfig), "media/picture-"+pictureID.String()+".png")

	require.NoError(t, seeder.Delete(t.Context(), pictureID))
	deletedResponse := picturesE2ERequest(
		t, server, token, http.MethodGet, "/api/v1/pictures/"+pictureID.String()+"/content", nil,
	)
	deletedBody := picturesE2EBody(t, deletedResponse, http.StatusOK)
	assert.Equal(t, "deleted", deletedResponse.Header.Get("X-Picture-Placeholder"))
	assert.Equal(t, "image/svg+xml", deletedResponse.Header.Get("Content-Type"))
	assert.Contains(t, string(deletedBody), "Картинка удалена")
}

func assertPicturesE2ERequiresAuth(t *testing.T, serverURL string, pictureID, categoryID uuid.UUID) {
	t.Helper()
	tests := []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/api/v1/pictures/categories"},
		{method: http.MethodGet, path: "/api/v1/pictures/search?query=кот"},
		{method: http.MethodGet, path: "/api/v1/pictures/category/" + categoryID.String() + "/list"},
		{method: http.MethodGet, path: "/api/v1/pictures/" + pictureID.String() + "/content"},
		{method: http.MethodPost, path: "/api/v1/pictures/" + pictureID.String() + "/import"},
	}
	for _, test := range tests {
		request, err := http.NewRequestWithContext(t.Context(), test.method, serverURL+test.path, nil)
		require.NoError(t, err)
		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		_ = picturesE2EBody(t, response, http.StatusUnauthorized)
	}
}

func jsonUnmarshal(data []byte, target any) error {
	return json.Unmarshal(data, target)
}
