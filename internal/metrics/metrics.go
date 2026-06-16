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

func NewHandler() http.Handler {
	return promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
}

func IncRequests(method, path, status string) {
	httpRequestsTotal.WithLabelValues(method, path, status).Inc()
}

func ObserveDuration(method, path, status string, seconds float64) {
	httpRequestDuration.WithLabelValues(method, path, status).Observe(seconds)
}

func IncInFlight() {
	httpRequestsInFlight.Inc()
}

func DecInFlight() {
	httpRequestsInFlight.Dec()
}
