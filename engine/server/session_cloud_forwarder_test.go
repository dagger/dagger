package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/agentcontrol"
	"github.com/dagger/dagger/engine/clientdb"
	enginetel "github.com/dagger/dagger/engine/telemetry"
)

// Cloud export behaviors of the forwarder tests' exporters.
const (
	forwardOK int32 = iota
	forwardFail
	forwardHang
)

// forwardTestExporter stands in for one of the session's Cloud exporters. It
// fails, hangs until the export's context ends, or records, per mode.
type forwardTestExporter struct {
	mode        atomic.Int32
	failures    atomic.Int32 // exports to fail before recording, in forwardOK
	attempts    atomic.Int32
	inFlight    atomic.Int32
	maxInFlight atomic.Int32
	shutdowns   atomic.Int32
	// reject, when set, fails any export carrying a span name or log body
	// it returns an error for, recording nothing.
	reject func(name string) error

	mu       sync.Mutex
	maxBatch int
	sizes    []int
	names    []string // span names or log bodies, in export order
	payloads []string
	controls []sdklog.Record
}

func (e *forwardTestExporter) enter(ctx context.Context, n int) error {
	e.attempts.Add(1)
	inFlight := e.inFlight.Add(1)
	for {
		seen := e.maxInFlight.Load()
		if inFlight <= seen || e.maxInFlight.CompareAndSwap(seen, inFlight) {
			break
		}
	}
	e.mu.Lock()
	e.maxBatch = max(e.maxBatch, n)
	e.sizes = append(e.sizes, n)
	e.mu.Unlock()
	switch e.mode.Load() {
	case forwardFail:
		return errors.New("cloud unavailable")
	case forwardHang:
		<-ctx.Done()
		return ctx.Err()
	}
	if e.failures.Load() > 0 {
		e.failures.Add(-1)
		return errors.New("cloud unavailable")
	}
	return nil
}

func (e *forwardTestExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	defer e.inFlight.Add(-1)
	if err := e.enter(ctx, len(spans)); err != nil {
		return err
	}
	if e.reject != nil {
		for _, span := range spans {
			if err := e.reject(span.Name()); err != nil {
				return err
			}
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, span := range spans {
		e.names = append(e.names, span.Name())
	}
	return nil
}

func (e *forwardTestExporter) Export(ctx context.Context, records []sdklog.Record) error {
	defer e.inFlight.Add(-1)
	if err := e.enter(ctx, len(records)); err != nil {
		return err
	}
	if e.reject != nil {
		for _, rec := range records {
			if err := e.reject(rec.Body().AsString()); err != nil {
				return err
			}
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, rec := range records {
		switch digest, payload, _ := classifyCallPayloadRecord(rec); {
		case payload:
			e.payloads = append(e.payloads, digest)
		case agentcontrol.IsRecord(rec):
			e.controls = append(e.controls, rec.Clone())
		default:
			e.names = append(e.names, rec.Body().AsString())
		}
	}
	return nil
}

func (e *forwardTestExporter) ForceFlush(context.Context) error { return nil }
func (e *forwardTestExporter) Shutdown(context.Context) error {
	e.shutdowns.Add(1)
	return nil
}

func (e *forwardTestExporter) received() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.names...)
}

// fastCloudForwardTuning is the forwarder's tuning scaled down for tests.
func fastCloudForwardTuning() cloudForwardTuning {
	return cloudForwardTuning{
		batchRows:         100,
		maxPayloadSize:    cloudForwardMaxPayloadSize,
		poll:              5 * time.Millisecond,
		exportTimeout:     50 * time.Millisecond,
		minBackoff:        time.Millisecond,
		maxBackoff:        10 * time.Millisecond,
		backgroundTimeout: 30 * time.Second,
		shutdownTimeout:   time.Second,
	}
}

// forwardTestStore is a server with one client store, "main", to forward.
func forwardTestStore(t *testing.T) *Server {
	t.Helper()
	srv := &Server{clientDBs: clientdb.NewDBs(t.TempDir())}
	srv.telemetryPubSub = NewPubSub(srv)
	return srv
}

// startForwardTest starts a forwarder of srv's "main" store with its own
// store reference, as startCloudForwarder does.
func startForwardTest(t *testing.T, srv *Server, spans, logs *forwardTestExporter, tuning cloudForwardTuning) *cloudForwarder {
	t.Helper()
	db, err := srv.clientDBs.Open(t.Context(), "main")
	require.NoError(t, err)
	var f *cloudForwarder
	registered := make(chan struct{})
	f = newCloudForwarder("session", "main", db, spans, logs, tuning, func() {
		<-registered
		srv.cloudForwarders.remove(f)
	})
	srv.cloudForwarders.add(f)
	close(registered)
	t.Cleanup(func() {
		f.stop(errors.New("test done"))
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		require.NoError(t, f.waitReleased(ctx))
	})
	return f
}

func writeForwardTestSpans(t *testing.T, srv *Server, from, n int) {
	t.Helper()
	spans := make([]sdktrace.ReadOnlySpan, n)
	for i := range n {
		id := from + i
		spans[i] = tracetest.SpanStub{
			Name: fmt.Sprintf("span-%d", id),
			SpanContext: trace.NewSpanContext(trace.SpanContextConfig{
				TraceID: trace.TraceID{1}, SpanID: trace.SpanID{byte(id >> 16), byte(id >> 8), byte(id), 1}, TraceFlags: trace.FlagsSampled,
			}),
			StartTime: time.Now(),
			EndTime:   time.Now(),
		}.Snapshot()
	}
	require.NoError(t, srv.telemetryPubSub.Spans("main").ExportSpans(t.Context(), spans))
}

func writeForwardTestLogs(t *testing.T, srv *Server, records ...otellog.Record) {
	t.Helper()
	sdkRecords := make([]sdklog.Record, len(records))
	for i, rec := range records {
		sdkRecords[i] = controlTestRecord(t, rec)
	}
	require.NoError(t, srv.telemetryPubSub.Logs("main").Export(t.Context(), sdkRecords))
}

func forwardTestNames(prefix string, n int) []string {
	names := make([]string, n)
	for i := range names {
		names[i] = fmt.Sprintf("%s-%d", prefix, i)
	}
	return names
}

// Through an outage, however many rows pile up, each lane holds one batch:
// no export carries more than a batch, one export at a time, and the cursors
// stay put while Cloud fails or hangs. Once Cloud recovers everything arrives
// exactly once, in order.
func TestCloudForwarderHoldsOneBatchThroughOutage(t *testing.T) {
	t.Parallel()
	srv := forwardTestStore(t)
	spans, logs := &forwardTestExporter{}, &forwardTestExporter{}
	spans.mode.Store(forwardFail)
	logs.mode.Store(forwardHang)
	tuning := fastCloudForwardTuning()
	f := startForwardTest(t, srv, spans, logs, tuning)

	const nSpans, nLogs = 5000, 3000
	for i := 0; i < nSpans; i += 500 {
		writeForwardTestSpans(t, srv, i, 500)
	}
	records := make([]otellog.Record, nLogs)
	for i := range records {
		records[i] = cloudOtherRecord(i)
	}
	writeForwardTestLogs(t, srv, records...)

	require.Eventually(t, func() bool {
		return spans.attempts.Load() >= 20 && logs.attempts.Load() >= 3
	}, 10*time.Second, time.Millisecond, "the forwarder keeps retrying through the outage")
	for _, lane := range f.lanes {
		cursor, _ := lane.position()
		require.Zero(t, cursor, "%s: no row counts as published before Cloud takes it", lane.signal)
	}
	require.Empty(t, spans.received())
	require.Empty(t, logs.received())

	spans.mode.Store(forwardOK)
	logs.mode.Store(forwardOK)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	require.NoError(t, f.drain(ctx))

	want := forwardTestNames("span", nSpans)
	require.Equal(t, want, spans.received(), "every span once, in order")
	wantLogs := make([]string, nLogs)
	for i := range wantLogs {
		wantLogs[i] = fmt.Sprintf("exec output %d", i)
	}
	require.Equal(t, wantLogs, logs.received(), "every record once, in order")
	for _, e := range []*forwardTestExporter{spans, logs} {
		require.LessOrEqual(t, e.maxBatch, tuning.batchRows, "one batch at most per export")
		require.Equal(t, int32(1), e.maxInFlight.Load(), "one export at a time")
	}

	f.finish()
	require.NoError(t, f.waitReleased(ctx))
	require.Equal(t, int32(1), spans.shutdowns.Load(), "the forwarder owns its exporters")
	require.Equal(t, int32(1), logs.shutdowns.Load())
	require.Equal(t, clientdb.OpenStats{}, srv.clientDBs.OpenStats(), "the forwarder releases its store")
	require.Empty(t, srv.cloudForwarders.KeepSet())
}

// Agent control records, call payloads and ordinary records all reach Cloud
// from the store, in the order the store took them, through export failures
// and a burst far beyond any in-memory queue.
func TestCloudForwarderPublishesControlsAndPayloadsInOrder(t *testing.T) {
	t.Parallel()
	srv := forwardTestStore(t)
	spans, logs := &forwardTestExporter{}, &forwardTestExporter{}
	logs.failures.Store(3)
	f := startForwardTest(t, srv, spans, logs, fastCloudForwardTuning())

	agent := archiveAgent()
	agent.Activity = agent.Activity.UTC()
	edge := agentcontrol.Subscription{
		EdgeKey:  agentcontrol.EdgeKey{Namespace: agent.Namespace, Watched: agent.Handle, Subscriber: "chief"},
		Revision: 1, States: []string{"IDLE", "FAILED"},
	}
	newer := agent
	newer.Revision++
	writeForwardTestLogs(t, srv, agent.Record(), edge.Record(), newer.Record(), cloudPayloadRecordValue(agent.Digest))
	const burst = 7000
	records := make([]otellog.Record, burst)
	for i := range records {
		records[i] = cloudOtherRecord(i)
	}
	writeForwardTestLogs(t, srv, records...)
	writeForwardTestLogs(t, srv, cloudPayloadRecordValue("xxh3:second"))

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	require.NoError(t, f.drain(ctx))
	logs.mu.Lock()
	defer logs.mu.Unlock()
	require.Len(t, logs.controls, 3)
	gotAgent, _, err := agentcontrol.Decode(logs.controls[0])
	require.NoError(t, err)
	require.Equal(t, agent, *gotAgent)
	_, gotEdge, err := agentcontrol.Decode(logs.controls[1])
	require.NoError(t, err)
	require.Equal(t, edge, *gotEdge)
	gotNewer, _, err := agentcontrol.Decode(logs.controls[2])
	require.NoError(t, err)
	require.Equal(t, newer, *gotNewer)
	for _, rec := range logs.controls {
		require.Equal(t, archiveTestTrace, rec.TraceID().String())
	}
	require.Equal(t, []string{agent.Digest, "xxh3:second"}, logs.payloads, "each payload once, in order")
	require.Len(t, logs.names, burst, "nothing dropped")
}

// A batch whose request exceeds the payload limit is read again in smaller
// prefixes from the same cursor; a single row over it is sent alone.
func TestCloudForwarderSplitsOversizedBatches(t *testing.T) {
	t.Parallel()
	srv := forwardTestStore(t)
	spans, logs := &forwardTestExporter{}, &forwardTestExporter{}
	tuning := fastCloudForwardTuning()
	tuning.maxPayloadSize = 8 << 10
	f := startForwardTest(t, srv, spans, logs, tuning)

	big := make([]byte, 1<<10)
	for i := range big {
		big[i] = 'x'
	}
	const n = 50
	records := make([]otellog.Record, 0, n+1)
	for i := range n {
		var rec otellog.Record
		rec.SetBody(otellog.StringValue(fmt.Sprintf("record-%d %s", i, big)))
		records = append(records, rec)
	}
	var huge otellog.Record
	huge.SetBody(otellog.StringValue("huge " + string(make([]byte, 16<<10))))
	records = append(records, huge)
	writeForwardTestLogs(t, srv, records...)

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	require.NoError(t, f.drain(ctx))
	got := logs.received()
	require.Len(t, got, n+1, "every record once")
	for i := range n {
		require.Equal(t, fmt.Sprintf("record-%d %s", i, big), got[i])
	}
	logs.mu.Lock()
	defer logs.mu.Unlock()
	require.Greater(t, len(logs.sizes), 2, "the batch was split")
	require.Equal(t, 1, logs.sizes[len(logs.sizes)-1], "the oversized row goes alone")
	for _, size := range logs.sizes[:len(logs.sizes)-1] {
		// 1KiB bodies: a batch within 8KiB carries fewer than 8.
		require.Less(t, size, 8)
	}
}

// Stopping cancels the export in flight and releases at once: no export
// outlives the forwarder, which reports what it left unpublished.
func TestCloudForwarderStopCancelsExports(t *testing.T) {
	t.Parallel()
	srv := forwardTestStore(t)
	spans, logs := &forwardTestExporter{}, &forwardTestExporter{}
	spans.mode.Store(forwardHang)
	logs.mode.Store(forwardHang)
	tuning := fastCloudForwardTuning()
	tuning.exportTimeout = time.Hour
	f := startForwardTest(t, srv, spans, logs, tuning)
	writeForwardTestSpans(t, srv, 0, 10)
	writeForwardTestLogs(t, srv, cloudOtherRecord(0))
	require.Eventually(t, func() bool {
		return spans.inFlight.Load() == 1 && logs.inFlight.Load() == 1
	}, 10*time.Second, time.Millisecond)

	start := time.Now()
	f.stop(errors.New("stop"))
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	require.NoError(t, f.waitReleased(ctx))
	require.Less(t, time.Since(start), time.Second)
	require.Zero(t, spans.inFlight.Load(), "no export outlives the forwarder")
	require.Zero(t, logs.inFlight.Load(), "no export outlives the forwarder")
	require.Equal(t, clientdb.OpenStats{}, srv.clientDBs.OpenStats())
}

func TestCloudForwarderFinishAfterRelease(t *testing.T) {
	t.Parallel()
	srv := forwardTestStore(t)
	f := startForwardTest(t, srv, &forwardTestExporter{}, &forwardTestExporter{}, fastCloudForwardTuning())
	f.stop(errors.New("stop"))
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	require.NoError(t, f.waitReleased(ctx))

	f.finish()
	f.timerMu.Lock()
	active := f.deadline != nil && f.deadline.Stop()
	f.timerMu.Unlock()
	require.False(t, active, "finishing a released forwarder must not retain it with a new deadline")
	require.Empty(t, srv.cloudForwarders.KeepSet())
	require.Equal(t, clientdb.OpenStats{}, srv.clientDBs.OpenStats())
}

// Once the session is gone, a credential that can no longer be used ends
// forwarding at once, rather than at the background deadline; while the
// session runs, the forwarder keeps retrying.
func TestCloudForwarderStopsWhenCredentialUnusable(t *testing.T) {
	t.Parallel()
	srv := forwardTestStore(t)
	spans, logs := &forwardTestExporter{}, &forwardTestExporter{}
	tokenGone := &credentialGoneExporter{forwardTestExporter: spans}
	db, err := srv.clientDBs.Open(t.Context(), "main")
	require.NoError(t, err)
	f := newCloudForwarder("session", "main", db, tokenGone, logs, fastCloudForwardTuning(), nil)
	writeForwardTestSpans(t, srv, 0, 10)

	require.Eventually(t, func() bool { return spans.attempts.Load() >= 5 }, 10*time.Second, time.Millisecond)
	select {
	case <-f.done:
		t.Fatal("stopped while the session runs")
	default:
	}
	f.finish()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	require.NoError(t, f.waitReleased(ctx), "stops well before the background deadline")
	require.ErrorIs(t, context.Cause(f.ctx), errCloudForwardTokenUnusable)
}

// credentialGoneExporter fails every export as an OAuth refresh does once
// the main client is gone.
type credentialGoneExporter struct {
	*forwardTestExporter
}

func (e *credentialGoneExporter) ExportSpans(ctx context.Context, _ []sdktrace.ReadOnlySpan) error {
	e.attempts.Add(1)
	return fmt.Errorf("post: %w", errCloudRefreshSessionClosing)
}

// After the session, forwarding through an outage ends at the background
// deadline.
func TestCloudForwarderBackgroundDeadline(t *testing.T) {
	t.Parallel()
	srv := forwardTestStore(t)
	spans, logs := &forwardTestExporter{}, &forwardTestExporter{}
	spans.mode.Store(forwardFail)
	tuning := fastCloudForwardTuning()
	tuning.backgroundTimeout = 200 * time.Millisecond
	f := startForwardTest(t, srv, spans, logs, tuning)
	writeForwardTestSpans(t, srv, 0, 10)
	f.finish()
	start := time.Now()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	require.NoError(t, f.waitReleased(ctx))
	require.GreaterOrEqual(t, time.Since(start), 150*time.Millisecond)
	require.ErrorIs(t, context.Cause(f.ctx), errCloudForwardDeadline)
	require.Empty(t, srv.cloudForwarders.KeepSet())
}

// The engine's stop gives caught-up forwarders the grace to finish and stops
// the rest.
func TestCloudForwardersStopAll(t *testing.T) {
	t.Parallel()
	srv := forwardTestStore(t)
	okSpans, okLogs := &forwardTestExporter{}, &forwardTestExporter{}
	caughtUp := startForwardTest(t, srv, okSpans, okLogs, fastCloudForwardTuning())
	writeForwardTestSpans(t, srv, 0, 10)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	require.NoError(t, caughtUp.drain(ctx))
	caughtUp.finish()

	hangSpans, hangLogs := &forwardTestExporter{}, &forwardTestExporter{}
	hangSpans.mode.Store(forwardHang)
	tuning := fastCloudForwardTuning()
	tuning.exportTimeout = time.Hour
	hanging := startForwardTest(t, srv, hangSpans, hangLogs, tuning)
	hanging.finish()

	start := time.Now()
	require.NoError(t, srv.cloudForwarders.stopAll(ctx, 200*time.Millisecond))
	require.Less(t, time.Since(start), 2*time.Second)
	require.NotErrorIs(t, context.Cause(caughtUp.ctx), errCloudForwardEngineStop, "the caught-up forwarder finished on its own")
	require.ErrorIs(t, context.Cause(hanging.ctx), errCloudForwardEngineStop)
	require.Empty(t, srv.cloudForwarders.KeepSet())
}

// A published marker keeps a row out of Cloud; nothing else does.
func TestCloudPublishedRow(t *testing.T) {
	t.Parallel()
	srv := forwardTestStore(t)
	var marked, plain otellog.Record
	marked.SetBody(otellog.StringValue("marked"))
	plain.SetBody(otellog.StringValue("plain"))
	exp := cloudPublishedLogExporter{next: srv.telemetryPubSub.Logs("main")}
	require.NoError(t, exp.Export(t.Context(), []sdklog.Record{controlTestRecord(t, marked)}))
	require.NoError(t, srv.telemetryPubSub.Logs("main").Export(t.Context(), []sdklog.Record{controlTestRecord(t, plain)}))
	db, err := srv.clientDBs.Open(t.Context(), "main")
	require.NoError(t, err)
	defer db.Close()
	next, req, rows, err := fetchLogBatch(t.Context(), db, 0, 10, forwardLogRow)
	require.NoError(t, err)
	require.Equal(t, 2, rows)
	require.Equal(t, int64(2), next)
	var bodies []string
	for _, rl := range req.GetResourceLogs() {
		for _, sl := range rl.GetScopeLogs() {
			for _, rec := range sl.GetLogRecords() {
				bodies = append(bodies, rec.GetBody().GetStringValue())
			}
		}
	}
	require.Equal(t, []string{"plain"}, bodies)
	require.False(t, cloudPublishedRow([]byte(`[{"key":"note","value":{"stringValue":"\"dagger.io/telemetry.cloud_published\""}}]`)),
		"the key named in a value is not the marker")
}

// The existing forwarder keeps the store alive and publishes its remaining
// telemetry after teardown, whether shutdown drained for a near-expiry token
// or left a fresh token's telemetry to the forwarder immediately.
func TestSessionCloudForwarderDrainsAfterTeardown(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		expiry time.Time
	}{
		{"fresh token", time.Now().Add(time.Hour)},
		{"token near expiry", time.Now().Add(time.Second)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			receiver := newCloudReceiver(t, false)
			receiver.refuse.Store(true)
			const bound = 300 * time.Millisecond
			tuning := fastCloudForwardTuning()
			tuning.exportTimeout = 5 * time.Second
			srv := &Server{sessionCloudFlushTimeout: bound, cloudForwardTuning: &tuning}
			sess, root := newCloudTestSession(t, srv, &engine.ClientMetadata{
				CloudAuth:               basicCloudAuth("dag_test_token"),
				CloudURL:                receiver.URL,
				CloudTelemetryPublisher: engine.CloudTelemetryPublisherEngine,
			})
			sess.state.Store(sessionStateInitialized)
			srv.daggerSessions = map[string]*daggerSession{sess.sessionID: sess}
			emitCloudTestTelemetry(t, sess, root)
			postTelemetry(t, srv.telemetryPubSub, sess.sessionID, root.clientID)

			start := time.Now()
			stopBudget := sess.startCloudShutdownBudget()
			sess.setCloudTokenExpiry(tc.expiry)
			sess.flushSessionCloudTelemetryForShutdown(ctx)
			require.NoError(t, sess.FlushTelemetry(ctx, "client shutdown"))
			stopBudget()
			require.Less(t, time.Since(start), bound+250*time.Millisecond, "the shutdown waits on the outage for at most one bound")

			start = time.Now()
			require.NoError(t, sess.shutdownTelemetry(ctx))
			require.Less(t, time.Since(start), bound+time.Second, "teardown does not wait on the forwarder")
			require.True(t, srv.cloudForwarders.KeepSet()[root.clientID], "the store is kept while the forwarder publishes")
			require.Equal(t, 1, srv.clientDBs.OpenStats().Refs, "the forwarder holds the store open")
			_, spans, logs, _ := receiver.snapshot()
			require.Empty(t, spans)
			require.Empty(t, logs)

			receiver.refuse.Store(false)
			require.NoError(t, sess.cloudForwarder.waitReleased(ctx))
			_, spans, logs, _ = receiver.snapshot()
			require.Equal(t, []string{"cloud-span"}, dedupe(filterNames(spans, "cloud-span")))
			require.Equal(t, 1, countName(spans, "posted-span"), "posted telemetry once")
			require.Equal(t, 1, countName(logs, "cloud-log"))
			require.Equal(t, 1, countName(logs, "posted-log"))
			require.Empty(t, srv.cloudForwarders.KeepSet(), "the forwarder released the store")
			require.Equal(t, clientdb.OpenStats{}, srv.clientDBs.OpenStats())
		})
	}
}

func countName(names []string, name string) int {
	return len(filterNames(names, name))
}

func filterNames(names []string, name string) []string {
	var out []string
	for _, got := range names {
		if got == name {
			out = append(out, got)
		}
	}
	return out
}

// Client store collection leaves a store alone while a forwarder publishes
// it, its session gone, and collects it once the forwarder released it.
func TestCloudForwarderKeepsStoreFromCollection(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	receiver := newCloudReceiver(t, false)
	receiver.refuse.Store(true)
	tuning := fastCloudForwardTuning()
	srv := &Server{cloudForwardTuning: &tuning}
	sess, root := newCloudTestSession(t, srv, &engine.ClientMetadata{
		CloudAuth:               basicCloudAuth("dag_test_token"),
		CloudURL:                receiver.URL,
		CloudTelemetryPublisher: engine.CloudTelemetryPublisherEngine,
	})
	emitCloudTestTelemetry(t, sess, root)
	require.NoError(t, sess.shutdownTelemetry(ctx))
	// No session keeps the store now; only the forwarder does.
	age := func() {
		old := time.Now().Add(-2 * clientdb.CollectGarbageAfter)
		entries, err := os.ReadDir(srv.clientDBs.Root)
		require.NoError(t, err)
		require.NotEmpty(t, entries)
		for _, entry := range entries {
			require.NoError(t, os.Chtimes(filepath.Join(srv.clientDBs.Root, entry.Name()), old, old))
		}
	}
	age()
	srv.gcClientDBsOnce()
	require.True(t, srv.cloudForwarders.KeepSet()[root.clientID])
	entries, err := os.ReadDir(srv.clientDBs.Root)
	require.NoError(t, err)
	require.NotEmpty(t, entries, "the forwarded store survives collection")

	sess.cloudForwarder.stop(errors.New("test"))
	require.NoError(t, sess.cloudForwarder.waitReleased(ctx))
	age()
	srv.gcClientDBsOnce()
	entries, err = os.ReadDir(srv.clientDBs.Root)
	require.NoError(t, err)
	require.Empty(t, entries, "a released store is collected")
}

// The Cloud exporters' own errors classify as the forwarder expects: 4xx
// statuses other than the credential's, 408 and 429 are permanent
// rejections; 401 and 403 are the credential's; 408, 429, 5xx and transport
// failures are transient.
func TestCloudExportErrorClassification(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		status     int
		permanent  bool
		credential bool
	}{
		{status: http.StatusBadRequest, permanent: true},
		{status: http.StatusRequestEntityTooLarge, permanent: true},
		{status: http.StatusUnprocessableEntity, permanent: true},
		{status: http.StatusUnauthorized, credential: true},
		{status: http.StatusForbidden, credential: true},
		{status: http.StatusRequestTimeout},
		{status: http.StatusTooManyRequests},
		{status: http.StatusInternalServerError},
		{status: http.StatusServiceUnavailable},
	} {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			t.Parallel()
			cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "refused", tc.status)
			}))
			defer cloud.Close()
			spans, logs, _, err := enginetel.NewCloudExporters(t.Context(), basicCloudAuth("dag_test_token"), nil, cloud.URL)
			require.NoError(t, err)
			// Retryable statuses are retried by the exporter until the
			// export's context ends.
			ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
			defer cancel()
			spanErr := spans.ExportSpans(ctx, []sdktrace.ReadOnlySpan{tracetest.SpanStub{
				Name: "span",
				SpanContext: trace.NewSpanContext(trace.SpanContextConfig{
					TraceID: trace.TraceID{1}, SpanID: trace.SpanID{1}, TraceFlags: trace.FlagsSampled,
				}),
			}.Snapshot()})
			logErr := logs.Export(ctx, []sdklog.Record{controlTestRecord(t, cloudOtherRecord(0))})
			for signal, err := range map[string]error{"spans": spanErr, "logs": logErr} {
				require.Error(t, err, signal)
				require.Equal(t, tc.permanent, cloudRejectedPermanently(err), "%s: %v", signal, err)
				require.Equal(t, tc.credential, cloudCredentialUnusable(err), "%s: %v", signal, err)
			}
			require.NoError(t, spans.Shutdown(t.Context()))
			require.NoError(t, logs.Shutdown(t.Context()))
		})
	}
	require.False(t, cloudRejectedPermanently(errors.New("dial tcp: connection refused")))
	require.False(t, cloudRejectedPermanently(context.DeadlineExceeded))
}

// A request Cloud refuses for good ends forwarding for the session at once,
// every lane included, even while the session runs: one refused request, not
// a retry, or a bisection of the batch down to the rows Cloud refuses.
func TestCloudForwarderStopsWhenRejected(t *testing.T) {
	t.Parallel()
	for name, failure := range map[string]error{
		"400": errors.New("failed to send logs to http://cloud/v1/logs: 400 Bad Request (body: malformed)"),
		"404": errors.New("failed to send logs to http://cloud/v1/logs: 404 Not Found (body: no such org)"),
		"413": errors.New("failed to send logs to http://cloud/v1/logs: 413 Request Entity Too Large (body: too large)"),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := forwardTestStore(t)
			spans, logs := &forwardTestExporter{}, &forwardTestExporter{}
			// Hold the span lane in retries so it outlives the log lane's
			// refusal: the refusal must end it too.
			spans.mode.Store(forwardFail)
			logs.reject = func(string) error { return failure }
			f := startForwardTest(t, srv, spans, logs, fastCloudForwardTuning())
			writeForwardTestSpans(t, srv, 0, 10)
			const n = 300
			records := make([]otellog.Record, n)
			for i := range records {
				records[i] = cloudOtherRecord(i)
			}
			writeForwardTestLogs(t, srv, records...)

			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			require.NoError(t, f.waitReleased(ctx), "stops without waiting for the session to end")
			require.ErrorIs(t, context.Cause(f.ctx), errCloudForwardRejected)
			require.EqualValues(t, 1, logs.attempts.Load(), "one refused request")
			require.Empty(t, logs.received())
			require.Empty(t, srv.cloudForwarders.KeepSet(), "the store is released")
		})
	}
}

// Transient failures, a retryable status or a 5xx, never skip a row: the
// lane retries from its cursor until Cloud takes it.
func TestCloudForwarderNeverSkipsOnTransientFailures(t *testing.T) {
	t.Parallel()
	for name, failure := range map[string]error{
		"503":     errors.New("traces export: context deadline exceeded: retry-able request failure: body: unavailable"),
		"500":     errors.New("traces export: failed to send to http://cloud/v1/traces: 500 Internal Server Error (body: oops)"),
		"408":     errors.New("traces export: failed to send to http://cloud/v1/traces: 408 Request Timeout (body: slow)"),
		"network": errors.New(`traces export: Post "http://cloud/v1/traces": dial tcp: connection refused`),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := forwardTestStore(t)
			spans, logs := &forwardTestExporter{}, &forwardTestExporter{}
			var outage atomic.Bool
			outage.Store(true)
			spans.reject = func(string) error {
				if outage.Load() {
					return failure
				}
				return nil
			}
			f := startForwardTest(t, srv, spans, logs, fastCloudForwardTuning())
			writeForwardTestSpans(t, srv, 0, 50)
			require.Eventually(t, func() bool { return spans.attempts.Load() >= 30 }, 10*time.Second, time.Millisecond)
			cursor, _ := f.lanes[0].position()
			require.Zero(t, cursor, "nothing is skipped through a transient failure")

			outage.Store(false)
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			require.NoError(t, f.drain(ctx))
			require.Equal(t, forwardTestNames("span", 50), spans.received())
		})
	}
}
