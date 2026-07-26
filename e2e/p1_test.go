//go:build e2e

package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/cryptox"
	"github.com/Linka-masterskaya/zip-backend/internal/folder"
	"github.com/Linka-masterskaya/zip-backend/internal/httpapi"
	"github.com/Linka-masterskaya/zip-backend/internal/media"
	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
	"github.com/Linka-masterskaya/zip-backend/internal/pack"
	"github.com/Linka-masterskaya/zip-backend/internal/student"
	"github.com/Linka-masterskaya/zip-backend/internal/testutil"
	"github.com/Linka-masterskaya/zip-backend/migrations"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const e2eJWTSecret = "e2e-only-secret"

func TestE2E_P1UserJourney(t *testing.T) {
	pool := e2eDatabase(t)
	ownerID := e2eUser(t, pool, "owner")
	readerID := e2eUser(t, pool, "reader")
	server := e2eServer(t, pool)
	ownerToken := e2eToken(t, ownerID, "defectologist")
	readerToken := e2eToken(t, readerID, "defectologist")

	t.Run("authorization is enforced", func(t *testing.T) {
		response := e2eRequest(t, server, "", http.MethodGet, "/api/v1/students", nil)
		assert.Equal(t, http.StatusUnauthorized, response.StatusCode)
		e2eClose(t, response)
	})

	root := e2eCreateFolder(t, server, ownerToken, map[string]any{
		"section": "my", "kind": "folder", "name": "Мои материалы",
	})
	child := e2eCreateFolder(t, server, ownerToken, map[string]any{
		"parent_id": root.ID, "section": "my", "kind": "folder", "name": "Звуки",
	})
	library := e2eCreateFolder(t, server, ownerToken, map[string]any{
		"section": "library", "kind": "folder", "name": "Общая библиотека",
	})

	t.Run("non-empty folder cannot be deleted", func(t *testing.T) {
		response := e2eRequest(
			t, server, ownerToken, http.MethodDelete, "/api/v1/folders/"+root.ID.String(), nil,
		)
		assert.Equal(t, http.StatusConflict, response.StatusCode)
		e2eClose(t, response)
	})

	createdPack := e2eJSON[pack.Pack](
		t,
		e2eRequest(t, server, ownerToken, http.MethodPost, "/api/v1/packs", map[string]any{
			"title": "Автоматизация Р", "folder_id": root.ID,
		}),
		http.StatusCreated,
	)
	assert.JSONEq(
		t,
		`{"metadata":{"version":"2.0"},"settings":{"columns":1,"rows":1},"blocks":[]}`,
		string(createdPack.Config),
	)

	uploaded := e2eUploadMedia(t, server, ownerToken, tinyPNG())
	foreignMedia := e2eUploadMedia(t, server, readerToken, tinyPNG())
	foreignConfig := packImageConfig(foreignMedia.ID)
	foreignResponse := e2eRequest(t, server, ownerToken, http.MethodPut,
		"/api/v1/packs/"+createdPack.ID.String()+"/config", foreignConfig)
	assert.Equal(t, http.StatusForbidden, foreignResponse.StatusCode)
	e2eClose(t, foreignResponse)

	config := packImageConfig(uploaded.ID)
	configured := e2eJSON[pack.Pack](
		t,
		e2eRequest(t, server, ownerToken, http.MethodPut,
			"/api/v1/packs/"+createdPack.ID.String()+"/config", config),
		http.StatusOK,
	)
	assert.Contains(t, string(configured.Config), uploaded.ID.String())

	exported := e2eRequest(
		t, server, ownerToken, http.MethodGet,
		"/api/v1/packs/"+createdPack.ID.String()+"/export", nil,
	)
	exportBytes := e2eBody(t, exported, http.StatusOK)
	assert.Equal(t, "application/vnd.linka+zip", exported.Header.Get("Content-Type"))
	imported := e2eImportPack(t, server, ownerToken, root.ID, exportBytes)
	assert.Equal(t, "Импортированный набор", imported.Title)
	assert.Contains(t, string(imported.Config), uploaded.ID.String())
	var storageUsed int64
	require.NoError(t, pool.QueryRow(t.Context(), `
		SELECT o.storage_used_bytes
		FROM organizations o JOIN users u ON u.org_id = o.id
		WHERE u.id = $1`, ownerID).Scan(&storageUsed))
	assert.Equal(t, int64(len(tinyPNG())), storageUsed, "deduplicated import must not consume quota twice")

	updatedPack := e2eJSON[pack.Pack](
		t,
		e2eRequest(
			t,
			server,
			ownerToken,
			http.MethodPatch,
			"/api/v1/packs/"+createdPack.ID.String(),
			map[string]any{
				"title": "Автоматизация звука Р", "age_min": 5, "age_max": 8,
				"difficulty": "medium", "goals": []string{"speech"}, "notes": "Домашнее задание",
			},
		),
		http.StatusOK,
	)
	assert.Equal(t, "Автоматизация звука Р", updatedPack.Title)
	require.NotNil(t, updatedPack.AgeMin)
	assert.Equal(t, 5, *updatedPack.AgeMin)

	movedPack := e2eJSON[pack.Pack](
		t,
		e2eRequest(
			t,
			server,
			ownerToken,
			http.MethodPost,
			"/api/v1/packs/"+createdPack.ID.String()+"/move",
			map[string]any{"folder_id": child.ID},
		),
		http.StatusOK,
	)
	assert.Equal(t, child.ID, movedPack.FolderID)

	contents := e2eJSON[folder.ContentsPage](
		t,
		e2eRequest(
			t,
			server,
			ownerToken,
			http.MethodGet,
			"/api/v1/folders/"+child.ID.String()+"/contents",
			nil,
		),
		http.StatusOK,
	)
	require.Len(t, contents.Items, 1)
	assert.Equal(t, "pack", contents.Items[0].Type)
	assert.Equal(t, createdPack.ID, contents.Items[0].ID)

	published := e2eJSON[pack.Pack](
		t,
		e2eRequest(
			t,
			server,
			ownerToken,
			http.MethodPost,
			"/api/v1/packs/"+createdPack.ID.String()+"/publication",
			map[string]any{"library_folder_id": library.ID},
		),
		http.StatusOK,
	)
	require.NotNil(t, published.PublishedAt)
	require.NotNil(t, published.LibraryFolderID)
	assert.Equal(t, library.ID, *published.LibraryFolderID)

	t.Run("published pack is readable but not deletable", func(t *testing.T) {
		readable := e2eJSON[pack.Pack](
			t,
			e2eRequest(
				t,
				server,
				readerToken,
				http.MethodGet,
				"/api/v1/packs/"+createdPack.ID.String(),
				nil,
			),
			http.StatusOK,
		)
		assert.Equal(t, createdPack.ID, readable.ID)

		response := e2eRequest(
			t,
			server,
			ownerToken,
			http.MethodDelete,
			"/api/v1/packs/"+createdPack.ID.String(),
			nil,
		)
		assert.Equal(t, http.StatusConflict, response.StatusCode)
		e2eClose(t, response)
	})

	response := e2eRequest(
		t,
		server,
		ownerToken,
		http.MethodDelete,
		"/api/v1/packs/"+createdPack.ID.String()+"/publication",
		nil,
	)
	assert.Equal(t, http.StatusNoContent, response.StatusCode)
	e2eClose(t, response)

	t.Run("unpublished pack is private again", func(t *testing.T) {
		response := e2eRequest(
			t,
			server,
			readerToken,
			http.MethodGet,
			"/api/v1/packs/"+createdPack.ID.String(),
			nil,
		)
		assert.Equal(t, http.StatusNotFound, response.StatusCode)
		e2eClose(t, response)
	})

	createdStudent := e2eJSON[student.Student](
		t,
		e2eRequest(t, server, ownerToken, http.MethodPost, "/api/v1/students", map[string]any{
			"email": " Student@Example.com ", "name": "Анна", "age": 7,
		}),
		http.StatusCreated,
	)
	assert.Equal(t, "student@example.com", createdStudent.Email)

	students := e2eJSON[[]student.Student](
		t,
		e2eRequest(t, server, ownerToken, http.MethodGet, "/api/v1/students", nil),
		http.StatusOK,
	)
	require.Len(t, students, 1)
	assert.Equal(t, createdStudent.ID, students[0].ID)

	studentFolder := e2eCreateFolder(t, server, ownerToken, map[string]any{
		"section": "students", "kind": "student", "student_id": createdStudent.ID, "name": "Анна",
	})
	response = e2eRequest(
		t,
		server,
		ownerToken,
		http.MethodDelete,
		"/api/v1/students/"+createdStudent.ID.String(),
		nil,
	)
	assert.Equal(t, http.StatusConflict, response.StatusCode)
	e2eClose(t, response)

	for _, path := range []string{
		"/api/v1/folders/" + studentFolder.ID.String(),
		"/api/v1/students/" + createdStudent.ID.String(),
	} {
		response = e2eRequest(t, server, ownerToken, http.MethodDelete, path, nil)
		assert.Equal(t, http.StatusNoContent, response.StatusCode)
		e2eClose(t, response)
	}
}

func e2eDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, cleanup := testutil.NewPostgres(t)
	t.Cleanup(cleanup)

	sqlDB := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, migrations.Run(sqlDB))
	return pool
}

func e2eServer(t *testing.T, pool *pgxpool.Pool) *httptest.Server {
	t.Helper()
	crypto, err := cryptox.New(bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32))
	require.NoError(t, err)

	objectStorage, cleanupStorage := testutil.NewMinIO(t)
	t.Cleanup(cleanupStorage)
	packRepo := pack.NewRepository(pool)
	packService := pack.NewService(packRepo, nil)
	packHandler := pack.NewHandler(packService)
	mediaService := media.NewService(media.NewRepository(pool), objectStorage)
	mediaHandler := media.NewHandler(mediaService)
	contentHandler := pack.NewContentHandler(
		pack.NewContentService(packRepo, objectStorage, mediaService, packService),
	)
	folderHandler := folder.NewHandler(folder.NewService(folder.NewRepository(pool)))
	studentHandler := student.NewHandler(student.NewService(student.NewRepository(pool), crypto))

	mux := http.NewServeMux()
	httpapi.RegisterP1Routes(
		mux,
		middleware.NewAuthMW([]byte(e2eJWTSecret)),
		func(next http.Handler) http.Handler { return next },
		httpapi.P1Handlers{
			Pack: packHandler, Content: contentHandler, Media: mediaHandler,
			Folder: folderHandler, Student: studentHandler,
		},
	)
	server := httptest.NewServer(middleware.Chain(
		mux,
		middleware.RecoveryMiddleware,
		middleware.RequestIDMiddleware,
	))
	t.Cleanup(server.Close)
	return server
}

func e2eUploadMedia(
	t *testing.T,
	server *httptest.Server,
	token string,
	data []byte,
) media.Response {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "pixel.png")
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
	return e2eJSON[media.Response](t, response, http.StatusCreated)
}

func e2eImportPack(
	t *testing.T,
	server *httptest.Server,
	token string,
	folderID uuid.UUID,
	data []byte,
) pack.Pack {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("title", "Импортированный набор"))
	require.NoError(t, writer.WriteField("folder_id", folderID.String()))
	part, err := writer.CreateFormFile("file", "pack.linka")
	require.NoError(t, err)
	_, err = part.Write(data)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	request, err := http.NewRequestWithContext(
		t.Context(), http.MethodPost, server.URL+"/api/v1/packs/import", &body,
	)
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := server.Client().Do(request)
	require.NoError(t, err)
	return e2eJSON[pack.Pack](t, response, http.StatusCreated)
}

func tinyPNG() []byte {
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

func packImageConfig(mediaID uuid.UUID) map[string]any {
	return map[string]any{
		"metadata": map[string]any{"version": "2.0"},
		"settings": map[string]any{"columns": 1, "rows": 1},
		"blocks": []any{map[string]any{
			"id": "block-1", "type": "grid",
			"elements": []any{map[string]any{
				"id": "image-1", "kind": "image", "media_id": mediaID,
			}},
		}},
	}
}

func e2eBody(t *testing.T, response *http.Response, expectedStatus int) []byte {
	t.Helper()
	defer e2eClose(t, response)
	data, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, expectedStatus, response.StatusCode, "unexpected response: %s", data)
	return data
}

func e2eUser(t *testing.T, pool *pgxpool.Pool, name string) uuid.UUID {
	t.Helper()
	orgID, userID := uuid.New(), uuid.New()
	_, err := pool.Exec(
		context.Background(),
		`INSERT INTO organizations (id, name) VALUES ($1, $2)`,
		orgID,
		name,
	)
	require.NoError(t, err)
	_, err = pool.Exec(
		context.Background(),
		`INSERT INTO users (id, org_id, display_name) VALUES ($1, $2, $3)`,
		userID,
		orgID,
		name,
	)
	require.NoError(t, err)
	return userID
}

func e2eToken(t *testing.T, userID uuid.UUID, role string) string {
	t.Helper()
	now := time.Now()
	claims := middleware.AccessClaims{
		Role: role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: userID.String(),
			Issuer:  middleware.JWTIssuer,
			Audience: jwt.ClaimStrings{
				middleware.JWTAudience,
			},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).
		SignedString([]byte(e2eJWTSecret))
	require.NoError(t, err)
	return token
}

func e2eCreateFolder(
	t *testing.T,
	server *httptest.Server,
	token string,
	body map[string]any,
) folder.Folder {
	t.Helper()
	return e2eJSON[folder.Folder](
		t,
		e2eRequest(t, server, token, http.MethodPost, "/api/v1/folders", body),
		http.StatusCreated,
	)
}

func e2eRequest(
	t *testing.T,
	server *httptest.Server,
	token string,
	method string,
	path string,
	body any,
) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(
		t.Context(),
		method,
		server.URL+path,
		reader,
	)
	require.NoError(t, err)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := server.Client().Do(request)
	require.NoError(t, err)
	return response
}

func e2eJSON[T any](t *testing.T, response *http.Response, expectedStatus int) T {
	t.Helper()
	defer e2eClose(t, response)
	data, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(
		t,
		expectedStatus,
		response.StatusCode,
		"unexpected response: %s",
		string(data),
	)
	var result T
	require.NoError(t, json.Unmarshal(data, &result), fmt.Sprintf("response: %s", data))
	return result
}

func e2eClose(t *testing.T, response *http.Response) {
	t.Helper()
	require.NoError(t, response.Body.Close())
}
