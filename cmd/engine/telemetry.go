package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/dagger/dagger/internal/buildkit/identity"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/config"
	"github.com/dagger/dagger/engine/telemetry/cgroupmetrics"
	telemetry "github.com/dagger/otel-go"
)

const (
	InstrumentationScopeName = "dagger.io/engine"
)

var (
	engineName string
	// Each engine process is a separate epoch for cumulative cgroup counters.
	engineInstanceID = identity.NewID()
)

func init() {
	var ok bool
	engineName, ok = os.LookupEnv(engine.DaggerNameEnv)
	if !ok {
		// use the hostname
		hostname, err := os.Hostname()
		if err != nil {
			engineName = "rand-" + identity.NewID() // random ID as a fallback
		} else {
			engineName = hostname
		}
	}
}

func InitTelemetry(ctx context.Context) context.Context {
	otelResource, err := resource.New(ctx,
		resource.WithHost(),
		resource.WithAttributes(
			semconv.ServiceNameKey.String("dagger-engine"),
			semconv.ServiceVersionKey.String(engine.Version),
			attribute.String("dagger.io/engine.name", engineName),
		),
	)
	if err != nil {
		slog.Error("failed to create OTel resource", "error", err)
		return ctx
	}

	// Do not enable Detect: engine trace and log routing must stay unchanged.
	return telemetry.Init(ctx, telemetry.Config{Resource: otelResource})
}

// initResourceMetrics creates an engine-owned provider after config loading.
// It is never installed in the context or otel-go's shared exporter registry.
func initResourceMetrics(ctx context.Context, cfg config.TelemetryConfig) *sdkmetric.MeterProvider {
	if !cfg.ResourceMetrics || telemetry.Resource == nil {
		return nil
	}
	if os.Getenv(engine.OTelMetricsEndpointEnv) == "" {
		slog.Warn("engine resource metrics require OTEL_EXPORTER_OTLP_METRICS_ENDPOINT; export disabled")
		return nil
	}

	exporter, err := newResourceMetricExporter(ctx)
	if err != nil {
		slog.Warn("failed to configure engine resource metric export", "error", err)
		return nil
	}
	// Do not change the resource used by existing client, trace, or log exports.
	resourceAttrs := append(telemetry.Resource.Attributes(), semconv.ServiceInstanceIDKey.String(engineInstanceID))
	resourceIdentity := resource.NewWithAttributes(telemetry.Resource.SchemaURL(), resourceAttrs...)
	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(resourceIdentity),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter)),
	)
	// Keep the callback registered through provider shutdown so the final
	// collection can still read the engine cgroup.
	if _, err := cgroupmetrics.Register(ctx, provider.Meter(cgroupmetrics.InstrumentationScopeName)); err != nil {
		slog.Warn("failed to register engine cgroup resource metrics", "error", err)
	}
	return provider
}

func newResourceMetricExporter(ctx context.Context) (sdkmetric.Exporter, error) {
	protocol := os.Getenv(engine.OTelMetricsProtocolEnv)
	if protocol == "" {
		protocol = os.Getenv(engine.OTelExporterProtocolEnv)
	}
	// The SDK exporters read endpoint, headers, TLS, timeout, and compression
	// from the standard OTLP environment variables. Do not duplicate that logic.
	switch protocol {
	case "", "http/protobuf":
		return otlpmetrichttp.New(ctx)
	case "grpc":
		return otlpmetricgrpc.New(ctx)
	default:
		return nil, fmt.Errorf("unsupported OTLP metrics protocol %q", protocol)
	}
}

func closeResourceMetrics(ctx context.Context, provider *sdkmetric.MeterProvider) {
	if provider == nil {
		return
	}
	flushCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err := provider.Shutdown(flushCtx); err != nil {
		slog.Warn("failed to shut down engine resource metrics", "error", err)
	}
}
