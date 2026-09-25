package folder

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContentsParsesIsFavorite(t *testing.T) {
	tests := []struct {
		query string
		want  *bool
	}{
		{"", nil},
		{"?is_favorite=true", boolPtr(true)},
		{"?is_favorite=false", boolPtr(false)},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			var got ContentsInput
			service := &fakeFolderService{contentsFn: func(_ context.Context, in ContentsInput) (*ContentsPage, error) {
				got = in
				return &ContentsPage{}, nil
			}}
			req := httptest.NewRequestWithContext(
				context.Background(), http.MethodGet, "/api/v1/sections/my/contents"+tt.query, nil)
			req.SetPathValue("section", "my")
			require.NoError(t, NewHandler(service).Contents(httptest.NewRecorder(), req))

			if tt.want == nil {
				assert.Nil(t, got.IsFavorite)
				return
			}
			require.NotNil(t, got.IsFavorite)
			assert.Equal(t, *tt.want, *got.IsFavorite)
		})
	}
}

func TestContentsRejectsMalformedIsFavorite(t *testing.T) {
	req := httptest.NewRequestWithContext(
		context.Background(), http.MethodGet, "/api/v1/sections/my/contents?is_favorite=yes", nil)
	req.SetPathValue("section", "my")

	err := NewHandler(&fakeFolderService{}).Contents(httptest.NewRecorder(), req)
	require.Error(t, err)
}

func boolPtr(v bool) *bool { return &v }
