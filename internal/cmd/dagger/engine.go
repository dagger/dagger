package daggercmd

import (
	"context"
	"fmt"
	"os"
	"slices"

	"github.com/dagger/dagger/dagql/dagui"
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
		ctx, cleanupTelemetry := initEngineTelemetry(ctx)
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

// setEngineTelemetryParams wires where the client forwards the telemetry the
// engine streams to it, from the pipeline initEngineTelemetry set up.
func setEngineTelemetryParams(ctx context.Context, params *client.Params) {
	// A nested CLI's parent already gets the engine's telemetry; see
	// engineForwardTargets.
	parentTelemetry := noTelemetryIndexes
	if nestedEngineSession() {
		parentTelemetry = cliEnvTelemetry
	}
	spanProcessors, logProcessors, metricExporters := engineForwardTargets(
		telemetry.SpanProcessors, telemetry.LogProcessors, telemetry.MetricExporters,
		parentTelemetry)
	params.EngineTrace = telemetry.SpanForwarder{
		Processors: spanProcessors,
	}
	// The engine names the session (Query.setSessionTitle) with a span-name
	// record on our primary span; apply it to the live span too, so the span
	// we export carries the title like the frontend shows it.
	namer := primarySpanNamer{span: trace.SpanFromContext(ctx)}
	params.EngineLogs = telemetry.LogForwarder{
		Processors: append(slices.Clone(logProcessors), namer),
	}
	params.EngineMetrics = metricExporters
	if cloud := cliCloudTelemetry; cloud.configured() {
		// Ask the engine to publish the session's telemetry to Cloud itself,
		// and have what it would send us forwarded everywhere else.
		params.EngineCloudTelemetry = true
		params.CloudURL = os.Getenv("DAGGER_CLOUD_URL")
		params.CloudCredentialsPath = auth.CredentialsFile()
		spanProcessors, logProcessors, metricExporters := engineForwardTargets(
			telemetry.SpanProcessors, telemetry.LogProcessors, telemetry.MetricExporters,
			parentTelemetry, cloud)
		params.EngineTraceWithoutCloud = telemetry.SpanForwarder{
			Processors: spanProcessors,
		}
		params.EngineLogsWithoutCloud = telemetry.LogForwarder{
			Processors: append(slices.Clone(logProcessors), namer),
		}
		params.EngineMetricsWithoutCloud = metricExporters
	}
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

	params.Profile = profileFlag

	params.CloudURLCallback = Frontend.SetCloudURL

	setEngineTelemetryParams(ctx, &params)

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

	ca, err := auth.GetCloudAuth(ctx)
	if err != nil {
		return params, err
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
		if before != nil {
			before(ctx)
		}

		// Telemetry deliberately starts after the untraced setup phase (Cloud
		// login), so credentials established there apply to the whole trace.
		ctx, cleanupTelemetry := initEngineTelemetry(ctx)
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
	if params.UserConfigPath == "" {
		// The shared user-level config file (~/.config/dagger/config.toml, or
		// $DAGGER_CONFIG). The engine reads its [workspaces.*] section for
		// user-level workspace overrides.
		params.UserConfigPath = llmconfig.ConfigFile
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
// OTEL_* exporters. Those singletons are sync.Once-cached and get shut
// down by telemetry.Close(); a pre-command session that wired them up and then
// tore them down would leave them dead for the real command that runs next in
// the same process, surfacing "HTTP exporter is shutdown" / "context canceled"
// telemetry warnings (e.g. the preflight session for a dynamic SDK command).
// Preparation sessions use only their own frontend exporters.
func engineTelemetryConfig(ctx context.Context) (_ telemetry.Config, cloud, env telemetryIndexes) {
	return engineTelemetryConfigWith(ctx, enginetel.ConfiguredCloudExporters, configuredEnvExporters)
}

type configuredCloudExportersFunc func(context.Context) (sdktrace.SpanExporter, sdklog.Exporter, sdkmetric.Exporter, bool)

// telemetryIndexes locates a set of the CLI's exporters (its Dagger Cloud
// exporters, or the ones configured by OTEL_* environment variables) in the
// pipeline telemetry.Init builds: the span processor's index in the
// telemetry.SpanProcessors Init sets, and the log and metric exporters'
// indexes among the ones Init appends, in Config order, to
// telemetry.LogProcessors and telemetry.MetricExporters. Each is -1 when the
// CLI has no such exporter.
type telemetryIndexes struct {
	spans, logs, metrics int
}

func (idx telemetryIndexes) configured() bool {
	return idx.spans >= 0 && idx.logs >= 0 && idx.metrics >= 0
}

var noTelemetryIndexes = telemetryIndexes{spans: -1, logs: -1, metrics: -1}

// cliCloudTelemetry is where initEngineTelemetry found the Cloud exporters in
// the global telemetry slices, so that the engine's telemetry can be
// forwarded without them when the engine publishes it to Cloud itself.
var cliCloudTelemetry = noTelemetryIndexes

// cliEnvTelemetry is where initEngineTelemetry put the exporters configured
// by the OTEL_* environment, so that a nested CLI can forward the engine's
// telemetry without them (see engineForwardTargets).
var cliEnvTelemetry = noTelemetryIndexes

// withoutIndex returns a copy of all without its i-th element.
func withoutIndex[T any](all []T, i int) []T {
	return withoutIndexes(all, i)
}

// withoutIndexes returns all without the elements at the given indexes;
// negative indexes are ignored. With none to drop, all is returned as-is, so
// a nil slice stays nil.
func withoutIndexes[T any](all []T, drop ...int) []T {
	if !slices.ContainsFunc(drop, func(i int) bool { return i >= 0 && i < len(all) }) {
		return all
	}
	out := make([]T, 0, len(all))
	for j, v := range all {
		if !slices.Contains(drop, j) {
			out = append(out, v)
		}
	}
	return out
}

// engineForwardTargets selects where the CLI forwards the telemetry it
// receives from the engine: every exporter the CLI has, except those in
// exclude.
//
// A nested CLI (one connected through DAGGER_SESSION_PORT, e.g. in a
// container with nesting enabled, or under `dagger run`) excludes the
// exporters it detected from its OTEL_* environment: those point back at its
// parent -- the engine's per-exec telemetry proxy, or `dagger run`'s -- which
// already receives everything the engine sends this client, since the engine
// routes a nested client's telemetry to all of its ancestors itself.
// Forwarding it again would deliver it twice. Spans tolerate that (a newer
// snapshot replaces the older), but every log record would be stored twice:
// output shown twice in the parent's UI, and a sub-agent's prompt duplicated
// in the conversation report an LLM tool result embeds.
func engineForwardTargets(
	spans []sdktrace.SpanProcessor,
	logs []sdklog.Processor,
	metrics []sdkmetric.Exporter,
	exclude ...telemetryIndexes,
) ([]sdktrace.SpanProcessor, []sdklog.Processor, []sdkmetric.Exporter) {
	var dropSpans, dropLogs, dropMetrics []int
	for _, idx := range exclude {
		dropSpans = append(dropSpans, idx.spans)
		dropLogs = append(dropLogs, idx.logs)
		dropMetrics = append(dropMetrics, idx.metrics)
	}
	return withoutIndexes(spans, dropSpans...),
		withoutIndexes(logs, dropLogs...),
		withoutIndexes(metrics, dropMetrics...)
}

// nestedEngineSession reports whether this CLI connects to its parent's
// session through DAGGER_SESSION_PORT rather than starting its own; see
// engine/client's Connect.
func nestedEngineSession() bool {
	_, nested := os.LookupEnv("DAGGER_SESSION_PORT")
	return nested
}

// configuredEnvExportersFunc returns the exporters the OTEL_* environment
// configures, each nil when it configures none.
type configuredEnvExportersFunc func(context.Context) (sdktrace.SpanExporter, sdklog.Exporter, sdkmetric.Exporter)

// configuredEnvExporters is the detection telemetry.Init would do with
// Config.Detect, done by engineTelemetryConfig instead so that it knows where
// the detected exporters end up.
func configuredEnvExporters(ctx context.Context) (sdktrace.SpanExporter, sdklog.Exporter, sdkmetric.Exporter) {
	var spans sdktrace.SpanExporter
	var logs sdklog.Exporter
	var metrics sdkmetric.Exporter
	if exp, ok := telemetry.ConfiguredSpanExporter(ctx); ok {
		spans = exp
	}
	if exp, ok := telemetry.ConfiguredLogExporter(ctx); ok {
		logs = exp
	}
	if exp, ok := telemetry.ConfiguredMetricExporter(ctx); ok {
		metrics = exp
	}
	return spans, logs, metrics
}

func engineTelemetryConfigWith(
	ctx context.Context,
	configuredCloudExporters configuredCloudExportersFunc,
	configuredEnvExporters configuredEnvExportersFunc,
) (_ telemetry.Config, cloud, env telemetryIndexes) {
	cfg := telemetry.Config{
		// The OTEL_* exporters are added below instead, to locate them.
		Detect:   false,
		Resource: Resource(ctx),
	}
	cloud, env = noTelemetryIndexes, noTelemetryIndexes
	if !frontendTelemetryDisabled(ctx) {
		cfg.LiveTraceExporters = append(cfg.LiveTraceExporters, Frontend.SpanExporter())
		cfg.LiveLogExporters = append(cfg.LiveLogExporters, Frontend.LogExporter())
		cfg.LiveMetricExporters = append(cfg.LiveMetricExporters, Frontend.MetricExporter())
	}
	if skipSharedTelemetryExporters {
		return cfg, cloud, env
	}
	if spans, logs, metrics, ok := configuredCloudExporters(ctx); ok {
		// Wrap the Cloud span exporter in a LARGE-queue live processor instead of
		// letting telemetry.Init wrap it with the default 2048-slot BSP, so the
		// CLI→Cloud hop does not silently drop spans on a big burst — a cold engine
		// build is ~15k spans, live-double-emitted ≈ 30k records. The wcprof
		// completeness carrier rides at the tail and was the first thing to drop;
		// this keeps the exported trace complete. (SpanProcessors are prepended to
		// the pipeline by telemetry.Init, same as a LiveTraceExporter would be.)
		cloud = telemetryIndexes{
			spans:   len(cfg.SpanProcessors),
			logs:    len(cfg.LiveLogExporters),
			metrics: len(cfg.LiveMetricExporters),
		}
		cfg.SpanProcessors = append(cfg.SpanProcessors, enginetel.NewLargeQueueLiveSpanProcessor(spans))
		cfg.LiveLogExporters = append(cfg.LiveLogExporters, logs)
		cfg.LiveMetricExporters = append(cfg.LiveMetricExporters, metrics)
	}

	// Add the OTEL_* exporters the way Config.Detect would.
	spans, logs, metrics := configuredEnvExporters(ctx)
	liveEnvSpans := -1
	if spans != nil {
		if telemetry.LiveTracesEnabled {
			liveEnvSpans = len(cfg.LiveTraceExporters)
			cfg.LiveTraceExporters = append(cfg.LiveTraceExporters, spans)
		} else {
			// Filter out unfinished spans to avoid confusing external systems:
			// the engine's are forwarded here too.
			env.spans = len(cfg.BatchedTraceExporters)
			cfg.BatchedTraceExporters = append(cfg.BatchedTraceExporters,
				telemetry.FilterLiveSpansExporter{SpanExporter: spans})
		}
	}
	if logs != nil {
		env.logs = len(cfg.LiveLogExporters)
		cfg.LiveLogExporters = append(cfg.LiveLogExporters, logs)
	}
	if metrics != nil {
		env.metrics = len(cfg.LiveMetricExporters)
		cfg.LiveMetricExporters = append(cfg.LiveMetricExporters, metrics)
	}
	// telemetry.Init sets SpanProcessors to cfg.SpanProcessors, then a
	// processor per live exporter, then one per batched exporter.
	switch {
	case liveEnvSpans >= 0:
		env.spans = len(cfg.SpanProcessors) + liveEnvSpans
	case env.spans >= 0:
		env.spans += len(cfg.SpanProcessors) + len(cfg.LiveTraceExporters)
	}
	return cfg, cloud, env
}

// initTelemetryPipeline runs telemetry.Init with cfg and records where it put
// the Cloud and OTEL_* exporters (cloud and env, as engineTelemetryConfigWith
// returns them).
func initTelemetryPipeline(ctx context.Context, cfg telemetry.Config, cloud, env telemetryIndexes) context.Context {
	cliCloudTelemetry, cliEnvTelemetry = pipelineIndexes(cloud), pipelineIndexes(env)
	return telemetry.Init(ctx, cfg)
}

// pipelineIndexes turns indexes into a Config's exporters into indexes into
// the global slices the next telemetry.Init builds from it. Init replaces
// telemetry.SpanProcessors but appends to LogProcessors and MetricExporters,
// which may hold an earlier Init's entries.
func pipelineIndexes(idx telemetryIndexes) telemetryIndexes {
	if idx.logs >= 0 {
		idx.logs += len(telemetry.LogProcessors)
	}
	if idx.metrics >= 0 {
		idx.metrics += len(telemetry.MetricExporters)
	}
	return idx
}

func initEngineTelemetry(ctx context.Context) (context.Context, func(error)) {
	cfg, cloud, env := engineTelemetryConfig(ctx)
	ctx = initTelemetryPipeline(ctx, cfg, cloud, env)
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
