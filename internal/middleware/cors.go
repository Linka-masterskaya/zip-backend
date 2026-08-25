package middleware

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/Linka-masterskaya/zip-backend/internal/config"
)

func CORSMiddleware(cfg config.CORSConfig) func(http.Handler) http.Handler {
	methods := strings.Join(cfg.AllowMethods, ", ")
	headers := strings.Join(cfg.AllowHeaders, ", ")
	origins := make(map[string]struct{}, len(cfg.AllowOrigins))
	for _, o := range cfg.AllowOrigins {
		if o = strings.TrimSpace(o); o != "" {
			origins[o] = struct{}{}
		}
	}

	allowCreds := strconv.FormatBool(cfg.AllowCredentials)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if _, ok := origins[origin]; ok {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Credentials", allowCreds)
				w.Header().Add("Vary", "Origin")
			}
			w.Header().Set("Access-Control-Allow-Headers", headers)
			w.Header().Set("Access-Control-Allow-Methods", methods)

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
