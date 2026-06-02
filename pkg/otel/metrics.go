package otel

import (
	"context"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const (
	meterName = "artifacts-proxy"
)

var (
	// HTTP metrics
	httpRequestDuration metric.Float64Histogram
	httpRequestCount    metric.Int64Counter
	httpRequestSize     metric.Int64Histogram
	httpResponseSize    metric.Int64Histogram

	// Cache metrics
	cacheHits   metric.Int64Counter
	cacheMisses metric.Int64Counter

	// Upstream metrics
	upstreamRequests metric.Int64Counter
	upstreamErrors  metric.Int64Counter
)

// InitMetrics initializes all custom metrics
func InitMetrics() error {
	meter := MeterProvider().Meter(meterName)

	var err error

	// HTTP request duration histogram
	httpRequestDuration, err = meter.Float64Histogram(
		"http.server.request.duration",
		metric.WithDescription("The duration of HTTP requests"),
		metric.WithUnit("ms"),
	)
	if err != nil {
		return err
	}

	// HTTP request count
	httpRequestCount, err = meter.Int64Counter(
		"http.server.request.count",
		metric.WithDescription("The number of HTTP requests"),
		metric.WithUnit("{request}"),
	)
	if err != nil {
		return err
	}

	// HTTP request size histogram
	httpRequestSize, err = meter.Int64Histogram(
		"http.server.request.size",
		metric.WithDescription("The size of HTTP requests"),
		metric.WithUnit("bytes"),
	)
	if err != nil {
		return err
	}

	// HTTP response size histogram
	httpResponseSize, err = meter.Int64Histogram(
		"http.server.response.size",
		metric.WithDescription("The size of HTTP responses"),
		metric.WithUnit("bytes"),
	)
	if err != nil {
		return err
	}

	// Cache hits counter
	cacheHits, err = meter.Int64Counter(
		"cache.hits",
		metric.WithDescription("The number of cache hits"),
		metric.WithUnit("{hit}"),
	)
	if err != nil {
		return err
	}

	// Cache misses counter
	cacheMisses, err = meter.Int64Counter(
		"cache.misses",
		metric.WithDescription("The number of cache misses"),
		metric.WithUnit("{miss}"),
	)
	if err != nil {
		return err
	}

	// Upstream requests counter
	upstreamRequests, err = meter.Int64Counter(
		"upstream.requests",
		metric.WithDescription("The number of upstream requests"),
		metric.WithUnit("{request}"),
	)
	if err != nil {
		return err
	}

	// Upstream errors counter
	upstreamErrors, err = meter.Int64Counter(
		"upstream.errors",
		metric.WithDescription("The number of upstream errors"),
		metric.WithUnit("{error}"),
	)
	if err != nil {
		return err
	}

	return nil
}

// MetricsMiddleware is an HTTP middleware that records request metrics
type MetricsMiddleware struct {
	next http.Handler
}

// NewMetricsMiddleware creates a new metrics middleware
func NewMetricsMiddleware(next http.Handler) *MetricsMiddleware {
	return &MetricsMiddleware{next: next}
}

// ServeHTTP implements http.Handler
func (m *MetricsMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	// Get upstream from context if available
	upstream := ""
	if v := r.Context().Value("upstream"); v != nil {
		if s, ok := v.(string); ok {
			upstream = s
		}
	}

	// Wrap the response writer to capture status code and size
	rw := &responseWriter{ResponseWriter: w, status: http.StatusOK, size: 0, upstream: upstream}

	m.next.ServeHTTP(rw, r)

	duration := float64(time.Since(start).Milliseconds())
	method := r.Method
	path := r.URL.Path
	status := rw.status

	// Build base attributes
	attrs := []attribute.KeyValue{
		attribute.String("http.method", method),
		attribute.String("http.url", path),
		attribute.Int("http.status_code", status),
	}

	// Add upstream if available
	if upstream != "" {
		attrs = append(attrs, attribute.String("upstream", upstream))
	}

	httpRequestDuration.Record(r.Context(), duration, metric.WithAttributes(attrs...))
	httpRequestCount.Add(r.Context(), 1, metric.WithAttributes(attrs...))

	// Try to get request size from Content-Length
	if r.ContentLength > 0 {
		httpRequestSize.Record(r.Context(), r.ContentLength, metric.WithAttributes(attrs...))
	}

	// Record response size
	httpResponseSize.Record(r.Context(), int64(rw.size), metric.WithAttributes(attrs...))
}

// responseWriter wraps http.ResponseWriter to capture status code and response size
type responseWriter struct {
	http.ResponseWriter
	status    int
	size      int
	upstream  string // Optional: upstream name for per-upstream metrics
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	n, err := rw.ResponseWriter.Write(b)
	rw.size += n
	return n, err
}

// RecordUpstreamRequestDuration records the duration of an upstream request
func RecordUpstreamRequestDuration(ctx context.Context, upstream string, durationMs float64) {
	if httpRequestDuration != nil {
		httpRequestDuration.Record(ctx, durationMs, metric.WithAttributes(
			attribute.String("upstream", upstream),
		))
	}
}

// RecordUpstreamRequest records an upstream request
func RecordUpstreamRequest(ctx context.Context, upstream string, method string, path string, status int) {
	if httpRequestCount != nil {
		httpRequestCount.Add(ctx, 1, metric.WithAttributes(
			attribute.String("upstream", upstream),
			attribute.String("http.method", method),
			attribute.String("http.url", path),
			attribute.Int("http.status_code", status),
		))
	}
}

// RecordUpstreamResponseSize records the response size from an upstream
func RecordUpstreamResponseSize(ctx context.Context, upstream string, size int64) {
	if httpResponseSize != nil {
		httpResponseSize.Record(ctx, size, metric.WithAttributes(
			attribute.String("upstream", upstream),
		))
	}
}

// RecordCacheHit records a cache hit metric
func RecordCacheHit(ctx context.Context, upstream string) {
	if cacheHits != nil {
		cacheHits.Add(ctx, 1, metric.WithAttributes(
			attribute.String("upstream", upstream),
		))
	}
}

// RecordCacheMiss records a cache miss metric
func RecordCacheMiss(ctx context.Context, upstream string) {
	if cacheMisses != nil {
		cacheMisses.Add(ctx, 1, metric.WithAttributes(
			attribute.String("upstream", upstream),
		))
	}
}

// RecordUpstreamError records an upstream error metric
func RecordUpstreamError(ctx context.Context, upstream string) {
	if upstreamErrors != nil {
		upstreamErrors.Add(ctx, 1, metric.WithAttributes(
			attribute.String("upstream", upstream),
		))
	}
}
