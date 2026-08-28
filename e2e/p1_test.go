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
	"net/url"
	"testing"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/cryptox"
	"github.com/Linka-masterskaya/zip-backend/internal/folder"
	"github.com/Linka-masterskaya/zip-backend/internal/httpapi"
	"github.com/Linka-masterskaya/zip-backend/internal/media"
	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
	"github.com/Linka-masterskaya/zip-backend/internal/pack"
	"github.com/Linka-masterskaya/zip-backend/internal/picturebank"
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
	readerID := e2eUserInOrg(t, pool, ownerID, "reader")
	foreignUserID := e2eUser(t, pool, "foreign")
	server := e2eServer(t, pool)
	ownerToken := e2eToken(t, ownerID, "defectologist")
	readerToken := e2eToken(t, readerID, "defectologist")
	foreignToken := e2eToken(t, foreignUserID, "defectologist")

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
	foreignMedia := e2eUploadMedia(t, server, foreignToken, tinyPNG())
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

	secondUpload := e2eUploadMediaNamed(t, server, ownerToken, "cat.png", testPNG(9))

	t.Run("media list is scoped, searchable, and cursor-paginated", func(t *testing.T) {
		all := e2eJSON[media.ListPage](
			t,
			e2eRequest(t, server, ownerToken, http.MethodGet, "/api/v1/media", nil),
			http.StatusOK,
		)
		assert.ElementsMatch(t, []uuid.UUID{uploaded.ID, secondUpload.ID}, mediaIDs(all.Items),
			"only the caller's own organization media, foreign media excluded")

		byName := e2eJSON[media.ListPage](
			t,
			e2eRequest(t, server, ownerToken, http.MethodGet, "/api/v1/media?query=cat", nil),
			http.StatusOK,
		)
		require.Len(t, byName.Items, 1)
		assert.Equal(t, secondUpload.ID, byName.Items[0].ID)
		assert.Equal(t, "cat.png", byName.Items[0].Name)
		assert.Equal(t, "image", byName.Items[0].MediaType)

		firstPage := e2eJSON[media.ListPage](
			t,
			e2eRequest(t, server, ownerToken, http.MethodGet, "/api/v1/media?limit=1", nil),
			http.StatusOK,
		)
		require.Len(t, firstPage.Items, 1)
		require.NotEmpty(t, firstPage.NextCursor)

		secondPage := e2eJSON[media.ListPage](
			t,
			e2eRequest(t, server, ownerToken, http.MethodGet,
				"/api/v1/media?limit=1&cursor="+url.QueryEscape(firstPage.NextCursor), nil),
			http.StatusOK,
		)
		require.Len(t, secondPage.Items, 1)
		assert.NotEqual(t, firstPage.Items[0].ID, secondPage.Items[0].ID, "second page does not repeat the first")
		assert.ElementsMatch(t,
			[]uuid.UUID{uploaded.ID, secondUpload.ID},
			mediaIDs(append(firstPage.Items, secondPage.Items...)),
			"both pages together cover every owned file exactly once",
		)
		assert.Empty(t, secondPage.NextCursor, "no more pages left")
	})

	updatedPack := e2eJSON[pack.Pack](
		t,
		e2eRequest(
			t,
			server,
			ownerToken,
			http.MethodPatch,
			"/api/v1/packs/"+createdPack.ID.String(),
			map[string]any{
				"title": "Автоматизация звука Р", "age": 5,
				"difficulty": "medium", "goals": []string{"speech"}, "notes": "Домашнее задание",
			},
		),
		http.StatusOK,
	)
	assert.Equal(t, "Автоматизация звука Р", updatedPack.Title)
	require.NotNil(t, updatedPack.Age)
	assert.Equal(t, 5, *updatedPack.Age)

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
			"/api/v1/sections/my/contents?parent_id="+child.ID.String(),
			nil,
		),
		http.StatusOK,
	)
	require.Len(t, contents.Items, 1)
	assert.Equal(t, "pack", contents.Items[0].Type)
	assert.Equal(t, createdPack.ID, contents.Items[0].ID)
	require.NotNil(t, contents.Items[0].Age)
	assert.Equal(t, 5, *contents.Items[0].Age)
	require.NotNil(t, contents.Items[0].Difficulty)
	assert.Equal(t, "medium", *contents.Items[0].Difficulty)

	t.Run("section root is addressable without a folder id", func(t *testing.T) {
		// «Мои наборы» — пункт меню, а не папка: id для корня не существует.
		root := e2eJSON[folder.ContentsPage](
			t,
			e2eRequest(t, server, ownerToken, http.MethodGet,
				"/api/v1/sections/my/contents", nil),
			http.StatusOK,
		)
		names := make([]string, 0, len(root.Items))
		for _, item := range root.Items {
			assert.Equal(t, "folder", item.Type)
			names = append(names, item.Name)
		}
		assert.Contains(t, names, "Мои материалы")
		assert.NotContains(t, names, "Звуки", "вложенная папка не должна попадать в корень")
		assert.NotContains(t, names, "Общая библиотека", "чужой раздел не должен попадать в корень")

		unknown := e2eRequest(t, server, ownerToken, http.MethodGet,
			"/api/v1/sections/nope/contents", nil)
		assert.Equal(t, http.StatusBadRequest, unknown.StatusCode)
		e2eClose(t, unknown)
	})

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

		inaccessible := e2eRequest(
			t,
			server,
			foreignToken,
			http.MethodGet,
			"/api/v1/packs/"+createdPack.ID.String(),
			nil,
		)
		assert.Equal(
			t,
			http.StatusNotFound,
			inaccessible.StatusCode,
			"published pack must not be accessible outside its organization",
		)
		e2eClose(t, inaccessible)

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

	students := e2eJSON[student.ListResult](
		t,
		e2eRequest(t, server, ownerToken, http.MethodGet, "/api/v1/students", nil),
		http.StatusOK,
	)
	require.Len(t, students.Items, 1)
	assert.Equal(t, createdStudent.ID, students.Items[0].ID)

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

func TestE2E_RealPackLifecycle(t *testing.T) {
	pool := e2eDatabase(t)
	ownerID := e2eUser(t, pool, "lifecycle-owner")
	server := e2eServer(t, pool)
	token := e2eToken(t, ownerID, "defectologist")

	myFolder := e2eCreateFolder(t, server, token, map[string]any{
		"section": "my", "kind": "folder", "name": "Рабочие материалы",
	})
	libraryFolder := e2eCreateFolder(t, server, token, map[string]any{
		"section": "library", "kind": "folder", "name": "Общий каталог",
	})
	createdPack := e2eJSON[pack.Pack](
		t,
		e2eRequest(t, server, token, http.MethodPost, "/api/v1/packs", map[string]any{
			"title": "Реальный набор", "folder_id": myFolder.ID,
		}),
		http.StatusCreated,
	)

	first := e2eUploadMedia(t, server, token, testPNG(1))
	second := e2eUploadMedia(t, server, token, testPNG(2))
	third := e2eUploadMedia(t, server, token, testPNG(3))
	e2eJSON[pack.Pack](
		t,
		e2eRequest(t, server, token, http.MethodPut,
			"/api/v1/packs/"+createdPack.ID.String()+"/config",
			packMediaConfig(first.ID, second.ID, third.ID)),
		http.StatusOK,
	)

	response := e2eRequest(
		t, server, token, http.MethodDelete, "/api/v1/media/"+second.ID.String(), nil,
	)
	assert.Equal(t, http.StatusConflict, response.StatusCode, "used media must be protected")
	e2eClose(t, response)

	e2eJSON[pack.Pack](
		t,
		e2eRequest(t, server, token, http.MethodPut,
			"/api/v1/packs/"+createdPack.ID.String()+"/config",
			packMediaConfig(first.ID)),
		http.StatusOK,
	)
	for _, mediaID := range []uuid.UUID{second.ID, third.ID} {
		response = e2eRequest(
			t, server, token, http.MethodDelete, "/api/v1/media/"+mediaID.String(), nil,
		)
		assert.Equal(t, http.StatusNoContent, response.StatusCode)
		e2eClose(t, response)
	}

	replacement := e2eUploadMedia(t, server, token, testPNG(4))
	e2eJSON[pack.Pack](
		t,
		e2eRequest(t, server, token, http.MethodPut,
			"/api/v1/packs/"+createdPack.ID.String()+"/config",
			packMediaConfig(first.ID, replacement.ID)),
		http.StatusOK,
	)
	exported := e2eRequest(
		t, server, token, http.MethodGet,
		"/api/v1/packs/"+createdPack.ID.String()+"/export", nil,
	)
	exportBytes := e2eBody(t, exported, http.StatusOK)
	assert.NotEmpty(t, exportBytes)
	importedPack := e2eImportPack(t, server, token, myFolder.ID, exportBytes)
	assert.Contains(t, string(importedPack.Config), first.ID.String())
	assert.Contains(t, string(importedPack.Config), replacement.ID.String())
	response = e2eRequest(
		t, server, token, http.MethodDelete, "/api/v1/packs/"+createdPack.ID.String(), nil,
	)
	assert.Equal(t, http.StatusNoContent, response.StatusCode)
	e2eClose(t, response)
	createdPack = importedPack

	firstStudent := e2eCreateStudent(t, server, token, "one@example.com", "Первый ребёнок")
	secondStudent := e2eCreateStudent(t, server, token, "two@example.com", "Второй ребёнок")
	firstShelf := e2eCreateFolder(t, server, token, map[string]any{
		"section": "students", "kind": "student",
		"student_id": firstStudent.ID, "name": firstStudent.Name,
	})
	secondShelf := e2eCreateFolder(t, server, token, map[string]any{
		"section": "students", "kind": "student",
		"student_id": secondStudent.ID, "name": secondStudent.Name,
	})

	assignments := e2eJSON[[]pack.Adaptation](
		t,
		e2eRequest(t, server, token, http.MethodPost,
			"/api/v1/packs/"+createdPack.ID.String()+"/students",
			map[string]any{"student_ids": []uuid.UUID{firstStudent.ID, secondStudent.ID}}),
		http.StatusOK,
	)
	require.Len(t, assignments, 2)
	for _, shelfID := range []uuid.UUID{firstShelf.ID, secondShelf.ID} {
		contents := e2eFolderContents(t, server, token, "students", shelfID)
		require.Len(t, contents.Items, 1)
		assert.Equal(t, createdPack.ID, contents.Items[0].ID)
		assert.Equal(t, "Импортированный набор", contents.Items[0].Name)
	}

	e2eJSON[pack.Pack](
		t,
		e2eRequest(t, server, token, http.MethodPost,
			"/api/v1/packs/"+createdPack.ID.String()+"/publication",
			map[string]any{"library_folder_id": libraryFolder.ID}),
		http.StatusOK,
	)
	libraryContents := e2eFolderContents(t, server, token, "library", libraryFolder.ID)
	require.Len(t, libraryContents.Items, 1)
	assert.Equal(t, createdPack.ID, libraryContents.Items[0].ID)
	assert.True(t, libraryContents.Items[0].Published)

	response = e2eRequest(
		t, server, token, http.MethodDelete, "/api/v1/packs/"+createdPack.ID.String(), nil,
	)
	assert.Equal(t, http.StatusConflict, response.StatusCode)
	e2eClose(t, response)
	response = e2eRequest(
		t, server, token, http.MethodDelete,
		"/api/v1/packs/"+createdPack.ID.String()+"/publication", nil,
	)
	assert.Equal(t, http.StatusNoContent, response.StatusCode)
	e2eClose(t, response)
	response = e2eRequest(
		t, server, token, http.MethodDelete, "/api/v1/packs/"+createdPack.ID.String(), nil,
	)
	assert.Equal(t, http.StatusNoContent, response.StatusCode)
	e2eClose(t, response)

	for _, shelfID := range []uuid.UUID{firstShelf.ID, secondShelf.ID} {
		assert.Empty(t, e2eFolderContents(t, server, token, "students", shelfID).Items)
	}
	assert.Empty(t, e2eFolderContents(t, server, token, "library", libraryFolder.ID).Items)
	for _, mediaID := range []uuid.UUID{first.ID, replacement.ID} {
		response = e2eRequest(
			t, server, token, http.MethodDelete, "/api/v1/media/"+mediaID.String(), nil,
		)
		assert.Equal(t, http.StatusNoContent, response.StatusCode)
		e2eClose(t, response)
		response = e2eRequest(
			t, server, token, http.MethodGet, "/api/v1/media/"+mediaID.String(), nil,
		)
		assert.Equal(t, http.StatusNotFound, response.StatusCode)
		e2eClose(t, response)
	}
	var storageUsed int64
	require.NoError(t, pool.QueryRow(t.Context(), `
		SELECT o.storage_used_bytes
		FROM organizations o JOIN users u ON u.org_id = o.id
		WHERE u.id = $1`, ownerID).Scan(&storageUsed))
	assert.Zero(t, storageUsed)
	var packsCount, adaptationsCount, usagesCount, mediaCount int
	require.NoError(t, pool.QueryRow(t.Context(), `
		SELECT
			(SELECT count(*) FROM packs),
			(SELECT count(*) FROM pack_adaptations),
			(SELECT count(*) FROM media_usages),
			(SELECT count(*) FROM media_files)`,
	).Scan(&packsCount, &adaptationsCount, &usagesCount, &mediaCount))
	assert.Zero(t, packsCount)
	assert.Zero(t, adaptationsCount)
	assert.Zero(t, usagesCount)
	assert.Zero(t, mediaCount)

	for _, path := range []string{
		"/api/v1/folders/" + firstShelf.ID.String(),
		"/api/v1/folders/" + secondShelf.ID.String(),
		"/api/v1/students/" + firstStudent.ID.String(),
		"/api/v1/students/" + secondStudent.ID.String(),
		"/api/v1/folders/" + myFolder.ID.String(),
		"/api/v1/folders/" + libraryFolder.ID.String(),
	} {
		response = e2eRequest(t, server, token, http.MethodDelete, path, nil)
		assert.Equal(t, http.StatusNoContent, response.StatusCode, path)
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

func e2eServer(
	t *testing.T,
	pool *pgxpool.Pool,
	pictureHandlers ...*picturebank.Handler,
) *httptest.Server {
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
	studentHandler := student.NewHandler(student.NewService(student.NewRepository(pool), crypto, objectStorage, mediaService))

	mux := http.NewServeMux()
	auth := middleware.NewAuthMW([]byte(e2eJWTSecret))
	passthrough := func(next http.Handler) http.Handler { return next }
	httpapi.RegisterPackRoutes(mux, auth, passthrough, httpapi.PackHandlers{
		Pack: packHandler, Content: contentHandler,
	})
	httpapi.RegisterMediaRoutes(mux, auth, passthrough, httpapi.MediaHandlers{Media: mediaHandler})
	httpapi.RegisterFolderRoutes(mux, auth, passthrough, httpapi.FolderHandlers{Folder: folderHandler})
	httpapi.RegisterStudentRoutes(mux, auth, passthrough, httpapi.StudentHandlers{Student: studentHandler})
	for _, pictureHandler := range pictureHandlers {
		httpapi.RegisterPictureBankRoutes(mux, auth, passthrough, pictureHandler)
	}
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
	return e2eUploadMediaNamed(t, server, token, "pixel.png", data)
}

func e2eUploadMediaNamed(
	t *testing.T,
	server *httptest.Server,
	token, filename string,
	data []byte,
) media.Response {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
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
	return packMediaConfig(mediaID)
}

func packMediaConfig(mediaIDs ...uuid.UUID) map[string]any {
	elements := make([]any, 0, len(mediaIDs))
	for index, mediaID := range mediaIDs {
		elements = append(elements, map[string]any{
			"id": fmt.Sprintf("image-%d", index+1), "kind": "image", "media_id": mediaID,
		})
	}
	return map[string]any{
		"metadata": map[string]any{"version": "2.0"},
		"settings": map[string]any{"columns": 1, "rows": 1},
		"blocks": []any{map[string]any{
			"id": "block-1", "type": "grid", "elements": elements,
		}},
	}
}

func testPNG(marker byte) []byte {
	data := append([]byte(nil), tinyPNG()...)
	return append(data, marker)
}

func mediaIDs(items []media.File) []uuid.UUID {
	ids := make([]uuid.UUID, len(items))
	for i, item := range items {
		ids[i] = item.ID
	}
	return ids
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

func e2eUserInOrg(
	t *testing.T,
	pool *pgxpool.Pool,
	orgMemberID uuid.UUID,
	name string,
) uuid.UUID {
	t.Helper()
	userID := uuid.New()
	result, err := pool.Exec(
		context.Background(),
		`INSERT INTO users (id, org_id, display_name)
		 SELECT $1, org_id, $3 FROM users WHERE id = $2`,
		userID,
		orgMemberID,
		name,
	)
	require.NoError(t, err)
	require.Equal(t, int64(1), result.RowsAffected())
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
	jwtToken := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	jwtToken.Header["typ"] = "access"
	token, err := jwtToken.SignedString([]byte(e2eJWTSecret))
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

func e2eCreateStudent(
	t *testing.T,
	server *httptest.Server,
	token, email, name string,
) student.Student {
	t.Helper()
	return e2eJSON[student.Student](
		t,
		e2eRequest(t, server, token, http.MethodPost, "/api/v1/students", map[string]any{
			"email": email, "name": name,
		}),
		http.StatusCreated,
	)
}

func e2eFolderContents(
	t *testing.T,
	server *httptest.Server,
	token string,
	section string,
	folderID uuid.UUID,
) folder.ContentsPage {
	t.Helper()
	return e2eJSON[folder.ContentsPage](
		t,
		e2eRequest(t, server, token, http.MethodGet,
			"/api/v1/sections/"+section+"/contents?parent_id="+folderID.String(), nil),
		http.StatusOK,
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
