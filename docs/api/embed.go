// Package api publishes the embedded OpenAPI contract and Swagger UI.
package api

import (
	_ "embed"
	"net/http"
	"strings"

	swaggerFiles "github.com/swaggo/files"
)

const (
	docsPath    = "/api/v1/docs"
	openAPIPath = "/api/v1/docs/openapi.yaml"
	assetsPath  = "/api/v1/docs/assets/"
)

//go:embed openapi.yaml
var openAPI []byte

// RegisterRoutes registers documentation endpoints. Call it only when docs are enabled.
func RegisterRoutes(mux interface {
	Handle(pattern string, handler http.Handler)
}) {
	mux.Handle("GET "+docsPath, http.HandlerFunc(serveUI))
	mux.Handle("GET "+openAPIPath, http.HandlerFunc(serveOpenAPI))
	mux.Handle("GET "+assetsPath+"{file}", http.HandlerFunc(serveAsset))
}

func serveOpenAPI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	if _, err := w.Write(openAPI); err != nil {
		return
	}
}

func serveUI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := w.Write([]byte(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Linka API</title>
  <link rel="stylesheet" href="/api/v1/docs/assets/swagger-ui.css">
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="/api/v1/docs/assets/swagger-ui-bundle.js"></script>
  <script>
    window.onload = function () {
      window.ui = SwaggerUIBundle({
        url: "/api/v1/docs/openapi.yaml",
        dom_id: "#swagger-ui",
        deepLinking: true,
        presets: [SwaggerUIBundle.presets.apis],
        persistAuthorization: true
      });
    };
  </script>
</body>
</html>`)); err != nil {
		return
	}
}

func serveAsset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("file")
	if name != "swagger-ui.css" && name != "swagger-ui-bundle.js" {
		http.NotFound(w, r)
		return
	}

	clone := r.Clone(r.Context())
	clone.URL.Path = "/" + strings.TrimPrefix(name, "/")
	swaggerFiles.Handler.ServeHTTP(w, clone)
}
