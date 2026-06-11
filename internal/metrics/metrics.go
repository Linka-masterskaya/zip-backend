// Package metrics contains Prometheus metrics registration and helpers.
package metrics

import (
	"net/http"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	registry *prometheus.Registry

	httpRequestsTotal    *prometheus.CounterVec
	httpRequestDuration  *prometheus.HistogramVec
	httpRequestsInFlight prometheus.Gauge

	initOnce sync.Once
)

// Initialize registers application metrics once.
func Initialize() {
	initOnce.Do(func() {
		registry = prometheus.NewRegistry()

		httpRequestsTotal = prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "http_requests_total",
				Help: "Total number of HTTP requests processed",
			},
			[]string{"method", "path", "status"},
		)

		httpRequestDuration = prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "http_request_duration_seconds",
				Help:    "Latency of HTTP requests in seconds",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"method", "path", "status"},
		)

		httpRequestsInFlight = prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "http_requests_in_flight",
				Help: "Current number of HTTP requests being processed",
			},
		)

		registry.MustRegister(httpRequestsTotal)
		registry.MustRegister(httpRequestDuration)
		registry.MustRegister(httpRequestsInFlight)

		registry.MustRegister(collectors.NewGoCollector())
		registry.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	})
}

// NewHandler returns an HTTP handler for exposing Prometheus metrics.
func NewHandler() http.Handler {
	return promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
}

// IncRequests increments the total HTTP requests counter.
func IncRequests(method, path, status string) {
	httpRequestsTotal.WithLabelValues(method, path, status).Inc()
}

// ObserveDuration records the HTTP request duration.
func ObserveDuration(method, path, status string, seconds float64) {
	httpRequestDuration.WithLabelValues(method, path, status).Observe(seconds)
}

// IncInFlight increments the in-flight HTTP requests gauge.
func IncInFlight() {
	httpRequestsInFlight.Inc()
}

// DecInFlight decrements the in-flight HTTP requests gauge.
func DecInFlight() {
	httpRequestsInFlight.Dec()
}
