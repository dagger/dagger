package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

// sessionTelemetryFlushTimeout bounds the main client's shutdown-time
// telemetry flush. The client gives the engine 10s to shut down before it
// fails the command, and one export to a hanging Cloud endpoint can take 10s
// by itself, so at 5s a Cloud outage costs telemetry, never the build.
const sessionTelemetryFlushTimeout = 5 * time.Second

// cloudTokenRefreshTimeout bounds one refresh of the session's OAuth token.
// A refresh runs on an uncancellable context, because exports run on
// background goroutines long after the request that created the session.
const cloudTokenRefreshTimeout = 5 * time.Second

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
	tokenRefresh := func(context.Context) (*oauth2.Token, error) {
		ctx, cancel := context.WithTimeout(context.Background(), cloudTokenRefreshTimeout)
		defer cancel()
		return srv.refreshSessionCloudToken(ctx, sess, md.CredentialsPath)
	}
	spans, logs, metrics, err := enginetel.NewCloudExporters(context.Background(), md.CloudAuth, tokenRefresh, md.CloudURL)
	if err != nil {
		slog.Warn("session telemetry not published to Cloud: cannot configure the Cloud exporters", "session", sess.sessionID, "error", err)
		return
	}
	sess.cloudBound = cloudFlushBound{sessionID: sess.sessionID, timeout: sessionTelemetryFlushTimeout}
	if srv.sessionCloudFlushTimeout > 0 {
		sess.cloudBound.timeout = srv.sessionCloudFlushTimeout
	}
	sess.cloudSpans, sess.cloudLogs = spans, logs
	sess.cloudMetrics = boundedCloudMetricExporter{Exporter: metrics, bound: sess.cloudBound}
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

// cloudFlushBound is a session's Cloud telemetry processor behind a bound: a
// flush or shutdown gives up after the timeout and logs its error instead of
// returning it, so a Cloud outage costs telemetry and never the command, and
// never holds the client's shutdown.
type cloudFlushBound struct {
	sessionID string
	timeout   time.Duration
}

func (b cloudFlushBound) bounded(ctx context.Context, what string, op func(context.Context) error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), b.timeout)
	defer cancel()
	if err := op(ctx); err != nil {
		slog.Warn("session telemetry not fully published to Cloud", "session", b.sessionID, "op", what, "error", err)
	}
}

type boundedCloudSpanProcessor struct {
	sdktrace.SpanProcessor
	bound cloudFlushBound
}

func (p boundedCloudSpanProcessor) ForceFlush(ctx context.Context) error {
	p.bound.bounded(ctx, "flush spans", p.SpanProcessor.ForceFlush)
	return nil
}

func (p boundedCloudSpanProcessor) Shutdown(ctx context.Context) error {
	p.bound.bounded(ctx, "shutdown spans", p.SpanProcessor.Shutdown)
	return nil
}

type boundedCloudLogProcessor struct {
	sdklog.Processor
	bound cloudFlushBound
}

func (p boundedCloudLogProcessor) ForceFlush(ctx context.Context) error {
	p.bound.bounded(ctx, "flush logs", p.Processor.ForceFlush)
	return nil
}

func (p boundedCloudLogProcessor) Shutdown(ctx context.Context) error {
	p.bound.bounded(ctx, "shutdown logs", p.Processor.Shutdown)
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
	e.bound.bounded(ctx, "export metrics", func(ctx context.Context) error {
		return e.Exporter.Export(ctx, metrics)
	})
	return nil
}

func (e boundedCloudMetricExporter) ForceFlush(ctx context.Context) error {
	e.bound.bounded(ctx, "flush metrics", e.Exporter.ForceFlush)
	return nil
}

func (e boundedCloudMetricExporter) Shutdown(ctx context.Context) error {
	e.bound.bounded(ctx, "shutdown metrics", e.Exporter.Shutdown)
	return nil
}

// flushSessionCloudTelemetry flushes the session's Cloud processors, bounded.
// The main client's shutdown calls it before the session's attachables close,
// because refreshing an OAuth token reads the client's credentials file
// through them.
func (sess *daggerSession) flushSessionCloudTelemetry(ctx context.Context) {
	for _, flush := range sess.cloudFlushers {
		_ = flush(ctx)
	}
}

// scaleOutTelemetryParams routes a scale-out engine's telemetry stream, which
// the parent client receives, into the session's client routing. When this
// session publishes to Cloud, the remote engine is asked to publish its own
// session with the same credential; if it does not, the parent publishes the
// stream through this session's Cloud processors instead. When this session
// does not publish, the remote is not asked either, and the stream reaches
// Cloud once, through the client that forwards this session's telemetry.
func (sess *daggerSession) scaleOutTelemetryParams(parent *clientRuntime, params *engineclient.Params) {
	params.EngineTrace = parent.spanExporter
	params.EngineLogs = parent.logExporter
	params.EngineMetrics = []sdkmetric.Exporter{parent.metricExporter}
	if sess.cloudSpanProcessor == nil || sess.cloudLogProcessor == nil || sess.cloudMetrics == nil {
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

// sessionCloudSpanForwarder and sessionCloudLogForwarder feed the session's
// Cloud processors, which the session shuts down itself.
type sessionCloudSpanForwarder struct{ telemetry.SpanForwarder }

func (sessionCloudSpanForwarder) Shutdown(context.Context) error { return nil }

type sessionCloudLogForwarder struct{ telemetry.LogForwarder }

func (sessionCloudLogForwarder) Shutdown(context.Context) error { return nil }
