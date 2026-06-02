package otel

import (
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

const (
	instrumentationNameHTTP = "artifacts-proxy/http"
)

// TracingMiddleware is an HTTP middleware that creates spans for each request
type TracingMiddleware struct {
	next http.Handler
}

// NewTracingMiddleware creates a new tracing middleware
func NewTracingMiddleware(next http.Handler) *TracingMiddleware {
	return &TracingMiddleware{next: next}
}

// ServeHTTP implements http.Handler
func (m *TracingMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	
	// Get upstream from context if available
	upstream := ""
	if v := ctx.Value("upstream"); v != nil {
		if s, ok := v.(string); ok {
			upstream = s
		}
	}
	
	// Start a new span for this request
	tracer := otel.Tracer(instrumentationNameHTTP)
	spanName := r.Method + " " + r.URL.Path
	
	// Build span attributes
	attrs := []attribute.KeyValue{
		attribute.String("http.method", r.Method),
		attribute.String("http.url", r.URL.String()),
		attribute.String("http.host", r.Host),
		attribute.String("http.user_agent", r.UserAgent()),
	}
	
	// Add upstream if available
	if upstream != "" {
		attrs = append(attrs, attribute.String("upstream", upstream))
		spanName = upstream + " - " + spanName
	}
	
	ctx, span := tracer.Start(ctx, spanName, trace.WithAttributes(attrs...))
	defer span.End()
	
	// Inject the trace context into the response headers
	propagator := otel.GetTextMapPropagator()
	propagator.Inject(ctx, propagation.HeaderCarrier(w.Header()))
	
	// Update span with response information after the request completes
	rw := &tracingResponseWriter{ResponseWriter: w, status: http.StatusOK}
	
	m.next.ServeHTTP(rw, r.WithContext(ctx))
	
	// Update span with status code
	span.SetAttributes(attribute.Int("http.status_code", rw.status))
	if rw.status >= 400 && rw.status < 600 {
		span.SetStatus(codes.Error, http.StatusText(rw.status))
		span.RecordError(nil)
	}
}

// tracingResponseWriter wraps http.ResponseWriter to capture status code
type tracingResponseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *tracingResponseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}
