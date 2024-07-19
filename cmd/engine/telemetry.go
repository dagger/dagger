package main

import (
	"context"
	"os"
	"time"

	"github.com/moby/buildkit/identity"
	"github.com/moby/buildkit/util/bklog"
	"github.com/sirupsen/logrus"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/engine"
	enginetel "github.com/dagger/dagger/engine/telemetry"
)

const (
	InstrumentationScopeName = "dagger.io/engine"
)

var (
	engineName string

	rootSpan trace.Span

	engineTraceProvider  *sdktrace.TracerProvider
	engineLoggerProvider *sdklog.LoggerProvider
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

func InitTelemetry(ctx context.Context) (context.Context, *enginetel.PubSub) {
	otelResource := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceNameKey.String("dagger-engine"),
		semconv.ServiceVersionKey.String(engine.Version),
		semconv.HostNameKey.String(engineName),
	)

	// Send engine telemetry to Cloud if configured.
	if _, logs, ok := enginetel.ConfiguredCloudExporters(ctx); ok {
		// TODO revive if/when we want engine logs to correlate to a trace
		// spanProcessor := sdktrace.NewBatchSpanProcessor(spans)
		// engineTraceProvider = sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanProcessor))

		// ctx, rootSpan = engineTraceProvider.Tracer(InstrumentationScopeName).Start(ctx, "dagger engine")

		engineLoggerProvider = sdklog.NewLoggerProvider(
			sdklog.WithResource(otelResource),
			sdklog.WithProcessor(sdklog.NewBatchProcessor(logs)),
		)
		logrus.AddHook(&otelLogrusHook{
			rootSpan: rootSpan,
			logger:   engineLoggerProvider.Logger(InstrumentationScopeName),
		})
	}

	pubsub := enginetel.NewPubSub()
	ctx = telemetry.Init(ctx, telemetry.Config{
		Resource: otelResource,

		// TODO: should this be true now that all userland telemetry goes to
		// local client tracer providers?
		Detect: false,
	})

	return ctx, pubsub
}

func CloseTelemetry() {
	telemetry.Close()

	if rootSpan != nil {
		rootSpan.End()
	}

	type shutdowner interface {
		Shutdown(context.Context) error
	}

	shutdown := func(shutdowner shutdowner) {
		timeout := 30 * time.Second
		shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		bklog.G(shutdownCtx).Debugf("Shutting down %T (timeout=%s)", shutdowner, timeout)
		shutdowner.Shutdown(shutdownCtx)
	}

	if engineTraceProvider != nil {
		shutdown(engineTraceProvider)
	}

	if engineLoggerProvider != nil {
		shutdown(engineLoggerProvider)
	}
}
