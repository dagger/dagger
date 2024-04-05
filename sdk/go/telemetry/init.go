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
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
)

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

		slog.Debug("configuring tracing via env", "protocol", proto)

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

func InitEmbedded(ctx context.Context, res *resource.Resource) context.Context {
	traceCfg := Config{
		Detect:   false, // false, since we want "live" exporting
		Resource: res,
	}
	if exp, ok := ConfiguredSpanExporter(ctx); ok {
		traceCfg.LiveTraceExporters = append(traceCfg.LiveTraceExporters, exp)
	}
	return Init(ctx, traceCfg)
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

	// Resource is the resource describing this component and runtime
	// environment.
	Resource *resource.Resource
}

// NearlyImmediate is 100ms, below which has diminishing returns in terms of
// visual perception vs. performance cost.
const NearlyImmediate = 100 * time.Millisecond

// LiveSpanProcessor is a SpanProcessor that can additionally receive updates
// for a span at runtime, rather than waiting until the span ends.
type LiveSpanProcessor interface {
	sdktrace.SpanProcessor

	// OnUpdate method enqueues a trace.ReadOnlySpan for later processing.
	OnUpdate(s sdktrace.ReadOnlySpan)
}

var SpanProcessors = []sdktrace.SpanProcessor{}
var tracerProvider *ProxyTraceProvider

// Init sets up the global OpenTelemetry providers tracing, logging, and
// someday metrics providers. It is called by the CLI, the engine, and the
// container shim, so it needs to be versatile.
func Init(ctx context.Context, cfg Config) context.Context {
	slog.Debug("initializing telemetry")

	if p, ok := os.LookupEnv("TRACEPARENT"); ok {
		slog.Debug("found TRACEPARENT", "value", p)
		ctx = propagation.TraceContext{}.Extract(ctx, propagation.MapCarrier{"traceparent": p})
	}

	// Set up a text map propagator so that things, well, propagate. The default
	// is a noop.
	otel.SetTextMapPropagator(propagation.TraceContext{})

	// Log to slog.
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		slog.Error("OpenTelemetry error", "error", err)
	}))

	if cfg.Detect {
		if exp, ok := ConfiguredSpanExporter(ctx); ok {
			cfg.BatchedTraceExporters = append(cfg.BatchedTraceExporters, exp)
		}
	}

	traceOpts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(cfg.Resource),
	}

	for _, exporter := range cfg.BatchedTraceExporters {
		traceOpts = append(traceOpts, sdktrace.WithBatcher(exporter))
	}

	liveProcessors := make([]LiveSpanProcessor, 0, len(cfg.LiveTraceExporters))
	for _, exporter := range cfg.LiveTraceExporters {
		processor := NewBatchSpanProcessor(exporter,
			WithBatchTimeout(NearlyImmediate))
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

	tracerProvider = NewProxyTraceProvider(
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

	return ctx
}

// Flush drains telemetry data, and is typically called just before a client
// goes away.
//
// NB: now that we wait for all spans to complete, this is less necessary, but
// it seems wise to keep it anyway, as the spots where it are needed are hard
// to find.
func Flush(ctx context.Context) {
	slog.Debug("flushing processors")
	if tracerProvider != nil {
		if err := tracerProvider.ForceFlush(ctx); err != nil {
			slog.Error("failed to flush spans", "error", err)
		}
	}
	slog.Debug("done flushing processors")
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
}
