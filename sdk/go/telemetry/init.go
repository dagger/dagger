package telemetry

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"google.golang.org/grpc"
)

func OTelConfigured() bool {
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
		if !OTelConfigured() {
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

var configuredLogExporter sdklog.Exporter
var configuredLogExporterOnce sync.Once

func ConfiguredLogExporter(ctx context.Context) (sdklog.Exporter, bool) {
	ctx = context.WithoutCancel(ctx)

	configuredLogExporterOnce.Do(func() {
		var err error

		var endpoint string
		if v := os.Getenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT"); v != "" {
			endpoint = v
		} else if v := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); v != "" {
			// we can't assume all OTLP endpoints support logs. better to be explicit
			// than have noisy otel errors.
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

		switch proto {
		case "http/protobuf", "http":
			headers := map[string]string{}
			if hs := os.Getenv("OTEL_EXPORTER_OTLP_HEADERS"); hs != "" {
				for _, header := range strings.Split(hs, ",") {
					name, value, _ := strings.Cut(header, "=")
					headers[name] = value
				}
			}
			configuredLogExporter, err = otlploghttp.New(ctx,
				otlploghttp.WithEndpointURL(endpoint),
				otlploghttp.WithHeaders(headers))

		case "grpc":
			// FIXME: bring back when it's actually implemented

			// u, err := url.Parse(endpoint)
			// if err != nil {
			// 	slog.Warn("bad OTLP logs endpoint %q: %w", endpoint, err)
			// 	return
			// }
			//
			opts := []otlploggrpc.Option{
				// 	otlploggrpc.WithEndpointURL(endpoint),
			}
			// if u.Scheme == "unix" {
			// 	dialer := func(ctx context.Context, addr string) (net.Conn, error) {
			// 		return net.Dial(u.Scheme, u.Path)
			// 	}
			// 	opts = append(opts,
			// 		otlploggrpc.WithDialOption(grpc.WithContextDialer(dialer)),
			// 		otlploggrpc.WithInsecure())
			// }
			configuredLogExporter, err = otlploggrpc.New(ctx, opts...)

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
	tracerProvider *sdktrace.TracerProvider = sdktrace.NewTracerProvider()
	loggerProvider *sdklog.LoggerProvider   = sdklog.NewLoggerProvider()
)

type Config struct {
	// Auto-detect exporters from OTEL_* env variables.
	Detect bool

	// SpanProcessors are processors to prepend to the telemetry pipeline.
	SpanProcessors []sdktrace.SpanProcessor

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
	LiveLogExporters []sdklog.Exporter

	// Resource is the resource describing this component and runtime
	// environment.
	Resource *resource.Resource
}

// NearlyImmediate is 100ms, below which has diminishing returns in terms of
// visual perception vs. performance cost.
const NearlyImmediate = 100 * time.Millisecond

// LiveTracesEnabled indicates that the configured OTEL_* exporter should be
// sent live span telemetry.
var LiveTracesEnabled = os.Getenv("OTEL_EXPORTER_OTLP_TRACES_LIVE") != ""

var SpanProcessors = []sdktrace.SpanProcessor{}
var LogProcessors = []sdklog.Processor{}

func InitEmbedded(ctx context.Context, res *resource.Resource) context.Context {
	traceCfg := Config{
		Detect:   false, // false, since we want "live" exporting
		Resource: res,
	}
	if exp, ok := ConfiguredSpanExporter(ctx); ok {
		traceCfg.LiveTraceExporters = append(traceCfg.LiveTraceExporters, exp)
	}
	if exp, ok := ConfiguredLogExporter(ctx); ok {
		traceCfg.LiveLogExporters = append(traceCfg.LiveLogExporters, exp)
	}
	return Init(ctx, traceCfg)
}

// Init sets up the global OpenTelemetry providers tracing, logging, and
// someday metrics providers. It is called by the CLI, the engine, and the
// container shim, so it needs to be versatile.
func Init(ctx context.Context, cfg Config) context.Context {
	// Set up a text map propagator so that things, well, propagate. The default
	// is a noop.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	// Inherit trace context from env if present.
	ctx = otel.GetTextMapPropagator().Extract(ctx, NewEnvCarrier(true))

	// Log to slog.
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		slog.Error("failed to emit telemetry", "error", err)
	}))

	if cfg.Resource == nil {
		cfg.Resource = FallbackResource()
	}

	if cfg.Detect {
		if exp, ok := ConfiguredSpanExporter(ctx); ok {
			if LiveTracesEnabled {
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

	SpanProcessors = cfg.SpanProcessors

	for _, exporter := range cfg.LiveTraceExporters {
		processor := NewLiveSpanProcessor(exporter)
		SpanProcessors = append(SpanProcessors, processor)
	}
	for _, exporter := range cfg.BatchedTraceExporters {
		processor := sdktrace.NewBatchSpanProcessor(exporter)
		SpanProcessors = append(SpanProcessors, processor)
	}
	for _, proc := range SpanProcessors {
		traceOpts = append(traceOpts, sdktrace.WithSpanProcessor(proc))
	}

	tracerProvider = sdktrace.NewTracerProvider(traceOpts...)

	// Register our TracerProvider as the global so any imported instrumentation
	// in the future will default to using it.
	//
	// NB: this is also necessary so that we can establish a root span, otherwise
	// telemetry doesn't work.
	otel.SetTracerProvider(tracerProvider)

	// Set up a log provider if configured.
	if len(cfg.LiveLogExporters) > 0 {
		logOpts := []sdklog.LoggerProviderOption{}
		for _, exp := range cfg.LiveLogExporters {
			processor := sdklog.NewBatchProcessor(exp,
				sdklog.WithExportInterval(NearlyImmediate))
			LogProcessors = append(LogProcessors, processor)
			logOpts = append(logOpts, sdklog.WithProcessor(processor))
		}
		loggerProvider = sdklog.NewLoggerProvider(logOpts...)

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
	if tracerProvider != nil {
		if err := tracerProvider.ForceFlush(ctx); err != nil {
			slog.Error("failed to flush spans", "error", err)
		}
	}
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
