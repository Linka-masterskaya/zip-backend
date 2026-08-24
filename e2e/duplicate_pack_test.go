//go:build e2e

package e2e_test

import (
	"net/http"
	"testing"

	"github.com/Linka-masterskaya/zip-backend/internal/pack"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestE2E_DuplicatePack(t *testing.T) {
	pool := e2eDatabase(t)
	ownerID := e2eUser(t, pool, "duplicate owner")
	readerID := uuid.New()
	_, err := pool.Exec(t.Context(), `
		INSERT INTO users (id, org_id, display_name)
		SELECT $1, org_id, 'duplicate reader' FROM users WHERE id = $2`, readerID, ownerID)
	require.NoError(t, err)
	server := e2eServer(t, pool)
	ownerToken := e2eToken(t, ownerID, "defectologist")
	readerToken := e2eToken(t, readerID, "defectologist")

	ownerFolder := e2eCreateFolder(t, server, ownerToken, map[string]any{
		"section": "my", "kind": "folder", "name": "Исходные наборы",
	})
	readerFolder := e2eCreateFolder(t, server, readerToken, map[string]any{
		"section": "my", "kind": "folder", "name": "Мои копии",
	})
	libraryFolder := e2eCreateFolder(t, server, ownerToken, map[string]any{
		"section": "library", "kind": "folder", "name": "Общая библиотека",
	})

	source := e2eJSON[pack.Pack](
		t,
		e2eRequest(t, server, ownerToken, http.MethodPost, "/api/v1/packs", map[string]any{
			"title": "Опубликованный набор", "folder_id": ownerFolder.ID,
		}),
		http.StatusCreated,
	)
	privatePack := e2eJSON[pack.Pack](
		t,
		e2eRequest(t, server, ownerToken, http.MethodPost, "/api/v1/packs", map[string]any{
			"title": "Приватный набор", "folder_id": ownerFolder.ID,
		}),
		http.StatusCreated,
	)
	uploaded := e2eUploadMedia(t, server, ownerToken, tinyPNG())
	source = e2eJSON[pack.Pack](
		t,
		e2eRequest(t, server, ownerToken, http.MethodPut,
			"/api/v1/packs/"+source.ID.String()+"/config", packImageConfig(uploaded.ID)),
		http.StatusOK,
	)
	source = e2eJSON[pack.Pack](
		t,
		e2eRequest(t, server, ownerToken, http.MethodPost,
			"/api/v1/packs/"+source.ID.String()+"/publication",
			map[string]any{"library_folder_id": libraryFolder.ID}),
		http.StatusOK,
	)

	var storageBefore int64
	var mediaBefore int
	require.NoError(t, pool.QueryRow(t.Context(), `
		SELECT o.storage_used_bytes
		FROM organizations o JOIN users u ON u.org_id = o.id
		WHERE u.id = $1`, ownerID).Scan(&storageBefore))
	require.NoError(t, pool.QueryRow(t.Context(), `SELECT count(*) FROM media_files`).Scan(&mediaBefore))

	duplicated := e2eJSON[pack.Pack](
		t,
		e2eRequest(t, server, readerToken, http.MethodPost,
			"/api/v1/packs/"+source.ID.String()+"/duplicate",
			map[string]any{"folder_id": readerFolder.ID}),
		http.StatusCreated,
	)
	assert.NotEqual(t, source.ID, duplicated.ID)
	assert.Equal(t, readerID, duplicated.OwnerID)
	assert.Equal(t, readerFolder.ID, duplicated.FolderID)
	assert.Equal(t, "Опубликованный набор (копия)", duplicated.Title)
	assert.Equal(t, "draft", duplicated.Status)
	assert.Nil(t, duplicated.PublishedAt)
	assert.Nil(t, duplicated.LibraryFolderID)
	assert.JSONEq(t, string(source.Config), string(duplicated.Config))

	updatedCopy := e2eJSON[pack.Pack](
		t,
		e2eRequest(t, server, readerToken, http.MethodPatch,
			"/api/v1/packs/"+duplicated.ID.String(), map[string]any{"title": "Изменённая копия"}),
		http.StatusOK,
	)
	assert.Equal(t, "Изменённая копия", updatedCopy.Title)
	unchangedSource := e2eJSON[pack.Pack](
		t,
		e2eRequest(t, server, ownerToken, http.MethodGet,
			"/api/v1/packs/"+source.ID.String(), nil),
		http.StatusOK,
	)
	assert.Equal(t, "Опубликованный набор", unchangedSource.Title)

	missingFolder := e2eRequest(t, server, readerToken, http.MethodPost,
		"/api/v1/packs/"+source.ID.String()+"/duplicate", nil)
	assert.Equal(t, http.StatusBadRequest, missingFolder.StatusCode)
	e2eClose(t, missingFolder)
	privateResponse := e2eRequest(t, server, readerToken, http.MethodPost,
		"/api/v1/packs/"+privatePack.ID.String()+"/duplicate",
		map[string]any{"folder_id": readerFolder.ID})
	assert.Equal(t, http.StatusNotFound, privateResponse.StatusCode)
	e2eClose(t, privateResponse)

	var storageAfter int64
	var mediaAfter int
	require.NoError(t, pool.QueryRow(t.Context(), `
		SELECT o.storage_used_bytes
		FROM organizations o JOIN users u ON u.org_id = o.id
		WHERE u.id = $1`, ownerID).Scan(&storageAfter))
	require.NoError(t, pool.QueryRow(t.Context(), `SELECT count(*) FROM media_files`).Scan(&mediaAfter))
	assert.Equal(t, storageBefore, storageAfter)
	assert.Equal(t, mediaBefore, mediaAfter)
}
