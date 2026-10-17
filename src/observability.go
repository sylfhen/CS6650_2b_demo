package main

import (
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// RED metrics: Rate, Errors, Duration, labeled by route template (for example
// /products/:productId) rather than the raw path, so that each product ID
// does not become its own time series.
var (
	httpRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Requests handled, by method, route template and status code (RED: rate).",
	}, []string{"method", "route", "status"})

	httpErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "http_request_errors_total",
		Help: "Requests that returned a non-2xx status code (RED: errors).",
	}, []string{"method", "route", "status"})

	// Handlers take about 0.1 ms, so the buckets start well below the
	// Prometheus defaults (which begin at 5 ms) to keep p50/p95 meaningful.
	httpDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "Time spent handling each request inside the server (RED: duration).",
		Buckets: []float64{0.0001, 0.00025, 0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1},
	}, []string{"method", "route"})
)

const metricsPath = "/metrics"

// newJSONLogger writes one JSON object per line to stdout, which Docker and
// the ECS awslogs driver both capture.
func newJSONLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, nil))
}

func routeLabel(c *gin.Context) string {
	if r := c.FullPath(); r != "" {
		return r
	}
	return "unmatched"
}

// observability records the RED metrics for every request and, if enabled,
// writes one structured log line with the same data. It must be registered
// before the recovery middleware so it also sees 500s produced by panics.
func observability(logger *slog.Logger, logRequests bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.URL.Path == metricsPath {
			c.Next()
			return
		}
		start := time.Now()
		c.Next()
		elapsed := time.Since(start)

		method := c.Request.Method
		route := routeLabel(c)
		status := c.Writer.Status()
		code := strconv.Itoa(status)

		httpRequests.WithLabelValues(method, route, code).Inc()
		if status < 200 || status > 299 {
			httpErrors.WithLabelValues(method, route, code).Inc()
		}
		httpDuration.WithLabelValues(method, route).Observe(elapsed.Seconds())

		if logRequests {
			size := c.Writer.Size()
			if size < 0 {
				size = 0
			}
			logger.LogAttrs(c.Request.Context(), slog.LevelInfo, "request",
				slog.String("method", method),
				slog.String("route", route),
				slog.String("path", c.Request.URL.Path),
				slog.Int("status", status),
				slog.Float64("duration_ms", float64(elapsed.Microseconds())/1000),
				slog.Int("bytes", size),
				slog.String("client_ip", c.ClientIP()),
			)
		}
	}
}

// metricsHandler exposes everything registered above in the Prometheus
// text format for scraping.
func metricsHandler() gin.HandlerFunc {
	return gin.WrapH(promhttp.Handler())
}
