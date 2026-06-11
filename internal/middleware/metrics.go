package middleware

import (
	"net/http"
	"strconv"
	"time"

	"github.com/Linka-masterskaya/zip-backend/internal/metrics"
)

type responseWriterWrapper struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriterWrapper) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func Metrics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metrics.IncInFlight()
		defer metrics.DecInFlight()

		start := time.Now()
		wrapper := &responseWriterWrapper{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(wrapper, r)

		duration := time.Since(start).Seconds()
		statusStr := strconv.Itoa(wrapper.statusCode)

		path := r.Pattern
		if path == "" {
			path = "unknown"
		}

		metrics.IncRequests(r.Method, path, statusStr)
		metrics.ObserveDuration(r.Method, path, statusStr, duration)
	})
}
