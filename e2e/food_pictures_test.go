//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/Linka-masterskaya/zip-backend/internal/folder"
	"github.com/Linka-masterskaya/zip-backend/internal/pack"
	"github.com/Linka-masterskaya/zip-backend/internal/picturebank"
	"github.com/Linka-masterskaya/zip-backend/pkg/linka"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestE2E_FoodPicturesFolderAssignedToStudent(t *testing.T) {
	pool := e2eDatabase(t)
	ownerID := e2eUser(t, pool, "food-pictures-owner")
	source := newFoodPictureSource()
	pictureHandler := picturebank.NewHandler(picturebank.NewService(source))
	server := e2eServer(t, pool, pictureHandler)
	token := e2eToken(t, ownerID, "defectologist")

	searches := []struct {
		query string
		count int
	}{
		{query: "котлета", count: 1},
		{query: "яблоки", count: 2},
		{query: "суп", count: 1},
		{query: "каша", count: 1},
	}
	selected := make([]picturebank.Picture, 0, 5)
	for _, search := range searches {
		pictures := e2eJSON[[]picturebank.Picture](
			t,
			e2eRequest(t, server, token, http.MethodGet,
				"/api/v1/pictures/search?query="+url.QueryEscape(search.query), nil),
			http.StatusOK,
		)
		require.Len(t, pictures, search.count, "search %q", search.query)
		selected = append(selected, pictures...)
	}
	require.Len(t, selected, 5)
	assert.Equal(t, []string{
		"Котлета", "Зелёное яблоко", "Красное яблоко", "Суп", "Гречневая каша",
	}, pictureNames(selected))

	for _, picture := range selected {
		response := e2eRequest(
			t, server, token, http.MethodGet,
			"/api/v1/pictures/"+picture.ID+"/content", nil,
		)
		body := e2eBody(t, response, http.StatusOK)
		assert.Equal(t, "image/png", response.Header.Get("Content-Type"))
		assert.Equal(t, source.images[picture.ID], body)
	}

	foodFolder := e2eCreateFolder(t, server, token, map[string]any{
		"section": "my", "kind": "folder", "name": "Еда",
	})
	createdPack := e2eJSON[pack.Pack](
		t,
		e2eRequest(t, server, token, http.MethodPost, "/api/v1/packs", map[string]any{
			"title": "Еда — 4 картинки", "folder_id": foodFolder.ID,
		}),
		http.StatusCreated,
	)

	// The user viewed five candidates and deliberately left the red apple out.
	usedPictures := []picturebank.Picture{selected[0], selected[1], selected[3], selected[4]}
	configuredPack := e2eJSON[pack.Pack](
		t,
		e2eRequest(t, server, token, http.MethodPut,
			"/api/v1/packs/"+createdPack.ID.String()+"/config",
			foodPackConfig(usedPictures)),
		http.StatusOK,
	)
	configured := decodeLinkaConfig(t, configuredPack.Config)
	require.Len(t, configured.Blocks, 1)
	require.Len(t, configured.Blocks[0].Elements, 4)
	assert.Equal(t, pictureIDs(usedPictures), elementPictureIDs(configured.Blocks[0].Elements))
	assert.NotContains(t, string(configuredPack.Config), selected[2].ID)

	foodContents := e2eFolderContents(t, server, token, "my", foodFolder.ID)
	require.Len(t, foodContents.Items, 1)
	assert.Equal(t, folder.ContentItem{
		Type: "pack", ID: createdPack.ID, Name: "Еда — 4 картинки",
		UpdatedAt: foodContents.Items[0].UpdatedAt,
	}, foodContents.Items[0])

	student := e2eCreateStudent(
		t, server, token, "food.journey.student@example.com", "Тестовый ученик",
	)
	studentShelf := e2eCreateFolder(t, server, token, map[string]any{
		"section": "students", "kind": "student",
		"student_id": student.ID, "name": student.Name,
	})
	assignments := e2eJSON[[]pack.Adaptation](
		t,
		e2eRequest(t, server, token, http.MethodPost,
			"/api/v1/packs/"+createdPack.ID.String()+"/students",
			map[string]any{"student_ids": []uuid.UUID{student.ID}}),
		http.StatusOK,
	)
	require.Len(t, assignments, 1)
	assert.Equal(t, student.ID, assignments[0].StudentID)
	assert.Equal(t, createdPack.ID, assignments[0].PackID)
	assert.JSONEq(t, string(configuredPack.Config), string(assignments[0].Config))

	shelfContents := e2eFolderContents(t, server, token, "students", studentShelf.ID)
	require.Len(t, shelfContents.Items, 1)
	assert.Equal(t, "pack", shelfContents.Items[0].Type)
	assert.Equal(t, createdPack.ID, shelfContents.Items[0].ID)
	assert.Equal(t, "Еда — 4 картинки", shelfContents.Items[0].Name)

	var mediaCount int
	var storageUsed int64
	require.NoError(t, pool.QueryRow(t.Context(), `SELECT count(*) FROM media_files`).Scan(&mediaCount))
	require.NoError(t, pool.QueryRow(t.Context(), `
		SELECT o.storage_used_bytes
		FROM organizations o JOIN users u ON u.org_id = o.id
		WHERE u.id = $1`, ownerID).Scan(&storageUsed))
	assert.Zero(t, mediaCount, "Pictures Bank references must not create local media")
	assert.Zero(t, storageUsed, "Pictures Bank references must not consume local storage")
}

type foodPictureSource struct {
	searches map[string][]picturebank.Picture
	images   map[string][]byte
}

func newFoodPictureSource() *foodPictureSource {
	food := picturebank.Category{ID: "food", Name: "Еда"}
	pictures := []picturebank.Picture{
		{ID: "1450d203-9c5d-43d0-9d80-22cc7ea2e1ab", Name: "Котлета", MIMEType: "image/png", Categories: []picturebank.Category{food}},
		{ID: "ec940ea8-173c-404d-aadd-47793149f19f", Name: "Зелёное яблоко", MIMEType: "image/png", Categories: []picturebank.Category{food}},
		{ID: "e863a9ad-f1de-4289-b302-fb8f4b3a00c8", Name: "Красное яблоко", MIMEType: "image/png", Categories: []picturebank.Category{food}},
		{ID: "97bca0bb-dbb5-4810-8e91-569c1a4b08e7", Name: "Суп", MIMEType: "image/png", Categories: []picturebank.Category{food}},
		{ID: "fba331be-9873-4a6d-97f8-52f2b6b689db", Name: "Гречневая каша", MIMEType: "image/png", Categories: []picturebank.Category{food}},
	}
	images := make(map[string][]byte, len(pictures))
	for index, picture := range pictures {
		images[picture.ID] = append(append([]byte(nil), tinyPNG()...), byte(index+1))
	}
	return &foodPictureSource{
		searches: map[string][]picturebank.Picture{
			"котлета": pictures[0:1], "яблоки": pictures[1:3],
			"суп": pictures[3:4], "каша": pictures[4:5],
		},
		images: images,
	}
}

func (s *foodPictureSource) Categories(context.Context) ([]picturebank.Category, error) {
	return []picturebank.Category{{ID: "food", Name: "Еда"}}, nil
}

func (s *foodPictureSource) Search(_ context.Context, query string) ([]picturebank.Picture, error) {
	return append([]picturebank.Picture(nil), s.searches[query]...), nil
}

func (s *foodPictureSource) Image(_ context.Context, pictureID string) (*picturebank.Image, error) {
	data, ok := s.images[pictureID]
	if !ok {
		return nil, picturebank.ErrPictureNotFound
	}
	return &picturebank.Image{Data: append([]byte(nil), data...), ContentType: "image/png"}, nil
}

func foodPackConfig(pictures []picturebank.Picture) map[string]any {
	elements := make([]any, 0, len(pictures))
	for _, picture := range pictures {
		elements = append(elements, map[string]any{
			"id":                "food-picture-" + picture.ID,
			"kind":              "image",
			"source_picture_id": picture.ID,
			"value":             picture.Name,
		})
	}
	return map[string]any{
		"metadata": map[string]any{"version": "2.0", "title": "Еда — 4 картинки"},
		"settings": map[string]any{"columns": 2, "rows": 2},
		"blocks": []any{map[string]any{
			"id": "food-grid", "type": "grid", "elements": elements,
		}},
	}
}

func pictureNames(pictures []picturebank.Picture) []string {
	names := make([]string, 0, len(pictures))
	for _, picture := range pictures {
		names = append(names, picture.Name)
	}
	return names
}

func pictureIDs(pictures []picturebank.Picture) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(pictures))
	for _, picture := range pictures {
		ids = append(ids, uuid.MustParse(picture.ID))
	}
	return ids
}

func elementPictureIDs(elements []linka.Element) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(elements))
	for _, element := range elements {
		if element.SourcePictureID != nil {
			ids = append(ids, *element.SourcePictureID)
		}
	}
	return ids
}

func decodeLinkaConfig(t *testing.T, data json.RawMessage) linka.Config {
	t.Helper()
	var config linka.Config
	require.NoError(t, json.Unmarshal(data, &config))
	return config
}
