package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"golang.org/x/oauth2"

	"github.com/dagger/dagger/engine"
	engineclient "github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/slog"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	cloudauth "github.com/dagger/dagger/internal/cloud/auth"
	telemetry "github.com/dagger/otel-go"
)

// sessionTelemetryFlushTimeout bounds each wait on Cloud, and all of them
// together while the main client's shutdown request runs. The client gives
// the engine 10s to shut down before it fails the command, and one export to
// a hanging Cloud endpoint can take 10s by itself, so at 5s a Cloud outage
// costs telemetry, never the build.
const sessionTelemetryFlushTimeout = 5 * time.Second

// sessionPublishesToCloud reports whether the session's main client asked the
// engine to publish the session's telemetry to Dagger Cloud, with the
// credential the client provides.
func sessionPublishesToCloud(md *engine.ClientMetadata) bool {
	return md != nil &&
		md.CloudTelemetryPublisher == engine.CloudTelemetryPublisherEngine &&
		md.CloudAuth != nil && md.CloudAuth.Token != nil
}

// initializeSessionCloudTelemetry builds the session's Dagger Cloud exporters
// from its main client's credential and Cloud URL, when the client asked the
// engine to publish. The session owns them.
func (srv *Server) initializeSessionCloudTelemetry(sess *daggerSession, md *engine.ClientMetadata) {
	if !sessionPublishesToCloud(md) {
		return
	}
	tokenRefresh := enginetel.BoundedTokenRefresh(func(ctx context.Context) (*oauth2.Token, error) {
		return srv.refreshSessionCloudToken(ctx, sess, md.CredentialsPath)
	})
	spans, logs, metrics, err := enginetel.NewCloudExporters(context.Background(), md.CloudAuth, tokenRefresh, md.CloudURL)
	if err != nil {
		slog.Warn("session telemetry not published to Cloud: cannot configure the Cloud exporters", "session", sess.sessionID, "error", err)
		return
	}
	sess.cloudBound = cloudFlushBound{sessionID: sess.sessionID, timeout: sessionTelemetryFlushTimeout, budget: &cloudShutdownBudget{}}
	if srv.sessionCloudFlushTimeout > 0 {
		sess.cloudBound.timeout = srv.sessionCloudFlushTimeout
	}
	sess.cloudSpans, sess.cloudLogs = spans, logs
	sess.cloudMetrics = boundedCloudMetricExporter{Exporter: metrics, bound: sess.cloudBound}
}

// publishesToCloud reports whether the session publishes its telemetry to
// Cloud. It is decided once, when the session's telemetry initializes and
// before any client can open a telemetry stream, so every stream of the
// session is confirmed alike and a client's forwarding never flips midway.
func (sess *daggerSession) publishesToCloud() bool {
	return sess.cloudSpanProcessor != nil && sess.cloudLogProcessor != nil && sess.cloudMetrics != nil
}

// refreshSessionCloudToken refreshes the main client's expired OAuth token
// from the credentials file on the client's host, and writes the refreshed
// token back there, since refreshing invalidates the old one.
func (srv *Server) refreshSessionCloudToken(ctx context.Context, sess *daggerSession, credentialsPath string) (*oauth2.Token, error) {
	if credentialsPath == "" {
		return nil, fmt.Errorf("refresh cloud token: no credentials path")
	}
	record, err := srv.clientRecordFromIDs(sess.sessionID, sess.mainClientCallerID)
	if err != nil {
		return nil, fmt.Errorf("refresh cloud token: main client: %w", err)
	}
	md, err := sess.clientMetadataSnapshot(record)
	if err != nil {
		return nil, fmt.Errorf("refresh cloud token: main client metadata: %w", err)
	}
	if sess.engineUtilClient == nil {
		return nil, fmt.Errorf("refresh cloud token: session gateway not initialized")
	}
	ctx = engine.ContextWithClientMetadata(ctx, md)
	data, err := sess.engineUtilClient.ReadCallerHostFile(ctx, credentialsPath)
	if err != nil {
		return nil, fmt.Errorf("refresh cloud token: read credentials: %w", err)
	}
	var token oauth2.Token
	if err := json.Unmarshal(data, &token); err != nil {
		return nil, fmt.Errorf("refresh cloud token: parse credentials: %w", err)
	}
	source, err := cloudauth.TokenSource(ctx, &token)
	if err != nil {
		return nil, fmt.Errorf("refresh cloud token: %w", err)
	}
	refreshed, err := source.Token()
	if err != nil {
		return nil, fmt.Errorf("refresh cloud token: %w", err)
	}
	if refreshed.AccessToken != token.AccessToken {
		encoded, err := json.Marshal(refreshed)
		if err != nil {
			return nil, fmt.Errorf("refresh cloud token: encode: %w", err)
		}
		if err := sess.engineUtilClient.IOReaderExport(ctx, bytes.NewReader(encoded), credentialsPath, 0o600); err != nil {
			return nil, fmt.Errorf("refresh cloud token: write credentials: %w", err)
		}
		slog.Info("refreshed cloud credentials", "session", sess.sessionID, "credentialsPath", credentialsPath)
	}
	return refreshed, nil
}

// cloudShutdownBudget is the one deadline for Cloud while the main client's
// shutdown request runs, set when it starts and cleared when it returns, so
// the waits on Cloud during the request add up to at most one bound.
type cloudShutdownBudget struct {
	mu       sync.Mutex
	deadline time.Time
}

func (b *cloudShutdownBudget) start(timeout time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.deadline.IsZero() {
		b.deadline = time.Now().Add(timeout)
	}
}

func (b *cloudShutdownBudget) end() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.deadline = time.Time{}
}

func (b *cloudShutdownBudget) current() (time.Time, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.deadline, !b.deadline.IsZero()
}

// cloudFlushBound bounds every wait on the session's Cloud exporters: at the
// earliest of its own timeout, the caller's deadline and the shutdown budget.
// It logs errors instead of returning them, so a Cloud outage costs telemetry
// and never the command, and never holds the client's shutdown.
type cloudFlushBound struct {
	sessionID string
	timeout   time.Duration
	budget    *cloudShutdownBudget
}

// bounded runs op within the bound. With nothing left of it, op is skipped;
// a release (a shutdown) still runs, with an expired context, so the
// exporter frees its resources without waiting on Cloud.
func (b cloudFlushBound) bounded(ctx context.Context, what string, release bool, op func(context.Context) error) {
	deadline := time.Now().Add(b.timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if b.budget != nil {
		if d, ok := b.budget.current(); ok && d.Before(deadline) {
			deadline = d
		}
	}
	if !time.Now().Before(deadline) && !release {
		slog.Warn("session telemetry not fully published to Cloud: no time left", "session", b.sessionID, "op", what)
		return
	}
	ctx, cancel := context.WithDeadline(context.WithoutCancel(ctx), deadline)
	defer cancel()
	if err := op(ctx); err != nil {
		slog.Warn("session telemetry not fully published to Cloud", "session", b.sessionID, "op", what, "error", err)
	}
}

// startCloudShutdownBudget starts the one Cloud deadline of the main client's
// shutdown request; the returned func clears it when the request returns.
func (sess *daggerSession) startCloudShutdownBudget() func() {
	if sess.cloudBound.budget == nil {
		return func() {}
	}
	sess.cloudBound.budget.start(sess.cloudBound.timeout)
	return sess.cloudBound.budget.end
}

// boundedCloudSpanProcessor and boundedCloudLogProcessor carry the session's
// telemetry to Cloud. Their ForceFlush returns at once: the processors export
// on their own, and the session's provider flushes (any client's shutdown,
// the telemetry APIs) must not wait on Cloud. The main client's shutdown
// flushes them once, through flushSessionCloudTelemetry, within the budget.
type boundedCloudSpanProcessor struct {
	sdktrace.SpanProcessor
	bound cloudFlushBound
}

func (boundedCloudSpanProcessor) ForceFlush(context.Context) error { return nil }

func (p boundedCloudSpanProcessor) flush(ctx context.Context) {
	p.bound.bounded(ctx, "flush spans", false, p.SpanProcessor.ForceFlush)
}

func (p boundedCloudSpanProcessor) Shutdown(ctx context.Context) error {
	p.bound.bounded(ctx, "shutdown spans", true, p.SpanProcessor.Shutdown)
	return nil
}

type boundedCloudLogProcessor struct {
	sdklog.Processor
	bound cloudFlushBound
}

func (boundedCloudLogProcessor) ForceFlush(context.Context) error { return nil }

func (p boundedCloudLogProcessor) flush(ctx context.Context) {
	p.bound.bounded(ctx, "flush logs", false, p.Processor.ForceFlush)
}

func (p boundedCloudLogProcessor) Shutdown(ctx context.Context) error {
	p.bound.bounded(ctx, "shutdown logs", true, p.Processor.Shutdown)
	return nil
}

// boundedCloudMetricExporter bounds the session's Cloud metric exports, which
// every client's periodic reader drives, and logs their errors instead of
// returning them into a client's metric flush.
type boundedCloudMetricExporter struct {
	sdkmetric.Exporter
	bound cloudFlushBound
}

func (e boundedCloudMetricExporter) Export(ctx context.Context, metrics *metricdata.ResourceMetrics) error {
	e.bound.bounded(ctx, "export metrics", false, func(ctx context.Context) error {
		return e.Exporter.Export(ctx, metrics)
	})
	return nil
}

func (e boundedCloudMetricExporter) ForceFlush(ctx context.Context) error {
	e.bound.bounded(ctx, "flush metrics", false, e.Exporter.ForceFlush)
	return nil
}

func (e boundedCloudMetricExporter) Shutdown(ctx context.Context) error {
	e.bound.bounded(ctx, "shutdown metrics", true, e.Exporter.Shutdown)
	return nil
}

// flushSessionCloudTelemetry flushes the session's Cloud span and log
// processors concurrently, within the bound. The main client's shutdown calls
// it once, before the session's attachables close, because refreshing an
// OAuth token reads the client's credentials file through them.
func (sess *daggerSession) flushSessionCloudTelemetry(ctx context.Context) {
	var wg sync.WaitGroup
	for _, flush := range sess.cloudFlushers {
		wg.Go(func() { flush(ctx) })
	}
	wg.Wait()
}

// scaleOutTelemetryParams routes a scale-out engine's telemetry stream, which
// the parent client receives, into the session's client routing. When this
// session publishes to Cloud, the remote engine is asked to publish its own
// session with the same credential; on streams the remote does not confirm,
// the parent publishes them through this session's Cloud processors instead.
// When this session does not publish, the remote is not asked either, and the
// stream reaches Cloud once, through the client that forwards this session's
// telemetry.
func (sess *daggerSession) scaleOutTelemetryParams(parent *clientRuntime, params *engineclient.Params) {
	params.EngineTrace = parent.spanExporter
	params.EngineLogs = parent.logExporter
	params.EngineMetrics = []sdkmetric.Exporter{parent.metricExporter}
	if !sess.publishesToCloud() {
		return
	}
	md := parent.clientMetadata
	params.EngineCloudTelemetry = true
	params.CloudURL = md.CloudURL
	params.CloudCredentialsPath = md.CredentialsPath
	params.EngineTraceWithoutCloud = params.EngineTrace
	params.EngineLogsWithoutCloud = params.EngineLogs
	params.EngineMetricsWithoutCloud = params.EngineMetrics
	params.EngineTrace = enginetel.MultiSpanExporter{
		parent.spanExporter,
		sessionCloudSpanForwarder{telemetry.SpanForwarder{Processors: []sdktrace.SpanProcessor{sess.cloudSpanProcessor}}},
	}
	params.EngineLogs = enginetel.MultiLogExporter{
		parent.logExporter,
		sessionCloudLogForwarder{telemetry.LogForwarder{Processors: []sdklog.Processor{sess.cloudLogProcessor}}},
	}
	params.EngineMetrics = []sdkmetric.Exporter{
		parent.metricExporter,
		enginetel.SharedMetricExporter{Exporter: sess.cloudMetrics},
	}
}

// Telemetry a process in one of the session's containers posts to the
// engine (an SDK, a nested CLI) reaches the client routing directly, never
// the session's providers, so the session's Cloud processors do not see it.
// The posted* exporters stamp the origin once and send the telemetry both to
// the client routing and, when the session publishes, to Cloud. A scale-out
// engine's returned stream does not come this way: its own containers post to
// the remote engine, which publishes them itself when it confirmed.

func (sess *daggerSession) postedSpanExporter(origin string) sdktrace.SpanExporter {
	var next sdktrace.SpanExporter = sess.spanExporter
	if sess.publishesToCloud() {
		next = enginetel.MultiSpanExporter{
			sess.spanExporter,
			sessionCloudSpanForwarder{telemetry.SpanForwarder{Processors: []sdktrace.SpanProcessor{sess.cloudSpanProcessor}}},
		}
	}
	return originSpanExporter{origin: origin, next: next}
}

func (sess *daggerSession) postedLogExporter(origin string) sdklog.Exporter {
	var next sdklog.Exporter = sess.logExporter
	if sess.publishesToCloud() {
		next = enginetel.MultiLogExporter{
			sess.logExporter,
			sessionCloudLogForwarder{telemetry.LogForwarder{Processors: []sdklog.Processor{sess.cloudLogProcessor}}},
		}
	}
	return originLogExporter{origin: origin, next: next}
}

func (sess *daggerSession) postedMetricExporters(client sdkmetric.Exporter) []sdkmetric.Exporter {
	if !sess.publishesToCloud() {
		return []sdkmetric.Exporter{client}
	}
	return []sdkmetric.Exporter{client, enginetel.SharedMetricExporter{Exporter: sess.cloudMetrics}}
}

// sessionCloudSpanForwarder and sessionCloudLogForwarder feed the session's
// Cloud processors, which the session shuts down itself.
type sessionCloudSpanForwarder struct{ telemetry.SpanForwarder }

func (sessionCloudSpanForwarder) Shutdown(context.Context) error { return nil }

type sessionCloudLogForwarder struct{ telemetry.LogForwarder }

func (sessionCloudLogForwarder) Shutdown(context.Context) error { return nil }
