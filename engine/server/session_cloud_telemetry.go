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
	"golang.org/x/sync/singleflight"
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
	// The client hands over its own Cloud URL, which it may reach when this
	// engine cannot. Take over publishing only from a URL this engine
	// reaches; a declined session leaves the client forwarding.
	cloudURL := enginetel.ResolveCloudURL(md.CloudURL)
	if err := srv.cloudReach.check(context.Background(), cloudURL); err != nil {
		slog.Warn("session telemetry not published to Cloud: the engine cannot reach the Cloud URL", "session", sess.sessionID, "url", cloudURL, "error", err)
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
	bound := cloudFlushBound{sessionID: sess.sessionID, timeout: sessionTelemetryFlushTimeout, budget: &cloudShutdownBudget{}}
	if srv.sessionCloudFlushTimeout > 0 {
		bound.timeout = srv.sessionCloudFlushTimeout
	}
	forwarder, err := srv.startCloudForwarder(sess.sessionID, sess.mainClientCallerID, spans, logs, bound.timeout)
	if err != nil {
		slog.Warn("session telemetry not published to Cloud: cannot read the main client's telemetry store", "session", sess.sessionID, "error", err)
		ctx, cancel := context.WithTimeout(context.Background(), bound.timeout)
		defer cancel()
		_ = errors.Join(spans.Shutdown(ctx), logs.Shutdown(ctx), metrics.Shutdown(ctx))
		return
	}
	sess.cloudBound = bound
	sess.cloudRefresh = &cloudRefreshGate{}
	sess.cloudForwarder = forwarder
	sess.cloudFlushers = append(sess.cloudFlushers, func(ctx context.Context) {
		sess.drainCloudForwarder(ctx)
	})
	sess.cloudMetrics = newCloudMetricQueue(metrics, sess.cloudBound)
}

// startCloudForwarder starts forwarding the store of the session's main
// client to Cloud through spans and logs, keeping the store from collection
// until the forwarder releases it.
func (srv *Server) startCloudForwarder(sessionID, mainClientID string, spans sdktrace.SpanExporter, logs sdklog.Exporter, shutdownTimeout time.Duration) (*cloudForwarder, error) {
	if srv.clientDBs == nil {
		return nil, errors.New("no telemetry stores")
	}
	db, err := srv.clientDBs.Open(context.Background(), mainClientID)
	if err != nil {
		return nil, err
	}
	tuning := defaultCloudForwardTuning()
	tuning.shutdownTimeout = shutdownTimeout
	if srv.cloudForwardTuning != nil {
		tuning = *srv.cloudForwardTuning
	}
	var forwarder *cloudForwarder
	released := make(chan struct{})
	forwarder = newCloudForwarder(sessionID, mainClientID, db, spans, logs, tuning, func() {
		<-released
		srv.cloudForwarders.remove(forwarder)
	})
	srv.cloudForwarders.add(forwarder)
	close(released)
	return forwarder, nil
}

// drainCloudForwarder publishes what the session sent so far: the session's
// providers put what they hold in the store, and the forwarder publishes up to
// the store's end, within the Cloud bound. Whatever is left is published in
// the background.
func (sess *daggerSession) drainCloudForwarder(ctx context.Context) {
	var errs error
	if sess.tracerProvider != nil {
		errs = errors.Join(errs, sess.tracerProvider.ForceFlush(ctx))
	}
	if sess.loggerProvider != nil {
		errs = errors.Join(errs, sess.loggerProvider.ForceFlush(ctx))
	}
	if errs != nil {
		slog.Warn("session telemetry not flushed to the store before publishing to Cloud", "session", sess.sessionID, "error", errs)
	}
	sess.cloudBound.bounded(ctx, "drain spans and logs", func(ctx context.Context) error {
		if err := sess.cloudForwarder.drain(ctx); err != nil {
			slog.Info("session telemetry still publishing to Cloud after shutdown", "session", sess.sessionID, "error", err)
		}
		return nil
	})
}

const (
	// cloudReachTimeout bounds one probe of a Cloud URL. Session start waits
	// for it only when the URL's last answer has expired: an unresolvable or
	// refusing host fails at once, a blackholed route takes the whole bound.
	cloudReachTimeout = 2 * time.Second
	// How long a probe's result stands: a reachable URL is probed again after
	// cloudReachOK, an unreachable one after cloudReachFailed.
	cloudReachOK     = 10 * time.Minute
	cloudReachFailed = time.Minute
)

// cloudReachability caches, per Cloud URL, whether this engine reaches it.
// Concurrent checks of a URL without a current result share one probe. The
// zero value probes with enginetel.ProbeCloudURL.
type cloudReachability struct {
	probe func(context.Context, string) error
	now   func() time.Time

	probes singleflight.Group
	mu     sync.Mutex
	known  map[string]cloudReachResult
}

type cloudReachResult struct {
	err   error
	until time.Time
}

// check returns nil when this engine reached cloudURL within the result's
// lifetime, probing it again, once for all concurrent checks, when that has
// expired.
func (c *cloudReachability) check(ctx context.Context, cloudURL string) error {
	if ok, err := c.current(cloudURL); ok {
		return err
	}
	_, err, _ := c.probes.Do(cloudURL, func() (any, error) {
		if ok, err := c.current(cloudURL); ok {
			return nil, err
		}
		probe := enginetel.ProbeCloudURL
		if c.probe != nil {
			probe = c.probe
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cloudReachTimeout)
		defer cancel()
		err := probe(ctx, cloudURL)
		lifetime := cloudReachOK
		if err != nil {
			lifetime = cloudReachFailed
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.known == nil {
			c.known = map[string]cloudReachResult{}
		}
		c.known[cloudURL] = cloudReachResult{err: err, until: c.clock().Add(lifetime)}
		return nil, err
	})
	return err
}

// current returns cloudURL's cached result, if it has not expired.
func (c *cloudReachability) current(cloudURL string) (bool, error) {
	now := c.clock()
	c.mu.Lock()
	defer c.mu.Unlock()
	known, ok := c.known[cloudURL]
	if !ok || !now.Before(known.until) {
		return false, nil
	}
	return true, known.err
}

func (c *cloudReachability) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// publishesToCloud reports whether the session publishes its telemetry to
// Cloud. It is decided once, when the session's telemetry initializes and
// before any client can open a telemetry stream, so every stream of the
// session is confirmed alike and a client's forwarding never flips midway.
func (sess *daggerSession) publishesToCloud() bool {
	return sess.cloudForwarder != nil && sess.cloudMetrics != nil
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

// isClosed reports whether refreshes stopped. A nil gate never closes.
func (g *cloudRefreshGate) isClosed() bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.closed
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
	// Past the main client's shutdown no refresh can reach its credentials
	// file; saying so plainly lets the Cloud forwarder stop once the token
	// it holds expires, rather than retry until its deadline.
	if sess.cloudRefresh.isClosed() {
		return nil, errCloudRefreshSessionClosing
	}
	record, err := srv.clientRecordFromIDs(sess.sessionID, sess.mainClientCallerID)
	if err != nil {
		// The session is gone, and its main client with it.
		return nil, fmt.Errorf("%w: %w", errCloudRefreshSessionClosing, err)
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

// cloudFlushBound bounds every wait on Cloud: at the earliest of its own
// timeout, the caller's deadline and the shutdown budget. It logs errors
// instead of returning them, so a Cloud outage never fails the command, and
// never holds the client's shutdown.
type cloudFlushBound struct {
	sessionID string
	timeout   time.Duration
	budget    *cloudShutdownBudget
}

// bounded runs op within the bound. With nothing left of it, op is skipped.
func (b cloudFlushBound) bounded(ctx context.Context, what string, op func(context.Context) error) {
	deadline := time.Now().Add(b.timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if b.budget != nil {
		if d, ok := b.budget.current(); ok && d.Before(deadline) {
			deadline = d
		}
	}
	if !time.Now().Before(deadline) {
		slog.Info("no time left to wait on Cloud", "session", b.sessionID, "op", what)
		return
	}
	ctx, cancel := context.WithDeadline(context.WithoutCancel(ctx), deadline)
	defer cancel()
	if err := op(ctx); err != nil {
		slog.Warn("waiting on Cloud", "session", b.sessionID, "op", what, "error", err)
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

// flushSessionCloudTelemetry publishes what the session has sent so far to
// Cloud, within the bound. The main client's shutdown calls it once, before
// the session's attachables close, because refreshing an OAuth token reads
// the client's credentials file through them.
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
// session with the same credential. A stream the remote does not confirm
// reaches Cloud like the rest of the session, from the main client's store;
// a stream it confirms reaches the store marked as published, and the
// session's forwarder leaves it out. When this session does not publish, the
// remote is not asked either, and the stream reaches Cloud once, through the
// client that forwards this session's telemetry.
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
	params.EngineTraceWithoutCloud = cloudPublishedSpanExporter{next: parent.spanExporter}
	params.EngineLogsWithoutCloud = cloudPublishedLogExporter{next: parent.logExporter}
	params.EngineMetricsWithoutCloud = params.EngineMetrics
	params.EngineMetrics = []sdkmetric.Exporter{
		parent.metricExporter,
		enginetel.SharedMetricExporter{Exporter: sess.cloudMetrics},
	}
}

// Telemetry a process in one of the session's containers posts to the
// engine (an SDK, a nested CLI) reaches the client routing directly, never
// the session's providers. The posted* exporters stamp the origin once; the
// session's store-to-Cloud forwarder publishes the spans and logs with the
// rest of the main client's store. Metrics have no store lane to Cloud, so
// they also go to the session's Cloud metric queue. A scale-out engine's
// returned stream does not come this way: its own containers post to the
// remote engine, which publishes them itself when it confirmed.

func (sess *daggerSession) postedSpanExporter(origin string) sdktrace.SpanExporter {
	return originSpanExporter{origin: origin, next: sess.spanExporter}
}

func (sess *daggerSession) postedLogExporter(origin string) sdklog.Exporter {
	return originLogExporter{origin: origin, next: sess.logExporter}
}

func (sess *daggerSession) postedMetricExporters(client sdkmetric.Exporter) []sdkmetric.Exporter {
	if !sess.publishesToCloud() {
		return []sdkmetric.Exporter{client}
	}
	return []sdkmetric.Exporter{client, enginetel.SharedMetricExporter{Exporter: sess.cloudMetrics}}
}
