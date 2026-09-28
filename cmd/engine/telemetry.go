package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/config"
	"github.com/dagger/dagger/engine/server"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/engine/telemetry/cgroupmetrics"
	"github.com/dagger/dagger/engine/telemetryattrs"
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

	// engineEventExportQueueSize bounds the event records waiting for export
	// to Cloud. The processor blocks the engine's event drain while it is
	// full, so the engine's own counted event queue is the one place events
	// drop.
	engineEventExportQueueSize = 4096
	// engineEventExportBatchSize bounds the event records of one export.
	engineEventExportBatchSize = 512
	// engineEventShutdownTimeout bounds the export's flush when the server did
	// not already shut it down, for example after a failed start.
	engineEventShutdownTimeout = 30 * time.Second
)

var (
	engineName string
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

// engineEventExport is the engine's export of its cache events to Dagger
// Cloud: a logger provider of its own, written only by the engine's event
// emitter.
type engineEventExport struct {
	provider     *sdklog.LoggerProvider
	shutdownOnce sync.Once
	shutdownErr  error
}

// Enabled reports whether the engine exports its cache events.
func (e *engineEventExport) Enabled() bool {
	return e != nil && e.provider != nil
}

// Logger is the logger the engine emits its cache events through.
func (e *engineEventExport) Logger() log.Logger {
	return e.provider.Logger(telemetryattrs.EngineEventScope)
}

// Shutdown flushes the remaining event records to Cloud within ctx. Later
// calls return the first call's result.
func (e *engineEventExport) Shutdown(ctx context.Context) error {
	if !e.Enabled() {
		return nil
	}
	e.shutdownOnce.Do(func() {
		e.shutdownErr = e.provider.Shutdown(ctx)
	})
	return e.shutdownErr
}

// shutdownAtExit shuts the export down, bounded, when the server did not.
func (e *engineEventExport) shutdownAtExit(ctx context.Context) {
	if !e.Enabled() {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), engineEventShutdownTimeout)
	defer cancel()
	if err := e.Shutdown(ctx); err != nil {
		slog.Error("failed to export engine cache events at shutdown", "error", err)
	}
}

// InitTelemetry sets up the engine process's telemetry and returns its
// resource, which names the engine instance, for newEngineEventExport.
func InitTelemetry(ctx context.Context, engineInstanceID string) (context.Context, *resource.Resource) {
	otelResource, err := resource.New(ctx,
		resource.WithHost(),
		resource.WithAttributes(
			semconv.ServiceNameKey.String("dagger-engine"),
			semconv.ServiceVersionKey.String(engine.Version),
			attribute.String("dagger.io/engine.name", engineName),
			semconv.ServiceInstanceID(engineInstanceID),
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

// envEngineEvents, set to a true boolean such as "1" or "true", enables the
// export of the engine's cache events. The token alone does not: clients
// forward DAGGER_CLOUD_TOKEN into every engine they provision.
const envEngineEvents = "_EXPERIMENTAL_DAGGER_ENGINE_EVENTS"

// newEngineEventExport returns the export of the engine's cache events to
// Dagger Cloud at DAGGER_CLOUD_URL, under DAGGER_CLOUD_TOKEN, when that token
// is set and either _EXPERIMENTAL_DAGGER_ENGINE_EVENTS or the engine config's
// telemetry.engineEvents enables it. The export has its own logger provider,
// so nothing else emitted in the process reaches Cloud. Otherwise the engine
// exports no events.
func newEngineEventExport(ctx context.Context, otelResource *resource.Resource, cfg config.TelemetryConfig) *engineEventExport {
	enabled, _ := strconv.ParseBool(os.Getenv(envEngineEvents))
	enabled = enabled || cfg.EngineEvents
	if !enabled || os.Getenv("DAGGER_CLOUD_TOKEN") == "" || otelResource == nil {
		return nil
	}
	cloudAuth, err := auth.GetCloudAuth(ctx)
	if err != nil || cloudAuth == nil {
		slog.Warn("engine cache events not exported: cannot read DAGGER_CLOUD_TOKEN", "error", err)
		return nil
	}
	spans, logs, metrics, err := enginetel.NewCloudExporters(ctx, cloudAuth, nil, os.Getenv("DAGGER_CLOUD_URL"))
	if err != nil {
		slog.Warn("engine cache events not exported: cannot configure the Cloud exporter", "error", err)
		return nil
	}
	// Session telemetry reaches Cloud through the sessions' own exporters;
	// only the log exporter carries engine events.
	if err := spans.Shutdown(ctx); err != nil {
		slog.Debug("shut down unused Cloud span exporter", "error", err)
	}
	if err := metrics.Shutdown(ctx); err != nil {
		slog.Debug("shut down unused Cloud metric exporter", "error", err)
	}
	processor := enginetel.NewBlockingLogProcessor(logs, engineEventExportQueueSize, engineEventExportBatchSize, telemetry.NearlyImmediate)
	return &engineEventExport{provider: sdklog.NewLoggerProvider(
		sdklog.WithResource(otelResource),
		sdklog.WithProcessor(processor),
	)}
}

// serverEngineEventExport returns the export as the server takes it: nil, not
// a nil pointer, when the engine exports no events.
func serverEngineEventExport(e *engineEventExport) server.EngineEventExport {
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
	// The process resource names the engine instance (service.instance.id),
	// the epoch of the cumulative cgroup counters.
	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(telemetry.Resource),
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
