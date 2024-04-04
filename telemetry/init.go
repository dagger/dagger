package telemetry

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"

	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/telemetry/inflight"
	"github.com/dagger/dagger/telemetry/sdklog"
	"github.com/dagger/dagger/telemetry/sdklog/otlploggrpc"
	"github.com/dagger/dagger/telemetry/sdklog/otlploghttp"
)

var configuredCloudSpanExporter sdktrace.SpanExporter
var configuredCloudLogsExporter sdklog.LogExporter
var ocnfiguredCloudExportersOnce sync.Once

func ConfiguredCloudExporters(ctx context.Context) (sdktrace.SpanExporter, sdklog.LogExporter, bool) {
	ocnfiguredCloudExportersOnce.Do(func() {
		cloudToken := os.Getenv("DAGGER_CLOUD_TOKEN")
		if cloudToken == "" {
			return
		}

		cloudURL := os.Getenv("DAGGER_CLOUD_URL")
		if cloudURL == "" {
			cloudURL = "https://api.dagger.cloud"
		}

		cloudEndpoint, err := url.Parse(cloudURL)
		if err != nil {
			slog.Warn("bad cloud URL", "error", err)
			return
		}

		tracesURL := cloudEndpoint.JoinPath("v1", "traces")
		logsURL := cloudEndpoint.JoinPath("v1", "logs")

		headers := map[string]string{
			"Authorization": "Bearer " + cloudToken,
		}

		configuredCloudSpanExporter, err = otlptracehttp.New(ctx,
			otlptracehttp.WithEndpointURL(tracesURL.String()),
			otlptracehttp.WithHeaders(headers))
		if err != nil {
			slog.Warn("failed to configure cloud tracing", "error", err)
			return
		}

		cfg := otlploghttp.Config{
			Endpoint: logsURL.Host,
			URLPath:  logsURL.Path,
			Insecure: logsURL.Scheme != "https",
			Headers:  headers,
		}
		configuredCloudLogsExporter = otlploghttp.NewClient(cfg)
	})

	return configuredCloudSpanExporter, configuredCloudLogsExporter,
		configuredCloudSpanExporter != nil
}

func OtelConfigured() bool {
	for _, env := range os.Environ() {
		if strings.HasPrefix(env, "OTEL_") {
			return true
		}
	}
	return false
}

var configuredSpanExporter sdktrace.SpanExporter
var configuredSpanExporterOnce sync.Once

func ConfiguredSpanExporter(ctx context.Context) (sdktrace.SpanExporter, bool) {
	ctx = context.WithoutCancel(ctx)

	configuredSpanExporterOnce.Do(func() {
		if !OtelConfigured() {
			return
		}

		var err error

		var proto string
		if v := os.Getenv("OTEL_EXPORTER_OTLP_TRACES_PROTOCOL"); v != "" {
			proto = v
		} else if v := os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL"); v != "" {
			proto = v
		} else {
			// https://github.com/open-telemetry/opentelemetry-specification/blob/v1.8.0/specification/protocol/exporter.md#specify-protocol
			proto = "http/protobuf"
		}

		var endpoint string
		if v := os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"); v != "" {
			endpoint = v
		} else if v := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); v != "" {
			if proto == "http/protobuf" {
				endpoint, err = url.JoinPath(v, "v1", "traces")
				if err != nil {
					slog.Warn("failed to join path", "error", err)
					return
				}
			} else {
				endpoint = v
			}
		}

		slog.ExtraDebug("configuring tracing via env", "protocol", proto)

		switch proto {
		case "http/protobuf", "http":
			configuredSpanExporter, err = otlptracehttp.New(ctx,
				otlptracehttp.WithEndpointURL(endpoint))
		case "grpc":
			var u *url.URL
			u, err = url.Parse(endpoint)
			if err != nil {
				slog.Warn("bad OTLP logs endpoint %q: %w", endpoint, err)
				return
			}
			opts := []otlptracegrpc.Option{
				otlptracegrpc.WithEndpointURL(endpoint),
			}
			if u.Scheme == "unix" {
				dialer := func(ctx context.Context, addr string) (net.Conn, error) {
					return net.Dial(u.Scheme, u.Path)
				}
				opts = append(opts,
					otlptracegrpc.WithDialOption(grpc.WithContextDialer(dialer)),
					otlptracegrpc.WithInsecure())
			}
			configuredSpanExporter, err = otlptracegrpc.New(ctx, opts...)
		default:
			err = fmt.Errorf("unknown OTLP protocol: %s", proto)
		}
		if err != nil {
			slog.Warn("failed to configure tracing", "error", err)
		}
	})
	return configuredSpanExporter, configuredSpanExporter != nil
}

var configuredLogExporter sdklog.LogExporter
var configuredLogExporterOnce sync.Once

func ConfiguredLogExporter(ctx context.Context) (sdklog.LogExporter, bool) {
	ctx = context.WithoutCancel(ctx)

	configuredLogExporterOnce.Do(func() {
		var err error

		var endpoint string
		if v := os.Getenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT"); v != "" {
			endpoint = v
		} else if v := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); v != "" {
			// we can't assume all OTLP endpoints supprot logs. better to be
			// explicit than have noisy otel errors.
			slog.ExtraDebug("note: intentionally not sending logs to OTEL_EXPORTER_OTLP_ENDPOINT; set OTEL_EXPORTER_OTLP_LOGS_ENDPOINT if needed")
			return
		}
		if endpoint == "" {
			return
		}

		var proto string
		if v := os.Getenv("OTEL_EXPORTER_OTLP_LOGS_PROTOCOL"); v != "" {
			proto = v
		} else if v := os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL"); v != "" {
			proto = v
		} else {
			// https://github.com/open-telemetry/opentelemetry-specification/blob/v1.8.0/specification/protocol/exporter.md#specify-protocol
			proto = "http/protobuf"
		}

		slog.ExtraDebug("configuring logging via env", "protocol", proto, "endpoint", endpoint)

		u, err := url.Parse(endpoint)
		if err != nil {
			slog.Warn("bad OTLP logs endpoint %q: %w", endpoint, err)
			return
		}

		switch proto {
		case "http/protobuf", "http":
			cfg := otlploghttp.Config{
				Endpoint: u.Host,
				URLPath:  u.Path,
				Insecure: u.Scheme != "https",
				Headers:  map[string]string{},
			}
			if headers := os.Getenv("OTEL_EXPORTER_OTLP_HEADERS"); headers != "" {
				for _, header := range strings.Split(headers, ",") {
					name, value, _ := strings.Cut(header, "=")
					cfg.Headers[name] = value
				}
			}
			configuredLogExporter = otlploghttp.NewClient(cfg)

		case "grpc":
			opts := []otlploggrpc.Option{
				otlploggrpc.WithEndpointURL(endpoint),
			}
			if u.Scheme == "unix" {
				dialer := func(ctx context.Context, addr string) (net.Conn, error) {
					return net.Dial(u.Scheme, u.Path)
				}
				opts = append(opts,
					otlploggrpc.WithDialOption(grpc.WithContextDialer(dialer)),
					otlploggrpc.WithInsecure())
			}
			client := otlploggrpc.NewClient(opts...)
			err = client.Start(ctx)
			configuredLogExporter = client
		default:
			err = fmt.Errorf("unknown OTLP protocol: %s", proto)
		}
		if err != nil {
			slog.Warn("failed to configure logging", "error", err)
		}
	})
	return configuredLogExporter, configuredLogExporter != nil
}

// FallbackResource is the fallback resource definition. A more specific
// resource should be set in Init.
func FallbackResource() *resource.Resource {
	return resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceNameKey.String("dagger"),
	)
}

var (
	// set by Init, closed by Close
	tracerProvider *inflight.ProxyTraceProvider
	loggerProvider *sdklog.LoggerProvider
)

// LiveSpanProcessor is a SpanProcessor that can additionally receive updates
// for a span at runtime, rather than waiting until the span ends.
type LiveSpanProcessor interface {
	sdktrace.SpanProcessor

	// OnUpdate method enqueues a trace.ReadOnlySpan for later processing.
	OnUpdate(s sdktrace.ReadOnlySpan)
}

type Config struct {
	// Auto-detect exporters from OTEL_* env variables.
	Detect bool

	// LiveTraceExporters are exporters that can receive updates for spans at runtime,
	// rather than waiting until the span ends.
	//
	// Example: TUI, Cloud
	LiveTraceExporters []sdktrace.SpanExporter

	// BatchedTraceExporters are exporters that receive spans in batches, after the
	// spans have ended.
	//
	// Example: Honeycomb, Jaeger, etc.
	BatchedTraceExporters []sdktrace.SpanExporter

	// LiveLogExporters are exporters that receive logs in batches of ~100ms.
	LiveLogExporters []sdklog.LogExporter

	// Resource is the resource describing this component and runtime
	// environment.
	Resource *resource.Resource
}

// NearlyImmediate is 100ms, below which has diminishing returns in terms of
// visual perception vs. performance cost.
const NearlyImmediate = 100 * time.Millisecond

var ForceLiveTrace = os.Getenv("FORCE_LIVE_TRACE") != ""

// Logger returns a logger with the given name.
func Logger(name string) log.Logger {
	return loggerProvider.Logger(name) // TODO more instrumentation attrs
}

var SpanProcessors = []sdktrace.SpanProcessor{}
var LogProcessors = []sdklog.LogProcessor{}

// Init sets up the global OpenTelemetry providers tracing, logging, and
// someday metrics providers. It is called by the CLI, the engine, and the
// container shim, so it needs to be versatile.
func Init(ctx context.Context, cfg Config) context.Context {
	slog.ExtraDebug("initializing telemetry")

	if p, ok := os.LookupEnv("TRACEPARENT"); ok {
		slog.ExtraDebug("found TRACEPARENT", "value", p)
		ctx = propagation.TraceContext{}.Extract(ctx, propagation.MapCarrier{"traceparent": p})
	}

	// Set up a text map propagator so that things, well, propagate. The default
	// is a noop.
	otel.SetTextMapPropagator(propagation.TraceContext{})

	// Log to slog.
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		slog.Error("OpenTelemetry error", "error", err)
	}))

	if cfg.Resource == nil {
		cfg.Resource = FallbackResource()
	}

	if cfg.Detect {
		if spans, logs, ok := ConfiguredCloudExporters(ctx); ok {
			cfg.LiveTraceExporters = append(cfg.LiveTraceExporters, spans)
			cfg.LiveLogExporters = append(cfg.LiveLogExporters, logs)
		}
		if exp, ok := ConfiguredSpanExporter(ctx); ok {
			if ForceLiveTrace {
				cfg.LiveTraceExporters = append(cfg.LiveTraceExporters, exp)
			} else {
				cfg.BatchedTraceExporters = append(cfg.BatchedTraceExporters,
					// Filter out unfinished spans to avoid confusing external systems.
					//
					// Normally we avoid sending them here by virtue of putting this into
					// BatchedTraceExporters, but that only applies to the local process.
					// Unfinished spans may end up here if they're proxied out of the
					// engine via Params.EngineTrace.
					FilterLiveSpansExporter{exp})
			}
		}
		if exp, ok := ConfiguredLogExporter(ctx); ok {
			cfg.LiveLogExporters = append(cfg.LiveLogExporters, exp)
		}
	}

	traceOpts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(cfg.Resource),
	}

	liveProcessors := make([]LiveSpanProcessor, 0, len(cfg.LiveTraceExporters))
	for _, exporter := range cfg.LiveTraceExporters {
		processor := inflight.NewBatchSpanProcessor(exporter,
			inflight.WithBatchTimeout(NearlyImmediate))
		liveProcessors = append(liveProcessors, processor)
		SpanProcessors = append(SpanProcessors, processor)
	}
	for _, exporter := range cfg.BatchedTraceExporters {
		processor := sdktrace.NewBatchSpanProcessor(exporter)
		SpanProcessors = append(SpanProcessors, processor)
	}
	for _, proc := range SpanProcessors {
		traceOpts = append(traceOpts, sdktrace.WithSpanProcessor(proc))
	}

	tracerProvider = inflight.NewProxyTraceProvider(
		sdktrace.NewTracerProvider(traceOpts...),
		func(s trace.Span) { // OnUpdate
			if ro, ok := s.(sdktrace.ReadOnlySpan); ok && s.IsRecording() {
				for _, processor := range liveProcessors {
					processor.OnUpdate(ro)
				}
			}
		},
	)

	// Register our TracerProvider as the global so any imported instrumentation
	// in the future will default to using it.
	//
	// NB: this is also necessary so that we can establish a root span, otherwise
	// telemetry doesn't work.
	otel.SetTracerProvider(tracerProvider)

	// Set up a log provider if configured.
	if len(cfg.LiveLogExporters) > 0 {
		lp := sdklog.NewLoggerProvider(cfg.Resource)
		for _, exp := range cfg.LiveLogExporters {
			processor := sdklog.NewBatchLogProcessor(exp,
				sdklog.WithBatchTimeout(NearlyImmediate))
			LogProcessors = append(LogProcessors, processor)
			lp.RegisterLogProcessor(processor)
		}
		loggerProvider = lp

		// TODO: someday do the following (once it exists)
		// Register our TracerProvider as the global so any imported
		// instrumentation in the future will default to using it.
		// otel.SetLoggerProvider(loggerProvider)
	}

	return ctx
}

// Flush drains telemetry data, and is typically called just before a client
// goes away.
//
// NB: now that we wait for all spans to complete, this is less necessary, but
// it seems wise to keep it anyway, as the spots where it are needed are hard
// to find.
func Flush(ctx context.Context) {
	slog.ExtraDebug("flushing processors")
	if tracerProvider != nil {
		if err := tracerProvider.ForceFlush(ctx); err != nil {
			slog.Error("failed to flush spans", "error", err)
		}
	}
	slog.ExtraDebug("done flushing processors")
}

// Close shuts down the global OpenTelemetry providers, flushing any remaining
// data to the configured exporters.
func Close() {
	flushCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	Flush(flushCtx)
	if tracerProvider != nil {
		if err := tracerProvider.Shutdown(flushCtx); err != nil {
			slog.Error("failed to shut down tracer provider", "error", err)
		}
	}
	if loggerProvider != nil {
		if err := loggerProvider.Shutdown(flushCtx); err != nil {
			slog.Error("failed to shut down logger provider", "error", err)
		}
	}
}
