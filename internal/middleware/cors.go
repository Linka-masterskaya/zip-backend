package middleware

import (
	"net/http"
	"strings"
)

func CORSMiddleware(allowedOrigins []string) func(http.Handler) http.Handler {
	origins := make(map[string]struct{}, len(allowedOrigins))
	for _, o := range allowedOrigins {
		if o = strings.TrimSpace(o); o != "" {
			origins[o] = struct{}{}
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if _, ok := origins[origin]; ok {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
			}
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Request-Id")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
