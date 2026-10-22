package daggercmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/client/imageload"
	"github.com/dagger/dagger/engine/client/pathutil"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/dagger/engine/slog"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/internal/cloud/auth"
	"github.com/dagger/dagger/internal/cmd/dagger/llmconfig"
	"github.com/dagger/dagger/util/cleanups"
	telemetry "github.com/dagger/otel-go"
	"github.com/muesli/termenv"
	"go.opentelemetry.io/otel"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

const (
	GPUSupportEnv = "_EXPERIMENTAL_DAGGER_GPU_SUPPORT"

	// engineEnv is the environment form of --engine. It takes the same values.
	engineEnv = "DAGGER_ENGINE"

	// cloudEngineEnv and RunnerHostEnv are soft-deprecated, like the --cloud
	// flag: --engine and DAGGER_ENGINE replace them, but they still work as a
	// fallback.
	cloudEngineEnv = "DAGGER_CLOUD_ENGINE"
	RunnerHostEnv  = "_EXPERIMENTAL_DAGGER_RUNNER_HOST"

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

type disableFrontendTelemetryKey struct{}

func withoutFrontendTelemetry(ctx context.Context) context.Context {
	return context.WithValue(ctx, disableFrontendTelemetryKey{}, true)
}

func frontendTelemetryDisabled(ctx context.Context) bool {
	disabled, _ := ctx.Value(disableFrontendTelemetryKey{}).(bool)
	return disabled
}

func withEngine(
	ctx context.Context,
	params client.Params,
	fn runClientCallback,
) error {
	return withEngineAction(ctx, "", params, fn)
}

// Metadata discovery can run before Cobra has parsed the full command. Give it
// its own frontend and exporters, then restore the execution session's state.
func withEngineMetadata(ctx context.Context, action string, params client.Params, fn runClientCallback) error {
	oldFrontend, oldOpts := Frontend, opts
	oldProgress, oldHasTTY := progress, hasTTY
	oldSkip := skipSharedTelemetryExporters
	defer func() {
		Frontend, opts = oldFrontend, oldOpts
		progress, hasTTY = oldProgress, oldHasTTY
		skipSharedTelemetryExporters = oldSkip
	}()

	configureProgressOptions()
	opts.Verbosity = dagui.HideCompletedVerbosity + verbose - quiet
	opts.OpenWeb = false
	opts.NoExit = false
	opts.DotOutputFilePath = ""
	skipSharedTelemetryExporters = true
	if err := resolveProgressFrontend(); err != nil {
		return err
	}
	return withEngineAction(ctx, action, params, fn)
}

func withEngineAction(
	ctx context.Context,
	action string,
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
		ctx, endCommand, cleanupTelemetry := initEngineTelemetry(ctx)
		if action != "" {
			var span trace.Span
			ctx, span = Tracer().Start(ctx, action, telemetry.Encapsulate())
			defer telemetry.EndWithCause(span, &rerr)
		}

		otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
			if opts.Debug {
				slog.Error("failed to emit telemetry", "error", err)
			}
			Frontend.SetTelemetryError(err)
		}))
		cleanup.Add("close telemetry", func() error {
			return cleanupTelemetry(rerr)
		})

		params, err := finalizeEngineParams(ctx, params)
		if err != nil {
			return cleanup.Run, err
		}
		params.EngineLogsPresence = liveLogsWanted(ctx)

		// Connect to and run with the engine
		sess, err := client.Connect(ctx, params)
		if err != nil {
			return cleanup.Run, err
		}
		cleanup.Add("close dagger session", sess.Close)
		detach := cliLocalTelemetry.attach(ctx, sess)
		traceID := dagui.TraceID{TraceID: trace.SpanContextFromContext(ctx).TraceID()}
		if live := startLiveLogs(ctx, Frontend, sess, sess.LogsPresence(), traceID); live != nil {
			// After the command's telemetry is handed over, so the store
			// holds all of its output, and before the session closes.
			cleanup.Add("read the final logs", func() error {
				finishLiveLogs(ctx, sess, live)
				return nil
			})
		}
		cleanup.Add("hand the command's telemetry to the engine", func() error {
			// End the command first, so the engine stores its span and
			// output whole before the session closes.
			endCommand(rerr)
			detach()
			return nil
		})

		Frontend.SetClient(sess.Dagger())

		return cleanup.Run, fn(ctx, sess)
	})
}

// finalizeEngineParams fills in the run-scoped client params that depend on the
// frontend and telemetry being set up. Must be called inside Frontend.Run,
// after initEngineTelemetry. Shared by withEngine and withSetupSessions.
func finalizeEngineParams(ctx context.Context, params client.Params) (client.Params, error) {
	if debugFlag {
		params.LogLevel = slog.LevelDebug
	}

	if selectedEngine() != "" || params.RunnerHost == "" {
		params.RunnerHost = configuredRunnerHost()
	}

	if RunnerImageLoader != "" {
		backend, err := imageload.GetBackend(RunnerImageLoader)
		if err != nil {
			return params, err
		}
		params.ImageLoaderBackend = backend
	}

	params.AllowedLLMModules = allowedLLMModules

	params.GitRemote, params.WorkspacePath = cloudWorkspace(ctx, params.Workspace)

	params.Profile = profileFlag

	params.CloudURLCallback = func(ctx context.Context, url string, msg string, logged bool) {
		Frontend.SetCloudURL(ctx, url, msg, logged)
		// A trace goes to a Cloud org: show any notice Cloud has about it
		// after the command.
		if logged {
			startCloudNotice(ctx, os.Stderr)
		}
	}

	// Engine telemetry goes everywhere this process's own does, but back to
	// the engine through local telemetry.
	spanProcessors := telemetry.SpanProcessors
	logProcessors := telemetry.LogProcessors
	metricExporters := telemetry.MetricExporters
	local := cliLocalTelemetry
	if local != nil {
		spanProcessors = withoutIndex(spanProcessors, local.spans)
		logProcessors = withoutIndex(logProcessors, local.logs)
		metricExporters = withoutIndex(metricExporters, local.metrics)
	}
	params.EngineTrace = flushableSpanForwarder{telemetry.SpanForwarder{
		Processors: spanProcessors,
	}}
	// The engine names the session (Query.setSessionTitle) with a span-name
	// record on our primary span; apply it to the live span too, so the span
	// we export carries the title like the frontend shows it.
	namer := primarySpanNamer{span: trace.SpanFromContext(ctx)}
	params.EngineLogs = telemetry.LogForwarder{
		Processors: append(slices.Clone(logProcessors), namer),
	}
	params.EngineMetrics = metricExporters
	if local != nil && local.cloud != nil {
		// Ask the engine to publish the session's telemetry to Cloud itself,
		// and have what it would send us forwarded everywhere else. Only an
		// engine that does not publish has it forwarded to Cloud too.
		params.EngineCloudTelemetry = true
		params.CloudURL = os.Getenv("DAGGER_CLOUD_URL")
		params.CloudCredentialsPath = auth.CredentialsFile()
		params.EngineTraceWithoutCloud = params.EngineTrace
		params.EngineLogsWithoutCloud = params.EngineLogs
		params.EngineMetricsWithoutCloud = params.EngineMetrics
		params.EngineTrace = flushableSpanForwarder{telemetry.SpanForwarder{
			Processors: append(slices.Clone(spanProcessors), local.cloud.forwardedSpans),
		}}
		params.EngineLogs = telemetry.LogForwarder{
			Processors: append(slices.Clone(logProcessors), local.cloud.forwardedLogs, namer),
		}
		params.EngineMetrics = append(slices.Clone(metricExporters), local.cloud.metrics)
	}

	params.WithTerminal = withTerminal

	params.Interactive = shellOnError
	params.InteractiveCommand = shellCommandOnErrorParsed

	if hasTTY {
		params.PromptHandler = Frontend
		var err error
		params.SSHAskpassExecutable, err = os.Executable()
		if err != nil {
			return params, err
		}
	}

	ca, checked := ctx.Value(doctorCloudAuthKey{}).(*auth.Cloud)
	if !checked {
		var err error
		ca, err = auth.GetCloudAuth(ctx)
		if err != nil {
			return params, err
		}
	}
	params.CloudAuth = ca

	return params, nil
}

// primarySpanNamer applies span-name records the engine publishes for the
// CLI's own primary span (Query.setSessionTitle) to that live span, so the
// span this process exports ends up with the title too. Everything else about
// the rename -- frontend, Cloud, archive -- follows from the record itself.
type primarySpanNamer struct {
	span trace.Span
}

var _ sdklog.Processor = primarySpanNamer{}

func (n primarySpanNamer) OnEmit(_ context.Context, rec *sdklog.Record) error {
	if rec == nil || n.span == nil || !n.span.IsRecording() {
		return nil
	}
	if rec.SpanID() != n.span.SpanContext().SpanID() || !dagui.IsSpanNameRecord(*rec) {
		return nil
	}
	if name, ok := dagui.LogBodyString(*rec); ok && name != "" {
		n.span.SetName(name)
	}
	return nil
}

func (primarySpanNamer) Shutdown(context.Context) error   { return nil }
func (primarySpanNamer) ForceFlush(context.Context) error { return nil }
func (primarySpanNamer) Enabled(context.Context, sdklog.EnabledParameters) bool {
	return true
}

// selectedEngine returns the engine selector, or "" when nothing selects an
// engine. Flags outrank the environment, and the current inputs outrank the
// deprecated ones:
//
//  1. --engine
//  2. --cloud            (deprecated flag)
//  3. DAGGER_ENGINE
//  4. DAGGER_CLOUD_ENGINE (deprecated)
//
// _EXPERIMENTAL_DAGGER_RUNNER_HOST is deprecated too, but it reaches the CLI
// as RunnerHost, so configuredRunnerHost handles it below all of these.
func selectedEngine() string {
	if engineFlag != "" {
		return engineFlag
	}
	if cloudFlag {
		return "cloud"
	}
	if value := os.Getenv(engineEnv); value != "" {
		return value
	}
	if cloudEngineEnvSet {
		return "cloud"
	}
	return ""
}

func configuredRunnerHost() string {
	switch selected := selectedEngine(); selected {
	case "":
		return RunnerHost
	case "cloud":
		return engine.DefaultCloudRunnerHost
	default:
		return selected
	}
}

// withSetupSessions runs initialization, migration, and optional setup under
// one frontend. Each operation requests a session when it needs the engine.
func withSetupSessions(
	ctx context.Context,
	before func(context.Context),
	fn func(ctx context.Context, connect func(context.Context) (*client.Client, func(), error)) error,
) error {
	return withLazySessions(ctx, client.Params{
		SkipWorkspaceModules:           true,
		SuppressCompatWorkspaceWarning: true,
	}, before, fn)
}

// withLazySessions runs fn under one frontend and trace without connecting to
// the engine. Each operation requests a session with params when it needs one.
func withLazySessions(
	ctx context.Context,
	params client.Params,
	before func(context.Context),
	fn func(ctx context.Context, connect func(context.Context) (*client.Client, func(), error)) error,
) (rerr error) {
	if err := applyWorkspaceClientParams(&params); err != nil {
		return err
	}
	if sessionWorkspace != "" && params.Workspace == nil {
		params.Workspace = &sessionWorkspace
	}
	return Frontend.Run(ctx, opts, func(ctx context.Context) (_ cleanups.CleanupF, rerr error) {
		var cleanup cleanups.Cleanups
		if before != nil {
			before(ctx)
		}

		// Telemetry deliberately starts after the untraced setup phase (Cloud
		// login), so credentials established there apply to the whole trace.
		ctx, _, cleanupTelemetry := initEngineTelemetry(ctx)
		otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
			if opts.Debug {
				slog.Error("failed to emit telemetry", "error", err)
			}
			Frontend.SetTelemetryError(err)
		}))
		cleanup.Add("close telemetry", func() error {
			return cleanupTelemetry(rerr)
		})

		connect := func(ctx context.Context) (*client.Client, func(), error) {
			fp, err := finalizeEngineParams(ctx, params)
			if err != nil {
				return nil, nil, err
			}
			sess, err := client.Connect(ctx, fp)
			if err != nil {
				return nil, nil, err
			}
			Frontend.SetClient(sess.Dagger())
			// The command outlives its sessions, so its span ends after the
			// last one; Cloud gets that end directly (see localTelemetry).
			detach := cliLocalTelemetry.attach(ctx, sess)
			return sess, func() {
				detach()
				_ = sess.Close()
			}, nil
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
	if params.UserConfigPath == "" {
		// The shared user-level config file (~/.config/dagger/config.toml, or
		// $DAGGER_CONFIG). The engine reads its [workspaces.*] section for
		// user-level workspace overrides.
		params.UserConfigPath = llmconfig.ConfigFile
	}
	if params.LLMConfig == nil {
		// Assembled from the process's cwd, which --workdir has already
		// changed, so ./.env is read from where the command runs.
		cfg, err := assembleLLMConfig()
		if err != nil {
			return err
		}
		params.LLMConfig = cfg
	}
	return nil
}

// skipSharedTelemetryExporters, when set, makes engineTelemetryConfig leave out
// the process-wide OTLP exporter singletons (Dagger Cloud + the OTEL_* "Detect"
// exporters). It is toggled by preparation sessions;
// see engineTelemetryConfig for why.
var skipSharedTelemetryExporters bool

// engineTelemetryConfig builds the telemetry pipeline for one engine session.
//
// Internal plumbing sessions (see skipSharedTelemetryExporters) opt out of the
// process-wide OTLP exporter singletons — the Dagger Cloud exporters and the
// OTEL_* "Detect" exporters. Those singletons are sync.Once-cached and get shut
// down by telemetry.Close(); a pre-command session that wired them up and then
// tore them down would leave them dead for the real command that runs next in
// the same process, surfacing "HTTP exporter is shutdown" / "context canceled"
// telemetry warnings (e.g. the preflight session for a dynamic SDK command).
// Preparation sessions use only their own frontend exporters.
func engineTelemetryConfig(ctx context.Context) (telemetry.Config, *localTelemetry) {
	return engineTelemetryConfigWithCloud(ctx, enginetel.ConfiguredCloudExporters)
}

type configuredCloudExportersFunc func(context.Context) (sdktrace.SpanExporter, sdklog.Exporter, sdkmetric.Exporter, bool)

// cliLocalTelemetry is the current pipeline's localTelemetry: where
// initEngineTelemetry put it in the global telemetry slices, so that engine
// telemetry is forwarded without it, and what attaches to engine sessions.
// nil for preparation sessions.
var cliLocalTelemetry *localTelemetry

// withoutIndex returns a copy of all without its i-th element.
func withoutIndex[T any](all []T, i int) []T {
	out := make([]T, 0, len(all))
	for j, v := range all {
		if j != i {
			out = append(out, v)
		}
	}
	return out
}

// engineTelemetryConfigWithCloud returns the pipeline config, and the
// localTelemetry it holds, with its indexes in the pipeline telemetry.Init
// builds: the span processor's index in the telemetry.SpanProcessors Init
// sets, and the log and metric exporters' indexes among the ones Init
// appends, in Config order, to telemetry.LogProcessors and
// telemetry.MetricExporters.
func engineTelemetryConfigWithCloud(ctx context.Context, configuredCloudExporters configuredCloudExportersFunc) (telemetry.Config, *localTelemetry) {
	cfg := telemetry.Config{
		Detect:   !skipSharedTelemetryExporters,
		Resource: Resource(ctx),
	}
	if !frontendTelemetryDisabled(ctx) {
		cfg.LiveTraceExporters = append(cfg.LiveTraceExporters, Frontend.SpanExporter())
		cfg.LiveMetricExporters = append(cfg.LiveMetricExporters, Frontend.MetricExporter())
	}
	if skipSharedTelemetryExporters {
		return cfg, nil
	}
	var local *localTelemetry
	if spans, logs, metrics, ok := configuredCloudExporters(ctx); ok {
		local = newLocalTelemetry(spans, logs, metrics)
	} else {
		local = newLocalTelemetry(nil, nil, nil)
	}
	local.spans = len(cfg.SpanProcessors)
	local.logs = len(cfg.LiveLogExporters)
	local.metrics = len(cfg.LiveMetricExporters)
	// A LARGE-queue live processor instead of the default 2048-slot BSP
	// telemetry.Init would wrap a live exporter with, so the hop to the
	// engine and Cloud does not silently drop spans on a big burst. (Init
	// prepends SpanProcessors to the pipeline, same as a LiveTraceExporter.)
	cfg.SpanProcessors = append(cfg.SpanProcessors, enginetel.NewLargeQueueLiveSpanProcessor(local.spanSink))
	cfg.LiveLogExporters = append(cfg.LiveLogExporters, local.logSink)
	cfg.LiveMetricExporters = append(cfg.LiveMetricExporters, local.metricSink)
	return cfg, local
}

func initEngineTelemetry(ctx context.Context) (_ context.Context, endCommand func(error), closeTelemetry func(error) error) {
	cfg, local := engineTelemetryConfig(ctx)
	// Init replaces telemetry.SpanProcessors but appends to LogProcessors and
	// MetricExporters, which may hold an earlier Init's entries.
	if local != nil {
		local.logs += len(telemetry.LogProcessors)
		local.metrics += len(telemetry.MetricExporters)
	}
	firstLogProcessor := len(telemetry.LogProcessors)
	ctx = telemetry.Init(ctx, cfg)
	if local != nil {
		spans := telemetry.SpanProcessors[local.spans]
		logs := telemetry.LogProcessors[local.logs]
		metrics := telemetry.MeterProvider(ctx)
		local.flush = func(ctx context.Context) error {
			err := errors.Join(spans.ForceFlush(ctx), logs.ForceFlush(ctx))
			if metrics != nil {
				err = errors.Join(err, metrics.ForceFlush(ctx))
			}
			return err
		}
	}
	cliLocalTelemetry = local

	// Install the frontend separately: telemetry.Init wraps LiveLogExporters
	// in dropping batch processors. Command stdio and forwarded engine logs
	// must instead wait for local queue space. Remote exporters stay batched.
	var frontendLogs *enginetel.BlockingLogProcessor
	var diagnostics *sdklog.BatchProcessor
	var diagnosticProvider *sdklog.LoggerProvider
	if !frontendTelemetryDisabled(ctx) {
		frontendLogs = enginetel.NewBlockingLogProcessor(
			Frontend.LogExporter(), enginetel.LogQueueSize,
			enginetel.LogExportMaxBatchSize, telemetry.NearlyImmediate,
		)
		logOpts := []sdklog.LoggerProviderOption{sdklog.WithResource(cfg.Resource)}
		for _, processor := range telemetry.LogProcessors[firstLogProcessor:] {
			logOpts = append(logOpts, sdklog.WithProcessor(processor))
		}
		// Diagnostics can be emitted by the frontend consumer, an error
		// handler, or cleanup. Keep default slog best-effort and independent
		// of frontend I/O; it must never wait on the command output queue.
		diagnostics = sdklog.NewBatchProcessor(cliDiagnosticExporter{out: os.Stderr},
			sdklog.WithMaxQueueSize(enginetel.LogQueueSize),
			sdklog.WithExportMaxBatchSize(enginetel.LogExportMaxBatchSize),
			sdklog.WithExportInterval(telemetry.NearlyImmediate),
		)
		diagnosticProvider = sdklog.NewLoggerProvider(append(slices.Clone(logOpts), sdklog.WithProcessor(diagnostics))...)
		ctx = telemetry.WithLoggerProvider(ctx, sdklog.NewLoggerProvider(append(logOpts, sdklog.WithProcessor(frontendLogs))...))
		// Engine log forwarding uses the same local processor. Append it so
		// the local telemetry indexes computed above remain valid.
		telemetry.LogProcessors = append(telemetry.LogProcessors, frontendLogs)
	}
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

	// Keep span attribution and remote export for diagnostics, but route the
	// local copy through the best-effort provider instead of command stdio.
	slogCtx := ctx
	if diagnosticProvider != nil {
		slogCtx = telemetry.WithLoggerProvider(ctx, diagnosticProvider)
	}
	slog.SetDefault(slog.SpanLogger(slogCtx, InstrumentationLibrary))

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

	// endCommand closes the command's output and ends its span. It runs
	// before the engine session closes, so the engine stores them whole, and
	// again, as a no-op, when telemetry closes. The frontend's output queue
	// is drained only when telemetry closes: engine logs still arrive through
	// it until the session has closed.
	var endOnce sync.Once
	var eofErr error
	endCommand = func(rerr error) {
		endOnce.Do(func() {
			// EOF emission can wait for queue space. Give it the same
			// bounded budget as the local drain.
			eofCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cliOutputDrainTimeout)
			eofErr = emitCLIOutputEOF(eofCtx)
			cancel()
			if eofErr != nil {
				rerr = errors.Join(rerr, &idtui.OutputDrainError{Err: eofErr})
			}
			telemetry.EndWithCause(span, &rerr)
		})
	}
	return ctx, endCommand, func(rerr error) error {
		endCommand(rerr)
		rootCmd.SetOut(oldOut)
		rootCmd.SetErr(oldErr)
		err := eofErr
		if frontendLogs != nil {
			flushCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cliOutputDrainTimeout)
			err = errors.Join(err, drainCLIOutputQueue(flushCtx, frontendLogs))
			cancel()
		}
		var drainErr error
		if err != nil {
			drainErr = &idtui.OutputDrainError{Err: err}
			slog.Error("command output was not fully delivered", "error", drainErr)
		}
		// telemetry.Close owns the remote providers created by Init.
		telemetry.Close()
		if diagnostics != nil {
			// Remote processors above remain owned by telemetry.Close. Only
			// close the local diagnostic processor here, with a short budget.
			diagnosticCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
			_ = diagnostics.Shutdown(diagnosticCtx)
			cancel()
		}
		return drainErr
	}
}

// cliOutputDrainTimeout bounds each step of delivering the command's output
// at exit: emitting its EOF records, and draining the frontend's queue.
const cliOutputDrainTimeout = 30 * time.Second

func drainCLIOutput(ctx context.Context, processor *enginetel.BlockingLogProcessor) error {
	err := emitCLIOutputEOF(ctx)
	if processor == nil {
		return err
	}
	return errors.Join(err, drainCLIOutputQueue(ctx, processor))
}

// emitCLIOutputEOF emits the command's stdio EOF records.
func emitCLIOutputEOF(ctx context.Context) error {
	// SpanStdio wrappers own no resources: Close only emits EOF records.
	// Create the EOF writers with the drain context instead of closing the
	// command writers, which retain the command's unbounded emission context.
	err := telemetry.SpanStdio(ctx, InstrumentationLibrary).Close()
	// OTel Writer.Close does not propagate OnEmit errors. Check the context
	// explicitly so a failed EOF emission cannot be mistaken for success.
	return errors.Join(err, context.Cause(ctx))
}

// drainCLIOutputQueue shuts down the frontend's output queue, delivering
// what it holds.
func drainCLIOutputQueue(ctx context.Context, processor *enginetel.BlockingLogProcessor) error {
	shutdownErr := processor.Shutdown(ctx)
	return errors.Join(context.Cause(ctx), shutdownErr)
}
