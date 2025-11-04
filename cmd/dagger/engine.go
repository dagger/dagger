package main

import (
	"context"
	"fmt"
	"os"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/client/imageload"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/dagger/engine/slog"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/util/cleanups"
	"go.opentelemetry.io/otel"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

const (
	GPUSupportEnv        = "_EXPERIMENTAL_DAGGER_GPU_SUPPORT"
	RunnerHostEnv        = "_EXPERIMENTAL_DAGGER_RUNNER_HOST"
	RunnerImageLoaderEnv = "_EXPERIMENTAL_DAGGER_RUNNER_IMAGESTORE"
	TraceNameEnv         = "DAGGER_TRACE_NAME"
)

var (
	// RunnerHost holds the host to connect to.
	//
	// Note: this is filled at link-time.
	RunnerHost string

	// RunnerImageLoader holds the image store for the client.
	RunnerImageLoader string
)

func init() {
	if v, ok := os.LookupEnv(RunnerHostEnv); ok {
		RunnerHost = v
	}
	if RunnerHost == "" {
		RunnerHost = defaultRunnerHost()
	}

	RunnerImageLoader = os.Getenv(RunnerImageLoaderEnv)
}

func defaultRunnerHost() string {
	tag := engine.Tag
	if tag == "" {
		// can happen during naive dev builds (so just fallback to something
		// semi-reasonable)
		return "container://" + distconsts.EngineContainerName
	}
	if os.Getenv(GPUSupportEnv) != "" {
		tag += "-gpu"
	}
	return fmt.Sprintf("image://%s:%s", engine.EngineImageRepo, tag)
}

type runClientCallback func(context.Context, *client.Client) error

func withEngine(
	ctx context.Context,
	params client.Params,
	fn runClientCallback,
) (rerr error) {
	return Frontend.Run(ctx, opts, func(ctx context.Context) (_ cleanups.CleanupF, rerr error) {
		var cleanup cleanups.Cleanups

		// Init tracing as early as possible and shutdown after the command
		// completes, ensuring progress is fully flushed to the frontend.
		ctx, cleanupTelemetry := initEngineTelemetry(ctx)

		otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
			if opts.Debug {
				slog.Error("failed to emit telemetry", "error", err)
			}
			Frontend.Opts().TelemetryError = err
		}))
		cleanup.Add("close telemetry", func() error {
			cleanupTelemetry(rerr)
			return nil
		})

		if debugFlag {
			params.LogLevel = slog.LevelDebug
		}

		if useCloudEngine {
			params.RunnerHost = "dagger-cloud://default-engine-config.dagger.cloud"
		} else if params.RunnerHost == "" {
			params.RunnerHost = RunnerHost
		}

		if RunnerImageLoader != "" {
			backend, err := imageload.GetBackend(RunnerImageLoader)
			if err != nil {
				return cleanup.Run, err
			}
			params.ImageLoaderBackend = backend
		}

		params.DisableHostRW = disableHostRW
		params.AllowedLLMModules = allowedLLMModules

		params.CloudURLCallback = Frontend.SetCloudURL

		params.EngineTrace = telemetry.SpanForwarder{
			Processors: telemetry.SpanProcessors,
		}
		params.EngineLogs = telemetry.LogForwarder{
			Processors: telemetry.LogProcessors,
		}
		params.EngineMetrics = telemetry.MetricExporters

		params.WithTerminal = withTerminal

		params.Interactive = interactive
		params.InteractiveCommand = interactiveCommandParsed

		if hasTTY {
			params.PromptHandler = Frontend
		}

		// Connect to and run with the engine
		sess, ctx, err := client.Connect(ctx, params)
		if err != nil {
			return cleanup.Run, err
		}
		cleanup.Add("close dagger session", sess.Close)

		Frontend.SetClient(sess.Dagger())

		return cleanup.Run, fn(ctx, sess)
	})
}

func initEngineTelemetry(ctx context.Context) (context.Context, func(error)) {
	// Setup telemetry config
	telemetryCfg := telemetry.Config{
		Detect:   true,
		Resource: Resource(ctx),

		LiveTraceExporters:  []sdktrace.SpanExporter{Frontend.SpanExporter()},
		LiveLogExporters:    []sdklog.Exporter{Frontend.LogExporter()},
		LiveMetricExporters: []sdkmetric.Exporter{Frontend.MetricExporter()},
	}
	if spans, logs, metrics, ok := enginetel.ConfiguredCloudExporters(ctx); ok {
		telemetryCfg.LiveTraceExporters = append(telemetryCfg.LiveTraceExporters, spans)
		telemetryCfg.LiveLogExporters = append(telemetryCfg.LiveLogExporters, logs)
		telemetryCfg.LiveMetricExporters = append(telemetryCfg.LiveMetricExporters, metrics)
	}
	ctx = telemetry.Init(ctx, telemetryCfg)

	// Set the full command string as the name of the root span.
	//
	// If you pass credentials in plaintext, yes, they will be leaked; don't do
	// that, since they will also be leaked in various other places (like the
	// process tree). Use Secret arguments instead.
	name := spanName(os.Args)
	if os.Getenv(TraceNameEnv) != "" {
		name = os.Getenv(TraceNameEnv)
	}
	ctx, span := Tracer().Start(ctx, name)

	// Set up global slog to log to the primary span output.
	slog.SetDefault(slog.SpanLogger(ctx, InstrumentationLibrary))

	// Set the root span as the target for "global logs"
	ctx = telemetry.ContextWithGlobalLogsSpan(ctx)

	// Set the span as the primary span for the frontend.
	Frontend.SetPrimary(dagui.SpanID{SpanID: span.SpanContext().SpanID()})

	// Direct command stdout/stderr to span stdio via OpenTelemetry.
	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary)
	rootCmd.SetOut(stdio.Stdout)
	rootCmd.SetErr(stdio.Stderr)

	return ctx, func(rerr error) {
		stdio.Close()
		telemetry.End(span, func() error { return rerr })
		telemetry.Close()
	}
}
