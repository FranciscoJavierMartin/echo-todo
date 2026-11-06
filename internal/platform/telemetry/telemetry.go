// Package telemetry configures OpenTelemetry tracing and the Prometheus registry.
package telemetry

import (
	"context"
	"errors"
	"fmt"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"echo-todo-2/internal/platform/postgres"
)

// Telemetry bundles the tracer provider and metrics registry the app uses.
type Telemetry struct {
	TracerProvider trace.TracerProvider
	Registry       *prometheus.Registry
	shutdown       func(context.Context) error
}

// New sets up tracing. With an OTLP endpoint, spans are batched to it over HTTP
// (standard OTEL_EXPORTER_OTLP_* env vars apply); without one a no-op provider is
// used but W3C traceparent is still propagated.
func New(ctx context.Context, serviceName, env, otlpEndpoint string) (*Telemetry, error) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	reg := NewRegistry()

	if otlpEndpoint == "" {
		return &Telemetry{TracerProvider: noop.NewTracerProvider(), Registry: reg, shutdown: func(context.Context) error { return nil }}, nil
	}
	exp, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(otlpEndpoint))
	if err != nil {
		return nil, fmt.Errorf("otlp exporter: %w", err)
	}
	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes(semconv.SchemaURL,
		semconv.ServiceName(serviceName),
		semconv.DeploymentEnvironmentName(env),
	))
	if err != nil && !errors.Is(err, resource.ErrSchemaURLConflict) {
		return nil, fmt.Errorf("otel resource: %w", err)
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp), sdktrace.WithResource(res))
	otel.SetTracerProvider(tp)
	return &Telemetry{TracerProvider: tp, Registry: reg, shutdown: tp.Shutdown}, nil
}

// NewForTest wraps an existing tracer provider (e.g. one backed by an in-memory exporter).
func NewForTest(tp trace.TracerProvider) *Telemetry {
	if tp == nil {
		tp = noop.NewTracerProvider()
	}
	return &Telemetry{TracerProvider: tp, Registry: NewRegistry(), shutdown: func(context.Context) error { return nil }}
}

// NewRegistry returns a registry with Go runtime and process collectors.
func NewRegistry() *prometheus.Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return reg
}

func (t *Telemetry) Shutdown(ctx context.Context) error { return t.shutdown(ctx) }

// PGTracer returns a pool option that creates a child span for every query.
func (t *Telemetry) PGTracer() postgres.Option {
	return func(cfg *pgxpool.Config) {
		cfg.ConnConfig.Tracer = otelpgx.NewTracer(
			otelpgx.WithTracerProvider(t.TracerProvider),
			otelpgx.WithTrimSQLInSpanName(),
			otelpgx.WithDisableAcquireTracer(),
		)
	}
}
