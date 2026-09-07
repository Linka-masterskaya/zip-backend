package picturebank

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSourceAdaptersShareReadContract(t *testing.T) {
	pictureID := uuid.New()
	imageData := testPNG()
	type adapterCase struct {
		categoryID string
		factory    func(*testing.T) Source
	}
	factories := map[string]adapterCase{
		"external": {categoryID: "animals", factory: func(t *testing.T) Source {
			t.Helper()
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/category/all":
					writeContractJSON(t, w, []Category{{ID: "animals", Name: "Животные"}})
				case "/picture/search", "/picture/search/":
					writeContractJSON(t, w, []Picture{{
						ID: pictureID.String(), Name: "Кот", MIMEType: "image/png",
						Categories: []Category{{ID: "animals", Name: "Животные"}},
					}})
				case "/picture/" + pictureID.String() + "/buffer":
					w.Header().Set("Content-Type", "image/png")
					_, err := w.Write(imageData)
					require.NoError(t, err)
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(upstream.Close)
			baseURL, err := url.Parse(upstream.URL)
			require.NoError(t, err)
			return newClient(
				testPicturesConfig(), baseURL, upstream.Client(),
				&fakeDistributedLimiter{allowed: true},
			)
		}},
		"local": {categoryID: "Животные", factory: func(t *testing.T) Source {
			t.Helper()
			metadata := localPictureMetadata{
				ID: pictureID, Category: "Животные", Title: "Кот",
				MIMEType: "image/png", SizeBytes: int64(len(imageData)),
				MinIOKey: LocalObjectPrefix + "/" + pictureID.String(),
			}
			source, err := newLocalSource(
				&fakeLocalRepository{categories: []string{"Животные"}, pictures: []localPictureMetadata{metadata}},
				&fakeLocalStorage{objects: map[string][]byte{metadata.MinIOKey: imageData}},
				int64(len(imageData))+1,
			)
			require.NoError(t, err)
			return source
		}},
	}

	for name, adapter := range factories {
		t.Run(name, func(t *testing.T) {
			source := adapter.factory(t)

			categories, err := source.Categories(t.Context())
			require.NoError(t, err)
			require.Len(t, categories, 1)
			assert.Equal(t, "Животные", categories[0].Name)
			assert.Equal(t, adapter.categoryID, categories[0].ID)

			for _, query := range []string{"кот", "100%_off"} {
				pictures, err := source.Search(t.Context(), query)
				require.NoError(t, err)
				require.Len(t, pictures, 1)
				assert.Equal(t, pictureID.String(), pictures[0].ID)
				assert.Equal(t, "Кот", pictures[0].Name)
				assert.Equal(t, "image/png", pictures[0].MIMEType)
				require.Len(t, pictures[0].Categories, 1)
				assert.Equal(t, "Животные", pictures[0].Categories[0].Name)
				assert.Equal(t, adapter.categoryID, pictures[0].Categories[0].ID)
			}

			image, err := source.Image(t.Context(), pictureID.String())
			require.NoError(t, err)
			assert.Equal(t, imageData, image.Data)
			assert.Equal(t, "image/png", image.ContentType)

			_, err = source.Image(t.Context(), uuid.New().String())
			require.ErrorIs(t, err, ErrPictureNotFound)
		})
	}
}

func writeContractJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	require.NoError(t, json.NewEncoder(w).Encode(value))
}
