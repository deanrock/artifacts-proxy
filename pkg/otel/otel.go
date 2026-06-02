package otel

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/semconv/v1.21.0"
	"google.golang.org/grpc"
	grinsecure "google.golang.org/grpc/credentials/insecure"
)

const (
	instrumentationName = "artifacts-proxy"
)

// Config holds the OpenTelemetry configuration
type Config struct {
	Endpoint    string
	Insecure    bool
	ServiceName string
}

// shutdownFunc is a function that can be called to shutdown a component
type shutdownFunc func(context.Context) error

// providers holds the OTEL providers and their shutdown functions
type providers struct {
	tracerProvider *sdktrace.TracerProvider
	meterProvider  *metric.MeterProvider
	loggerProvider *sdklog.LoggerProvider
	tracerShutdown shutdownFunc
	meterShutdown  shutdownFunc
	loggerShutdown shutdownFunc
}

var p *providers

// newResource creates a new resource with service attributes
func newResource(serviceName string) *sdkresource.Resource {
	return sdkresource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(serviceName),
		semconv.ServiceVersion(getVersion()),
	)
}

// Init initializes OpenTelemetry providers (tracer, meter, logger) with the given config
func Init(cfg Config) error {
	if cfg.ServiceName == "" {
		cfg.ServiceName = instrumentationName
	}

	var err error

	p = &providers{}

	// Set up propagator
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	// Initialize tracer provider
	p.tracerProvider, p.tracerShutdown, err = newTracerProvider(cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize tracer provider: %w", err)
	}

	// Initialize meter provider
	p.meterProvider, p.meterShutdown, err = newMeterProvider(cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize meter provider: %w", err)
	}

	// Initialize logger provider
	p.loggerProvider, p.loggerShutdown, err = newLoggerProvider(cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize logger provider: %w", err)
	}

	// Set global providers
	otel.SetTracerProvider(p.tracerProvider)
	otel.SetMeterProvider(p.meterProvider)

	// Note: There's no otel.SetLoggerProvider in the current SDK
	// The logger provider is set globally within the sdklog package

	return nil
}

// Shutdown shuts down all OTEL providers
func Shutdown(ctx context.Context) error {
	if p == nil {
		return nil
	}

	var errs []error

	if p.loggerShutdown != nil {
		if err := p.loggerShutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("logger shutdown: %w", err))
		}
	}

	if p.meterShutdown != nil {
		if err := p.meterShutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("meter shutdown: %w", err))
		}
	}

	if p.tracerShutdown != nil {
		if err := p.tracerShutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("tracer shutdown: %w", err))
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

// getGRPCDialOption returns the appropriate gRPC dial option based on whether TLS should be used
func getGRPCDialOption(insecure bool) grpc.DialOption {
	if insecure {
		return grpc.WithTransportCredentials(grinsecure.NewCredentials())
	}
	return grpc.WithDefaultCallOptions()
}

// newTracerProvider creates a new trace provider with OTLP exporter
func newTracerProvider(cfg Config) (*sdktrace.TracerProvider, shutdownFunc, error) {
	opts := []otlptracegrpc.Option{
		otlptracegrpc.WithEndpoint(cfg.Endpoint),
		otlptracegrpc.WithDialOption(getGRPCDialOption(cfg.Insecure)),
	}
	
	if cfg.Insecure {
		opts = append(opts, otlptracegrpc.WithInsecure())
	}
	
	exporter, err := otlptracegrpc.New(
		context.Background(),
		opts...,
	)
	if err != nil {
		return nil, nil, err
	}

	res := newResource(cfg.ServiceName)

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)

	return tp, func(ctx context.Context) error {
		if err := tp.Shutdown(ctx); err != nil {
			log.Printf("Error shutting down tracer provider: %v", err)
			return err
		}
		return nil
	}, nil
}

// newMeterProvider creates a new meter provider with OTLP exporter
func newMeterProvider(cfg Config) (*metric.MeterProvider, shutdownFunc, error) {
	opts := []otlpmetricgrpc.Option{
		otlpmetricgrpc.WithEndpoint(cfg.Endpoint),
		otlpmetricgrpc.WithDialOption(getGRPCDialOption(cfg.Insecure)),
	}
	
	if cfg.Insecure {
		opts = append(opts, otlpmetricgrpc.WithInsecure())
	}
	
	exporter, err := otlpmetricgrpc.New(
		context.Background(),
		opts...,
	)
	if err != nil {
		return nil, nil, err
	}

	res := newResource(cfg.ServiceName)

	mp := metric.NewMeterProvider(
		metric.WithResource(res),
		metric.WithReader(metric.NewPeriodicReader(exporter,
			metric.WithInterval(15*time.Second))),
	)

	return mp, func(ctx context.Context) error {
		if err := mp.Shutdown(ctx); err != nil {
			log.Printf("Error shutting down meter provider: %v", err)
			return err
		}
		return nil
	}, nil
}

// newLoggerProvider creates a new logger provider with OTLP exporter
func newLoggerProvider(cfg Config) (*sdklog.LoggerProvider, shutdownFunc, error) {
	opts := []otlploggrpc.Option{
		otlploggrpc.WithEndpoint(cfg.Endpoint),
		otlploggrpc.WithDialOption(getGRPCDialOption(cfg.Insecure)),
	}
	
	if cfg.Insecure {
		opts = append(opts, otlploggrpc.WithInsecure())
	}
	
	exporter, err := otlploggrpc.New(
		context.Background(),
		opts...,
	)
	if err != nil {
		return nil, nil, err
	}

	res := newResource(cfg.ServiceName)

	lp := sdklog.NewLoggerProvider(
		sdklog.WithResource(res),
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exporter)),
	)

	return lp, func(ctx context.Context) error {
		if err := lp.Shutdown(ctx); err != nil {
			log.Printf("Error shutting down logger provider: %v", err)
			return err
		}
		return nil
	}, nil
}

// getVersion returns the application version from environment or default
func getVersion() string {
	if v := os.Getenv("APP_VERSION"); v != "" {
		return v
	}
	return "unknown"
}

// TracerProvider returns the global tracer provider
func TracerProvider() *sdktrace.TracerProvider {
	return p.tracerProvider
}

// MeterProvider returns the global meter provider
func MeterProvider() *metric.MeterProvider {
	return p.meterProvider
}

// LoggerProvider returns the global logger provider
func LoggerProvider() *sdklog.LoggerProvider {
	return p.loggerProvider
}
