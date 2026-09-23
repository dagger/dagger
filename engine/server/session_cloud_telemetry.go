package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	otlpmetricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
	"golang.org/x/oauth2"
	"google.golang.org/protobuf/proto"

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
	sess.cloudRefresh = &cloudRefreshGate{}
	if srv.sessionCloudFlushTimeout > 0 {
		sess.cloudBound.timeout = srv.sessionCloudFlushTimeout
	}
	sess.cloudSpans, sess.cloudLogs = spans, logs
	sess.cloudMetrics = newCloudMetricQueue(metrics, sess.cloudBound)
}

// publishesToCloud reports whether the session publishes its telemetry to
// Cloud. It is decided once, when the session's telemetry initializes and
// before any client can open a telemetry stream, so every stream of the
// session is confirmed alike and a client's forwarding never flips midway.
func (sess *daggerSession) publishesToCloud() bool {
	return sess.cloudSpanProcessor != nil && sess.cloudLogProcessor != nil && sess.cloudMetrics != nil
}

var errCloudRefreshSessionClosing = errors.New("refresh cloud token: the main client is shutting down")

// cloudRefreshGate admits a refresh's file operations until the main
// client's shutdown has flushed Cloud. A refresh reads and writes the
// client's credentials file through its attachables; when they close under
// one, the gateway's lookup waits out its own 10s whatever the refresh's
// deadline. So after the Cloud flush, and before the attachables close,
// shutdown stops new file operations and waits, within the Cloud budget,
// for one in flight. A nil gate admits everything.
type cloudRefreshGate struct {
	mu       sync.Mutex
	closed   bool
	inflight sync.WaitGroup
}

func (g *cloudRefreshGate) enter() bool {
	if g == nil {
		return true
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return false
	}
	g.inflight.Add(1)
	return true
}

func (g *cloudRefreshGate) exit() {
	if g != nil {
		g.inflight.Done()
	}
}

// close stops new refreshes and waits for those in flight, until ctx ends.
func (g *cloudRefreshGate) close(ctx context.Context) error {
	g.stop()
	return g.wait(ctx)
}

func (g *cloudRefreshGate) stop() {
	g.mu.Lock()
	g.closed = true
	g.mu.Unlock()
}

func (g *cloudRefreshGate) wait(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		g.inflight.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// stopCloudTokenRefresh ends the refreshes' file operations for the rest of
// the session: the main client's shutdown calls it after the Cloud flush and
// before the attachables close. A refresh already exchanging its token keeps
// the new token without writing it back; exports with an expired token fail
// at once, costing telemetry at the very end of a session, never its
// shutdown.
func (sess *daggerSession) stopCloudTokenRefresh(ctx context.Context) {
	if sess.cloudRefresh == nil {
		return
	}
	// Stopping is unconditional. The wait for a file operation in flight
	// has its own bound, independent of the Cloud budget: a file operation
	// takes milliseconds, and the attachables must not close under one
	// even when a hanging Cloud has spent the budget.
	sess.cloudRefresh.stop()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cloudRefreshFileOpWait)
	defer cancel()
	if err := sess.cloudRefresh.wait(ctx); err != nil {
		slog.Warn("credentials file operation still in flight as the attachables close", "session", sess.sessionID, "error", err)
	}
}

// cloudRefreshFileOpWait bounds how long the main client's shutdown waits for
// a credentials file operation in flight before closing the attachables.
const cloudRefreshFileOpWait = time.Second

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
	// attachable fails at once once the client's attachables close or are
	// gone: waiting for them ignores every deadline (see cloudRefreshGate).
	attachable := func() error {
		if sess.closingCtx != nil && sess.closingCtx.Err() != nil {
			return errCloudRefreshSessionClosing
		}
		if sess.attachables != nil {
			if _, ok := sess.attachables.Lookup(record.clientID); !ok {
				return errCloudRefreshSessionClosing
			}
		}
		return nil
	}
	return refreshCredentialsFile(ctx, sess.sessionID, sess.cloudRefresh, cloudCredentialsFile{
		read: func(ctx context.Context) ([]byte, error) {
			if err := attachable(); err != nil {
				return nil, err
			}
			return sess.engineUtilClient.ReadCallerHostFile(ctx, credentialsPath)
		},
		// Replaced atomically: the CLI and other sessions' engines read and
		// write the same file.
		write: func(ctx context.Context, data []byte) error {
			if err := attachable(); err != nil {
				return err
			}
			return sess.engineUtilClient.ReplaceCallerHostFile(ctx, data, credentialsPath, 0o600)
		},
		refresh: func(ctx context.Context, token *oauth2.Token) (*oauth2.Token, error) {
			source, err := cloudauth.TokenSource(ctx, token)
			if err != nil {
				return nil, err
			}
			return source.Token()
		},
	})
}

// cloudCredentialsFile reads and replaces the main client's credentials file
// through its attachables, and exchanges its refresh token with Cloud.
type cloudCredentialsFile struct {
	read    func(context.Context) ([]byte, error)
	write   func(context.Context, []byte) error
	refresh func(context.Context, *oauth2.Token) (*oauth2.Token, error)
}

// refreshCredentialsFile refreshes the token in the credentials file and
// writes the new one back, since refreshing may invalidate the old. The read
// and the write each pass the gate on their own; the exchange with Cloud does
// not, so stopping refreshes waits only for a file operation in flight. A
// refresh that finds the gate closed before its write-back, or whose
// write-back fails, still returns the token it got.
func refreshCredentialsFile(ctx context.Context, sessionID string, gate *cloudRefreshGate, file cloudCredentialsFile) (*oauth2.Token, error) {
	if !gate.enter() {
		return nil, errCloudRefreshSessionClosing
	}
	data, err := file.read(ctx)
	gate.exit()
	if err != nil {
		return nil, fmt.Errorf("refresh cloud token: read credentials: %w", err)
	}
	var token oauth2.Token
	if err := json.Unmarshal(data, &token); err != nil {
		return nil, fmt.Errorf("refresh cloud token: parse credentials: %w", err)
	}
	refreshed, err := file.refresh(ctx, &token)
	if err != nil {
		return nil, fmt.Errorf("refresh cloud token: %w", err)
	}
	if refreshed.AccessToken == token.AccessToken {
		return refreshed, nil
	}
	encoded, err := json.Marshal(refreshed)
	if err != nil {
		slog.Warn("refreshed cloud token not written back", "session", sessionID, "error", err)
		return refreshed, nil
	}
	if !gate.enter() {
		slog.Info("refreshed cloud token not written back: the main client is shutting down", "session", sessionID)
		return refreshed, nil
	}
	err = file.write(ctx, encoded)
	gate.exit()
	if err != nil {
		slog.Warn("refreshed cloud token not written back", "session", sessionID, "error", err)
		return refreshed, nil
	}
	slog.Info("refreshed cloud credentials", "session", sessionID)
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

// cloudMetricQueueSize bounds the metric collections waiting for Cloud; the
// oldest is dropped beyond it.
const cloudMetricQueueSize = 256

// cloudMetricQueue exports the session's Cloud metrics on a goroutine of its
// own. A client's metric reader flushes and shuts down wherever the client's
// last lease is released, the /shutdown request's own cleanup included, so
// its exports must never wait on Cloud: Export copies the collection and
// returns, ForceFlush returns at once, and the queue drains in the
// background, each export within the bound. The session's teardown gets one
// bound for draining what is left, the export in flight and releasing the
// exporter. Beyond cloudMetricQueueSize the oldest whole collection is
// dropped; with cumulative temporality that loses resolution, not totals.
type cloudMetricQueue struct {
	next  sdkmetric.Exporter
	bound cloudFlushBound

	// ctx ends every export; Shutdown cancels it at its deadline.
	ctx    context.Context
	cancel context.CancelFunc

	mu       sync.Mutex
	queue    []*otlpmetricsv1.ResourceMetrics
	dropped  int
	closed   bool
	wake     chan struct{}
	drained  chan struct{}
	shutdown sync.Once
}

func newCloudMetricQueue(next sdkmetric.Exporter, bound cloudFlushBound) *cloudMetricQueue {
	ctx, cancel := context.WithCancel(context.Background())
	q := &cloudMetricQueue{
		next:    next,
		bound:   bound,
		ctx:     ctx,
		cancel:  cancel,
		wake:    make(chan struct{}, 1),
		drained: make(chan struct{}),
	}
	go q.run()
	return q
}

func (q *cloudMetricQueue) Temporality(kind sdkmetric.InstrumentKind) metricdata.Temporality {
	return q.next.Temporality(kind)
}

func (q *cloudMetricQueue) Aggregation(kind sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return q.next.Aggregation(kind)
}

// Export queues a deep copy of the collection: the reader reuses its buffers
// once Export returns, and the conversion to protobuf keeps some of them
// (histogram bounds and bucket counts) by reference.
func (q *cloudMetricQueue) Export(_ context.Context, metrics *metricdata.ResourceMetrics) error {
	if metrics == nil || len(metrics.ScopeMetrics) == 0 {
		return nil
	}
	converted, err := telemetry.ResourceMetricsToPB(metrics)
	if err != nil {
		slog.Warn("session metrics not published to Cloud", "session", q.bound.sessionID, "error", err)
		return nil
	}
	copied := proto.Clone(converted).(*otlpmetricsv1.ResourceMetrics)
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return nil
	}
	if len(q.queue) >= cloudMetricQueueSize {
		q.queue = q.queue[1:]
		q.dropped++
	}
	q.queue = append(q.queue, copied)
	q.mu.Unlock()
	select {
	case q.wake <- struct{}{}:
	default:
	}
	return nil
}

func (q *cloudMetricQueue) ForceFlush(context.Context) error { return nil }

// Shutdown drains the queue, lets the export in flight finish and releases
// the exporter, all by one deadline: the bound, or ctx's deadline if
// earlier. At the deadline the export in flight is cancelled and whatever is
// left is dropped.
func (q *cloudMetricQueue) Shutdown(ctx context.Context) error {
	q.shutdown.Do(func() {
		deadline := time.Now().Add(q.bound.timeout)
		if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
			deadline = d
		}
		q.mu.Lock()
		q.closed = true
		q.mu.Unlock()
		select {
		case q.wake <- struct{}{}:
		default:
		}
		timer := time.NewTimer(time.Until(deadline))
		defer timer.Stop()
		select {
		case <-q.drained:
		case <-timer.C:
		}
		q.cancel()
		<-q.drained
		q.mu.Lock()
		dropped := q.dropped + len(q.queue)
		q.queue = nil
		q.mu.Unlock()
		if dropped > 0 {
			slog.Warn("session metrics not fully published to Cloud", "session", q.bound.sessionID, "dropped", dropped)
		}
		releaseCtx, cancel := context.WithDeadline(context.WithoutCancel(ctx), deadline)
		defer cancel()
		if err := q.next.Shutdown(releaseCtx); err != nil {
			slog.Warn("session telemetry not fully published to Cloud", "session", q.bound.sessionID, "op", "shutdown metrics", "error", err)
		}
	})
	return nil
}

func (q *cloudMetricQueue) run() {
	defer close(q.drained)
	for {
		q.mu.Lock()
		if q.ctx.Err() != nil || (q.closed && len(q.queue) == 0) {
			q.mu.Unlock()
			return
		}
		if len(q.queue) == 0 {
			q.mu.Unlock()
			select {
			case <-q.wake:
			case <-q.ctx.Done():
			}
			continue
		}
		next := q.queue[0]
		q.queue = q.queue[1:]
		q.mu.Unlock()
		metrics, err := telemetry.ResourceMetricsFromPB(next)
		if err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(q.ctx, q.bound.timeout)
		err = q.next.Export(ctx, metrics)
		cancel()
		if err != nil && q.ctx.Err() == nil {
			slog.Warn("session telemetry not fully published to Cloud", "session", q.bound.sessionID, "op", "export metrics", "error", err)
		}
	}
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
	next := sess.spanExporter
	if sess.publishesToCloud() {
		next = enginetel.MultiSpanExporter{
			sess.spanExporter,
			sessionCloudSpanForwarder{telemetry.SpanForwarder{Processors: []sdktrace.SpanProcessor{sess.cloudSpanProcessor}}},
		}
	}
	return originSpanExporter{origin: origin, next: next}
}

func (sess *daggerSession) postedLogExporter(origin string) sdklog.Exporter {
	next := sess.logExporter
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

// cloudPayloadOnce passes each call payload to the session's Cloud log
// pipeline once, by digest. The client routing writes a payload at most
// once per destination; Cloud is one destination, reached by the engine's own
// emissions, by records posted from the session's containers (a nested CLI
// re-posts the payloads it received) and by a scale-out engine's stream. A
// digest is marked when its payload is handed to the pipeline's payload path,
// which queues every payload and retries a failed export with backoff up to
// enginetel.CallPayloadMaxExportAttempts times (see cloudLogPipeline), so a
// later copy would only repeat it.
type cloudPayloadOnce struct {
	sdklog.Processor

	mu   sync.Mutex
	seen map[string]struct{}
}

func newCloudPayloadOnce(next sdklog.Processor) *cloudPayloadOnce {
	return &cloudPayloadOnce{Processor: next, seen: map[string]struct{}{}}
}

func (p *cloudPayloadOnce) OnEmit(ctx context.Context, rec *sdklog.Record) error {
	if rec != nil {
		if digest, payload, err := classifyCallPayloadRecord(*rec); err == nil && payload && digest != "" {
			p.mu.Lock()
			_, sent := p.seen[digest]
			p.seen[digest] = struct{}{}
			p.mu.Unlock()
			if sent {
				return nil
			}
		}
	}
	return p.Processor.OnEmit(ctx, rec)
}

// cloudLogPipeline carries the session's records to Cloud. Call payloads take
// the lossless, retrying payload path the client routing uses
// (enginetel.CallPayloadBatchProcessor); every other record takes the batch
// processor, whose queue drops its oldest record when full. A payload lost
// there would never reach Cloud: cloudPayloadOnce suppresses every later copy.
// Both share the Cloud exporter, which the batch processor's Shutdown shuts
// down, so the payload path stops first.
type cloudLogPipeline struct {
	payloads *enginetel.CallPayloadBatchProcessor
	records  *sdklog.BatchProcessor
	others   sdklog.Processor
}

func newCloudLogPipeline(exporter sdklog.Exporter) *cloudLogPipeline {
	records := sdklog.NewBatchProcessor(exporter, sdklog.WithExportInterval(telemetry.NearlyImmediate))
	return &cloudLogPipeline{
		payloads: enginetel.NewCallPayloadBatchProcessor(exporter),
		records:  records,
		others:   enginetel.WithoutCallPayloads(records),
	}
}

func (p *cloudLogPipeline) OnEmit(ctx context.Context, rec *sdklog.Record) error {
	// Each side keeps only its own records.
	return errors.Join(p.payloads.OnEmit(ctx, rec), p.others.OnEmit(ctx, rec))
}

func (p *cloudLogPipeline) Enabled(context.Context, sdklog.EnabledParameters) bool {
	return true
}

// ForceFlush flushes both paths at once, within ctx.
func (p *cloudLogPipeline) ForceFlush(ctx context.Context) error {
	return bothWithin(ctx, p.payloads.ForceFlush, p.records.ForceFlush)
}

// Shutdown stops the payload path while flushing the other records, both
// within ctx, then shuts the batch processor and with it the exporter down.
func (p *cloudLogPipeline) Shutdown(ctx context.Context) error {
	err := bothWithin(ctx, p.payloads.Shutdown, p.records.ForceFlush)
	return errors.Join(err, p.records.Shutdown(ctx))
}

func bothWithin(ctx context.Context, a, b func(context.Context) error) error {
	errB := make(chan error, 1)
	go func() { errB <- b(ctx) }()
	errA := a(ctx)
	return errors.Join(errA, <-errB)
}

// sessionCloudSpanForwarder and sessionCloudLogForwarder feed the session's
// Cloud processors, which the session shuts down itself.
type sessionCloudSpanForwarder struct{ telemetry.SpanForwarder }

func (sessionCloudSpanForwarder) Shutdown(context.Context) error { return nil }

type sessionCloudLogForwarder struct{ telemetry.LogForwarder }

func (sessionCloudLogForwarder) Shutdown(context.Context) error { return nil }
