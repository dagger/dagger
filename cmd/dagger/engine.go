package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"dagger.io/dagger"
	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/dagger/engine/slog"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

const (
	GPUSupportEnv = "_EXPERIMENTAL_DAGGER_GPU_SUPPORT"
	RunnerHostEnv = "_EXPERIMENTAL_DAGGER_RUNNER_HOST"
)

var (
	// RunnerHost holds the host to connect to.
	//
	// Note: this is filled at link-time.
	RunnerHost string
)

func init() {
	if v, ok := os.LookupEnv(RunnerHostEnv); ok {
		RunnerHost = v
	}
	if RunnerHost == "" {
		RunnerHost = defaultRunnerHost()
	}
}

func defaultRunnerHost() string {
	tag := engine.Tag
	if tag == "" {
		// can happen during naive dev builds (so just fallback to something
		// semi-reasonable)
		return "docker-container://" + distconsts.EngineContainerName
	}
	if os.Getenv(GPUSupportEnv) != "" {
		tag += "-gpu"
	}
	return fmt.Sprintf("docker-image://%s:%s", engine.EngineImageRepo, tag)
}

type runClientCallback func(context.Context, *client.Client) error

func withEngine(
	ctx context.Context,
	params client.Params,
	fn runClientCallback,
) error {
	return Frontend.Run(ctx, opts, func(ctx context.Context) (rerr error) {
		// Init tracing as early as possible and shutdown after the command
		// completes, ensuring progress is fully flushed to the frontend.
		ctx, cleanupTelemetry := initEngineTelemetry(ctx)
		defer func() { cleanupTelemetry(rerr) }()

		if debug {
			params.LogLevel = slog.LevelDebug
		}

		if params.RunnerHost == "" {
			params.RunnerHost = RunnerHost
		}

		params.DisableHostRW = disableHostRW

		params.EngineCallback = Frontend.ConnectedToEngine
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

		// Connect to and run with the engine
		sess, ctx, err := client.Connect(ctx, params)
		if err != nil {
			return err
		}
		defer sess.Close()

		// Automatically serve the module in the context directory if available.
		if params.ServeModule {
			mod, exist, err := initializeClientGeneratorModule(ctx, sess.Dagger(), ".")
			if err != nil && !errors.Is(err, ErrConfigNotFound) {
				return fmt.Errorf("failed to initialize current module: %w", err)
			}

			if exist {
				for _, dep := range mod.Dependencies {
					depKind, err := dep.Kind(ctx)
					if err != nil {
						return fmt.Errorf("failed to get dependency kind: %w", err)
					}

					// Skip remote dependencies because they are already serves during the call to `Connect`
					if depKind == dagger.ModuleSourceKindGitSource {
						continue
					}

					err = dep.AsModule().Serve(ctx)
					if err != nil {
						return fmt.Errorf("failed to serve dependency %w", err)
					}
				}

				sdkSource, err := mod.Source.SDK().Source(ctx)
				if err != nil {
					return fmt.Errorf("failed to get module SDK source: %w", err)
				}

				if sdkSource != "" {
					err := mod.Source.AsModule().Serve(ctx)
					if err != nil {
						return fmt.Errorf("failed to serve module source: %w", err)
					}
				}
			}
		}

		return fn(ctx, sess)
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
	ctx, span := Tracer().Start(ctx, spanName(os.Args))

	// Set up global slog to log to the primary span output.
	slog.SetDefault(slog.SpanLogger(ctx, InstrumentationLibrary))

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
