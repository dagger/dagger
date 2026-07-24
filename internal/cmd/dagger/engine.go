package daggercmd

import (
	"context"
	"fmt"
	"os"

	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/client/imageload"
	"github.com/dagger/dagger/engine/client/pathutil"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/dagger/engine/slog"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/internal/cloud/auth"
	"github.com/dagger/dagger/util/cleanups"
	telemetry "github.com/dagger/otel-go"
	"github.com/muesli/termenv"
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
	tag := engineVersion(engine.Tag)
	if tag == "" {
		// can happen during naive dev builds (so just fallback to something
		// semi-reasonable)
		return "container://" + distconsts.EngineContainerName
	}
	return runnerHostForEngineVersion(tag)
}

func engineVersion(tag string) string {
	if tag == "" {
		return ""
	}
	if os.Getenv(GPUSupportEnv) != "" {
		tag += "-gpu"
	}
	return tag
}

func runnerHostForEngineVersion(version string) string {
	return fmt.Sprintf("image://%s:%s", engine.EngineImageRepo, version)
}

type runClientCallback func(context.Context, *client.Client) error

func withEngine(
	ctx context.Context,
	params client.Params,
	fn runClientCallback,
) (rerr error) {
	if err := applyWorkspaceClientParams(&params); err != nil {
		return err
	}
	coreModuleSelected := isCoreModuleSelected()
	if coreModuleSelected {
		params.LoadWorkspaceModules = false
	}
	if !moduleNoURL {
		if modRef, _ := getExplicitModuleSourceRef(); modRef != "" {
			if !isCoreModuleRef(modRef) {
				params.Module = modRef
			}
		}
	}
	if sessionWorkspace != "" && params.Workspace == nil {
		params.Workspace = &sessionWorkspace
	}
	return Frontend.Run(ctx, opts, func(ctx context.Context) (_ cleanups.CleanupF, rerr error) {
		var cleanup cleanups.Cleanups

		// Init tracing as early as possible and shutdown after the command
		// completes, ensuring progress is fully flushed to the frontend.
		ctx, cleanupTelemetry := initClientTelemetry(ctx)

		otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
			if opts.Debug {
				slog.Error("failed to emit telemetry", "error", err)
			}
			Frontend.SetTelemetryError(err)
		}))
		cleanup.Add("close telemetry", func() error {
			cleanupTelemetry(rerr)
			return nil
		})

		params, err := finalizeEngineParams(ctx, params)
		if err != nil {
			return cleanup.Run, err
		}

		// Connect to and run with the engine
		sess, err := client.Connect(ctx, params)
		if err != nil {
			return cleanup.Run, err
		}
		cleanup.Add("close dagger session", sess.Close)

		Frontend.SetClient(sess.Dagger())

		return cleanup.Run, fn(ctx, sess)
	})
}

// finalizeEngineParams fills in the run-scoped client params that depend on the
// frontend and telemetry being set up. Must be called inside Frontend.Run,
// after initClientTelemetry. Shared by withEngine and withSetupSessions.
func finalizeEngineParams(ctx context.Context, params client.Params) (client.Params, error) {
	if debugFlag {
		params.LogLevel = slog.LevelDebug
	}

	if useCloudEngine {
		params.RunnerHost = engine.DefaultCloudRunnerHost
	} else if params.RunnerHost == "" {
		params.RunnerHost = RunnerHost
	}

	if RunnerImageLoader != "" {
		backend, err := imageload.GetBackend(RunnerImageLoader)
		if err != nil {
			return params, err
		}
		params.ImageLoaderBackend = backend
	}

	params.AllowedLLMModules = allowedLLMModules

	params.Profile = profileFlag

	params.CloudURLCallback = Frontend.SetCloudURL

	// setup exporters that will subscribe to engine telemetry.
	// by default it should only be the frontend unless the user
	// specifies additional ones via OTEL_* variables which the
	// client then will pick up.
	traceExporters := []sdktrace.SpanExporter{}
	logExporters := []sdklog.Exporter{}
	metricExporters := []sdkmetric.Exporter{}

	// if silent is set, don't set default exporters to avoid subscribing
	// to telemetry unnecessarily
	if !silent {
		traceExporters = append(traceExporters, Frontend.SpanExporter())
		logExporters = append(logExporters, Frontend.LogExporter())
		metricExporters = append(metricExporters, Frontend.MetricExporter())
	}

	if exp, ok := telemetry.ConfiguredSpanExporter(ctx); ok {
		if !telemetry.LiveTracesEnabled {
			exp = telemetry.FilterLiveSpansExporter{SpanExporter: exp}
		}
		traceExporters = append(traceExporters, exp)
	}
	if exp, ok := telemetry.ConfiguredLogExporter(ctx); ok {
		logExporters = append(logExporters, exp)
	}
	if exp, ok := telemetry.ConfiguredMetricExporter(ctx); ok {
		metricExporters = append(metricExporters, exp)
	}

	if len(traceExporters) > 0 {
		params.EngineTrace = enginetel.MultiSpanExporter(traceExporters)
	}
	if len(logExporters) > 0 {
		params.EngineLogs = enginetel.MultiLogExporter(logExporters)
	}
	if len(metricExporters) > 0 {
		params.EngineMetrics = metricExporters
	}

	params.WithTerminal = withTerminal

	params.Interactive = interactive
	params.InteractiveCommand = interactiveCommandParsed

	effectiveLockMode, err := resolveLockMode(params.LockMode, lockMode)
	if err != nil {
		return params, err
	}
	params.LockMode = effectiveLockMode

	if hasTTY {
		params.PromptHandler = Frontend
	}

	ca, err := auth.GetCloudAuth(ctx)
	if err != nil {
		return params, err
	}
	params.CloudAuth = ca

	return params, nil
}

// withSetupSessions runs fn under a single Frontend (one live TUI) while letting
// it open more than one engine session via connect. dagger setup needs both:
// its prompts are Frontend forms (which require the single-TUI run), and its
// recommended-module install must run in a FRESH session so it re-detects the
// workspace migrated earlier in the same command — the per-client workspace is
// detected once and cached for a session's lifetime, so reusing the migrate
// session would keep seeing the legacy dagger.json ("run dagger setup first").
func withSetupSessions(
	ctx context.Context,
	fn func(ctx context.Context, connect func(context.Context) (*client.Client, func(), error)) error,
) (rerr error) {
	params := client.Params{
		SkipWorkspaceModules:           true,
		SuppressCompatWorkspaceWarning: true,
	}
	if err := applyWorkspaceClientParams(&params); err != nil {
		return err
	}
	if sessionWorkspace != "" && params.Workspace == nil {
		params.Workspace = &sessionWorkspace
	}
	return Frontend.Run(ctx, opts, func(ctx context.Context) (_ cleanups.CleanupF, rerr error) {
		var cleanup cleanups.Cleanups

		ctx, cleanupTelemetry := initClientTelemetry(ctx)
		otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
			if opts.Debug {
				slog.Error("failed to emit telemetry", "error", err)
			}
			Frontend.SetTelemetryError(err)
		}))
		cleanup.Add("close telemetry", func() error {
			cleanupTelemetry(rerr)
			return nil
		})

		fp, err := finalizeEngineParams(ctx, params)
		if err != nil {
			return cleanup.Run, err
		}

		connect := func(ctx context.Context) (*client.Client, func(), error) {
			sess, err := client.Connect(ctx, fp)
			if err != nil {
				return nil, nil, err
			}
			Frontend.SetClient(sess.Dagger())
			return sess, func() { _ = sess.Close() }, nil
		}

		return cleanup.Run, fn(ctx, connect)
	})
}

func applyWorkspaceClientParams(params *client.Params) error {
	if params.Workspace == nil && workspaceRef != "" {
		ref := workspaceRef
		if !isObviouslyRemoteWorkspaceRef(ref) {
			// --workdir answers where this CLI command is running from. -W
			// answers which workspace the user selected. If -W is relative, it
			// follows the command cwd after --workdir has been applied:
			// `--workdir /work/shell -W ./ws` selects /work/shell/ws. Send that
			// host path to the engine; the engine still owns workspace detection
			// from there: git root, config, lock, compat. Remote refs stay
			// untouched for engine-side git parsing.
			absRef, err := pathutil.Abs(ref)
			if err != nil {
				return fmt.Errorf("resolve workspace: %w", err)
			}
			ref = absRef
		}
		params.Workspace = &ref
	}
	if params.WorkspaceEnv == nil && workspaceEnv != "" {
		env := workspaceEnv
		params.WorkspaceEnv = &env
	}
	return nil
}

func resolveLockMode(paramLockMode, globalLockMode string) (string, error) {
	effective := paramLockMode
	if effective == "" {
		effective = globalLockMode
	}
	if effective == "" {
		return "", nil
	}

	mode, err := workspace.ParseLockMode(effective)
	if err != nil {
		return "", err
	}
	return string(mode), nil
}

// skipSharedTelemetryExporters, when set, makes clientTelemetryConfig leave out
// the process-wide OTLP exporter singletons (Dagger Cloud + the OTEL_* "Detect"
// exporters). It is toggled by withEngineSilent for internal plumbing sessions;
// see clientTelemetryConfig for why.
var skipSharedTelemetryExporters bool

// clientTelemetryConfig builds the telemetry pipeline for the local CLI/client
// process. Engine telemetry is subscribed to separately and exported by the
// engine itself when Cloud export is configured.
//
// Internal plumbing sessions (see skipSharedTelemetryExporters) opt out of the
// process-wide OTLP exporter singletons — the Dagger Cloud exporters and the
// OTEL_* "Detect" exporters. Those singletons are sync.Once-cached and get shut
// down by telemetry.Close(); a pre-command session that wired them up and then
// tore them down would leave them dead for the real command that runs next in
// the same process, surfacing "HTTP exporter is shutdown" / "context canceled"
// telemetry warnings (e.g. the second session opened by `dagger module init`).
// Such sessions render to a discard frontend and have no reason to export the
// client's own telemetry to Cloud, so they simply skip the shared exporters.
func clientTelemetryConfig(ctx context.Context) telemetry.Config {
	cfg := telemetry.Config{
		Detect:   !skipSharedTelemetryExporters,
		Resource: Resource(ctx),

		LiveTraceExporters:  []sdktrace.SpanExporter{Frontend.SpanExporter()},
		LiveLogExporters:    []sdklog.Exporter{Frontend.LogExporter()},
		LiveMetricExporters: []sdkmetric.Exporter{Frontend.MetricExporter()},
	}
	if !skipSharedTelemetryExporters {
		if spans, logs, metrics, ok := enginetel.ConfiguredCloudExporters(ctx); ok {
			// Wrap the Cloud span exporter in a LARGE-queue live processor instead of
			// letting telemetry.Init wrap it with the default 2048-slot BSP, so the
			// CLI→Cloud hop does not silently drop spans on a big burst — a cold engine
			// build is ~15k spans, live-double-emitted ≈ 30k records. The wcprof
			// completeness carrier rides at the tail and was the first thing to drop;
			// this keeps the exported trace complete. (SpanProcessors are prepended to
			// the pipeline by telemetry.Init, same as a LiveTraceExporter would be.)
			cfg.SpanProcessors = append(cfg.SpanProcessors, enginetel.NewLargeQueueLiveSpanProcessor(spans))
			cfg.LiveLogExporters = append(cfg.LiveLogExporters, logs)
			cfg.LiveMetricExporters = append(cfg.LiveMetricExporters, metrics)
		}
	}
	return cfg
}

func initClientTelemetry(ctx context.Context) (context.Context, func(error)) {
	ctx = telemetry.Init(ctx, clientTelemetryConfig(ctx))
	// telemetry.Init extracts inherited OTel baggage from the environment.
	// Re-apply explicit local process settings afterward so a nested Dagger
	// command's own NO_COLOR/debug request wins over parent baggage.
	if termenv.EnvNoColor() {
		ctx = slog.ContextWithColorMode(ctx, true)
	}
	if debugFlag {
		ctx = slog.ContextWithDebugMode(ctx, true)
	}

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
	oldOut := rootCmd.OutOrStdout()
	oldErr := rootCmd.ErrOrStderr()
	rootCmd.SetOut(stdio.Stdout)
	rootCmd.SetErr(stdio.Stderr)

	return ctx, func(rerr error) {
		rootCmd.SetOut(oldOut)
		rootCmd.SetErr(oldErr)
		stdio.Close()
		telemetry.EndWithCause(span, &rerr)
		telemetry.Close()
	}
}
