package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/dagger/dagger/dagql/cachefact"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/config"
	"github.com/dagger/dagger/engine/server"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/engine/telemetry/cgroupmetrics"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/dagger/internal/cloud/auth"
	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
)

const (
	InstrumentationScopeName = "dagger.io/engine"

	// cacheFactExportQueueSize bounds the fact records waiting for export to
	// Cloud. The processor blocks the engine's fact drain while it is full,
	// so the engine's own counted fact queue is the one place facts drop.
	cacheFactExportQueueSize = 4096
	// cacheFactExportBatchSize bounds the fact records of one export.
	cacheFactExportBatchSize = 512
	// cacheFactShutdownTimeout bounds the export's flush when the server did
	// not already shut it down, for example after a failed start.
	cacheFactShutdownTimeout = 30 * time.Second
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

// cacheFactExport is the engine's export of its cache facts to Dagger Cloud:
// a logger provider of its own, written only by the engine's fact emitter.
type cacheFactExport struct {
	provider     *sdklog.LoggerProvider
	shutdownOnce sync.Once
	shutdownErr  error
}

// Enabled reports whether the engine exports cache facts.
func (e *cacheFactExport) Enabled() bool {
	return e != nil && e.provider != nil
}

// Logger is the logger the engine emits its cache facts through.
func (e *cacheFactExport) Logger() log.Logger {
	return e.provider.Logger(cachefact.ScopeName)
}

// Shutdown flushes the remaining fact records to Cloud within ctx. Later
// calls return the first call's result.
func (e *cacheFactExport) Shutdown(ctx context.Context) error {
	if !e.Enabled() {
		return nil
	}
	e.shutdownOnce.Do(func() {
		e.shutdownErr = e.provider.Shutdown(ctx)
	})
	return e.shutdownErr
}

// shutdownAtExit shuts the export down, bounded, when the server did not.
func (e *cacheFactExport) shutdownAtExit(ctx context.Context) {
	if !e.Enabled() {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cacheFactShutdownTimeout)
	defer cancel()
	if err := e.Shutdown(ctx); err != nil {
		slog.Error("failed to export cache facts at shutdown", "error", err)
	}
}

// InitTelemetry sets up the engine process's telemetry and returns its
// resource, which names the engine instance, for newCacheFactExport.
func InitTelemetry(ctx context.Context, engineInstanceID string) (context.Context, *resource.Resource) {
	otelResource, err := resource.New(ctx,
		resource.WithHost(),
		resource.WithAttributes(
			semconv.ServiceNameKey.String("dagger-engine"),
			semconv.ServiceVersionKey.String(engine.Version),
			attribute.String("dagger.io/engine.name", engineName),
			attribute.String(cachefact.ResourceEngineInstance, engineInstanceID),
		),
	)
	if err != nil {
		slog.Error("failed to create OTel resource", "error", err)
		return ctx, nil
	}

	ctx = telemetry.Init(ctx, telemetry.Config{
		Resource: otelResource,
	})

	return ctx, otelResource
}

// envCacheFactsExport enables the export of cache facts. The token alone does
// not: clients forward DAGGER_CLOUD_TOKEN into every engine they provision.
const envCacheFactsExport = "_EXPERIMENTAL_DAGGER_CACHE_FACTS_EXPORT"

// newCacheFactExport returns the export of the dagql cache's facts to Dagger
// Cloud at DAGGER_CLOUD_URL, under DAGGER_CLOUD_TOKEN, when that token is set
// and either _EXPERIMENTAL_DAGGER_CACHE_FACTS_EXPORT or the engine config's
// telemetry.cacheFacts enables it. The export has its own logger provider, so
// nothing else emitted in the process reaches Cloud. Otherwise the engine
// exports no facts.
func newCacheFactExport(ctx context.Context, otelResource *resource.Resource, cfg config.TelemetryConfig) *cacheFactExport {
	enabled := os.Getenv(envCacheFactsExport) != "" || cfg.CacheFacts
	if !enabled || os.Getenv("DAGGER_CLOUD_TOKEN") == "" || otelResource == nil {
		return nil
	}
	cloudAuth, err := auth.GetCloudAuth(ctx)
	if err != nil || cloudAuth == nil {
		slog.Warn("cache facts not exported: cannot read DAGGER_CLOUD_TOKEN", "error", err)
		return nil
	}
	spans, logs, metrics, err := enginetel.NewCloudExporters(ctx, cloudAuth, nil, os.Getenv("DAGGER_CLOUD_URL"))
	if err != nil {
		slog.Warn("cache facts not exported: cannot configure the Cloud exporter", "error", err)
		return nil
	}
	// Session telemetry reaches Cloud through clients; only the log exporter
	// carries facts.
	if err := spans.Shutdown(ctx); err != nil {
		slog.Debug("shut down unused Cloud span exporter", "error", err)
	}
	if err := metrics.Shutdown(ctx); err != nil {
		slog.Debug("shut down unused Cloud metric exporter", "error", err)
	}
	processor := enginetel.NewBlockingLogProcessor(logs, cacheFactExportQueueSize, cacheFactExportBatchSize, telemetry.NearlyImmediate)
	return &cacheFactExport{provider: sdklog.NewLoggerProvider(
		sdklog.WithResource(otelResource),
		sdklog.WithProcessor(processor),
	)}
}

// serverCacheFactExport returns the export as the server takes it: nil, not a
// nil pointer, when the engine exports no facts.
func serverCacheFactExport(e *cacheFactExport) server.CacheFactExport {
	if !e.Enabled() {
		return nil
	}
	return e
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
