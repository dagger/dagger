package server

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/agentcontrol"
	"github.com/dagger/dagger/engine/clientdb"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// callPayloadMissingTargets reports the route targets whose DB does not hold
// the digest yet, in route order. Test-only: production code never needs to
// observe delivery state outside the claim/take/settle transitions.
func (sess *daggerSession) callPayloadMissingTargets(digest string, targets []string) []string {
	sess.callPayloadMu.Lock()
	defer sess.callPayloadMu.Unlock()

	states := sess.callPayloadStates(digest, false)
	missing := make([]string, 0, len(targets))
	for _, target := range targets {
		if states[target] != callPayloadDelivered {
			missing = append(missing, target)
		}
	}
	return missing
}

func TestArchiveRegistrationFailureDoesNotDropControl(t *testing.T) {
	srv, _, _, _ := archiveFixture(t) //nolint:dogsled // This test creates its own session and control records.
	other := &daggerSession{sessionID: "other-session", mainClientCallerID: "other", clientRecords: map[string]*clientRecord{}}
	other.telemetryPubSub = NewPubSub(srv)
	other.archiveRegisterErr = fmt.Errorf("injected archive registration failure")
	other.clientRecords["other"] = &clientRecord{daggerSession: other, clientID: "other"}
	a := archiveAgent()
	a.Session = other.sessionID
	rec := controlTestRecord(t, a.Record())
	rec.AddAttributes(otellog.String(telemetryattrs.TelemetryOriginClientIDAttr, "other"))
	exp := sessionLogExporter{sess: other, ps: other.telemetryPubSub}
	require.NoError(t, exp.Export(t.Context(), []sdklog.Record{rec}))
	db, err := srv.clientDBs.Open(t.Context(), "other")
	require.NoError(t, err)
	defer db.Close()
	rows, err := db.SelectLogsSince(t.Context(), clientdb.SelectLogsSinceParams{Limit: 10})
	require.NoError(t, err)
	require.Len(t, rows, 1, "archive registration must not gate live persistence")
}

// callSpan is an ended span carrying the call frame body as dagger.io/dag.call,
// routed from origin.
func callSpan(t *testing.T, origin string, spanID byte, body []byte, digest string) sdktrace.ReadOnlySpan {
	t.Helper()
	attrs := []attribute.KeyValue{
		attribute.String(telemetry.DagDigestAttr, digest),
		attribute.String(telemetry.DagCallAttr, base64.StdEncoding.EncodeToString(body)),
	}
	if origin != "" {
		attrs = append(attrs, attribute.String(telemetryattrs.TelemetryOriginClientIDAttr, origin))
	}
	now := time.Now()
	return tracetest.SpanStub{
		Name: "Thing.lookup",
		SpanContext: trace.NewSpanContext(trace.SpanContextConfig{
			TraceID: trace.TraceID{1},
			SpanID:  trace.SpanID{spanID},
		}),
		StartTime:  now,
		EndTime:    now.Add(time.Millisecond),
		Attributes: attrs,
	}.Snapshot()
}

func clientRows(t *testing.T, dbs *clientdb.DBs, client string) (spans []clientdb.Span, logs []clientdb.Log) {
	t.Helper()
	db, err := dbs.Open(t.Context(), client)
	require.NoError(t, err)
	defer db.Close()
	spans, err = db.Read().SelectSpansSince(t.Context(), clientdb.SelectSpansSinceParams{Limit: 100})
	require.NoError(t, err)
	logs, err = db.Read().SelectLogsSince(t.Context(), clientdb.SelectLogsSinceParams{Limit: 100})
	require.NoError(t, err)
	return spans, logs
}

// A control record must not land ahead of the call frames queued before it:
// the call lanes are drained first, and a drain failure only warns.
func TestControlAfterCallsExporterDrainsCallLanesFirst(t *testing.T) {
	var order []string
	next := &orderedLogExporter{order: &order}
	exp := controlAfterCallsExporter{next: next, calls: []func(context.Context) error{
		func(context.Context) error { order = append(order, "call spans"); return nil },
		func(context.Context) error { order = append(order, "call payloads"); return errors.New("dropped") },
	}}
	require.NoError(t, exp.Export(t.Context(), []sdklog.Record{{}}))
	require.Equal(t, []string{"call spans", "call payloads", "controls"}, order)
}

type orderedLogExporter struct{ order *[]string }

func (e *orderedLogExporter) Export(context.Context, []sdklog.Record) error {
	*e.order = append(*e.order, "controls")
	return nil
}
func (*orderedLogExporter) ForceFlush(context.Context) error { return nil }
func (*orderedLogExporter) Shutdown(context.Context) error   { return nil }

// A call span is its frame's delivery: once the span lands in a target's DB,
// the payload counts as delivered there, so no payload log is written for it.
func TestSessionSpanExporterSettlesCallSpanDelivery(t *testing.T) {
	dbs := clientdb.NewDBs(t.TempDir())
	srv := &Server{clientDBs: dbs}
	sess := &daggerSession{clientRecords: map[string]*clientRecord{}}
	sess.clientRecords["parent"] = &clientRecord{daggerSession: sess, clientID: "parent"}
	sess.clientRecords["child"] = &clientRecord{daggerSession: sess, clientID: "child", parentClientIDs: []string{"parent"}}
	ps := NewPubSub(srv)
	body, digest := serverCallPayload(t, "lookup", "spanned")
	store := &callPayloadDeliveryStore{session: sess, targets: []string{"parent", "child"}}
	require.True(t, store.ClaimCallPayload(digest))

	spans := sessionSpanExporter{sess: sess, ps: ps}
	require.NoError(t, spans.ExportSpans(t.Context(), []sdktrace.ReadOnlySpan{callSpan(t, "child", 1, body, digest)}))
	require.Empty(t, sess.callPayloadMissingTargets(digest, store.targets))
	require.False(t, store.ClaimCallPayload(digest))

	// A later payload record for the same frame (a re-posted copy, a racing
	// walk) finds every target delivered.
	record := scopedLogRecord(t, "test.core", otellog.BytesValue(body),
		otellog.String(telemetryattrs.TelemetryOriginClientIDAttr, "child"),
		otellog.String(telemetry.ContentTypeAttr, telemetryattrs.CallPayloadContentType))
	require.NoError(t, sessionLogExporter{sess: sess, ps: ps}.Export(t.Context(), []sdklog.Record{record}))

	// Another snapshot of the span is still written: it is also the span.
	require.NoError(t, spans.ExportSpans(t.Context(), []sdktrace.ReadOnlySpan{callSpan(t, "child", 1, body, digest)}))
	for _, target := range store.targets {
		spanRows, logRows := clientRows(t, dbs, target)
		require.Len(t, spanRows, 2, "target %s", target)
		require.Empty(t, logRows, "target %s", target)
	}
}

// A failed span write releases the frame's targets, so the protected span
// processor's retry, or a later closure walk, can still deliver it.
func TestSessionSpanExporterReleasesFailedCallSpan(t *testing.T) {
	root := filepath.Join(t.TempDir(), "store")
	require.NoError(t, os.WriteFile(root, []byte("temporarily unavailable"), 0600))
	dbs := clientdb.NewDBs(root)
	srv := &Server{clientDBs: dbs}
	sess := &daggerSession{clientRecords: map[string]*clientRecord{}}
	sess.clientRecords["client"] = &clientRecord{daggerSession: sess, clientID: "client"}
	spans := sessionSpanExporter{sess: sess, ps: NewPubSub(srv)}
	body, digest := serverCallPayload(t, "lookup", "retry")
	store := &callPayloadDeliveryStore{session: sess, targets: []string{"client"}}
	require.True(t, store.ClaimCallPayload(digest))

	span := callSpan(t, "client", 1, body, digest)
	require.Error(t, spans.ExportSpans(t.Context(), []sdktrace.ReadOnlySpan{span}))
	require.Equal(t, []string{"client"}, sess.callPayloadMissingTargets(digest, store.targets))
	require.True(t, store.ClaimCallPayload(digest), "a failed write must release the producer's claim")

	require.NoError(t, os.Remove(root))
	require.NoError(t, spans.ExportSpans(t.Context(), []sdktrace.ReadOnlySpan{span}))
	require.Empty(t, sess.callPayloadMissingTargets(digest, store.targets))
	require.False(t, store.ClaimCallPayload(digest))
}

// A span that cannot be routed must not poison its batch: the protected call
// span processor would otherwise retry it and then drop its siblings too.
func TestSessionSpanExporterSkipsUnroutableSpans(t *testing.T) {
	dbs := clientdb.NewDBs(t.TempDir())
	srv := &Server{clientDBs: dbs}
	sess := &daggerSession{clientRecords: map[string]*clientRecord{}}
	sess.clientRecords["client"] = &clientRecord{daggerSession: sess, clientID: "client"}
	spans := sessionSpanExporter{sess: sess, ps: NewPubSub(srv)}
	body, digest := serverCallPayload(t, "lookup", "routable")
	strayBody, strayDigest := serverCallPayload(t, "lookup", "stray")

	require.NoError(t, spans.ExportSpans(t.Context(), []sdktrace.ReadOnlySpan{
		callSpan(t, "", 1, strayBody, strayDigest),
		callSpan(t, "client", 2, body, digest),
		callSpan(t, "nobody", 3, strayBody, strayDigest),
	}))
	require.Empty(t, sess.callPayloadMissingTargets(digest, []string{"client"}))
	require.Equal(t, []string{"client"}, sess.callPayloadMissingTargets(strayDigest, []string{"client"}))
	spanRows, _ := clientRows(t, dbs, "client")
	require.Len(t, spanRows, 1)
}

func TestSessionLogExporterRetriesPayloadAfterStoreFailure(t *testing.T) {
	root := filepath.Join(t.TempDir(), "store")
	require.NoError(t, os.WriteFile(root, []byte("temporarily unavailable"), 0600))
	dbs := clientdb.NewDBs(root)
	srv := &Server{clientDBs: dbs}
	sess := &daggerSession{clientRecords: map[string]*clientRecord{}}
	sess.clientRecords["client"] = &clientRecord{daggerSession: sess, clientID: "client"}
	exporter := sessionLogExporter{sess: sess, ps: NewPubSub(srv)}
	body, _ := serverCallPayload(t, "lookup", "retry")
	record := scopedLogRecord(t, "test.core", otellog.BytesValue(body),
		otellog.String(telemetryattrs.TelemetryOriginClientIDAttr, "client"),
		otellog.String(telemetry.ContentTypeAttr, telemetryattrs.CallPayloadContentType))
	require.Error(t, exporter.Export(t.Context(), []sdklog.Record{record}))
	require.NoError(t, os.Remove(root))
	require.NoError(t, exporter.Export(t.Context(), []sdklog.Record{record}))
	db, err := dbs.Open(t.Context(), "client")
	require.NoError(t, err)
	defer db.Close()
	rows, err := db.Read().SelectLogsSince(t.Context(), clientdb.SelectLogsSinceParams{Limit: 100})
	require.NoError(t, err)
	require.Len(t, rows, 1, "retry after storage recovery must persist the payload")
}

func TestSessionLogExporterRetriesOnlyFailedPayloadTargets(t *testing.T) {
	root := t.TempDir()
	blocked := filepath.Join(root, "parent.logs.log")
	require.NoError(t, os.Mkdir(blocked, 0700))
	dbs := clientdb.NewDBs(root)
	srv := &Server{clientDBs: dbs}
	sess := &daggerSession{clientRecords: map[string]*clientRecord{}}
	sess.clientRecords["parent"] = &clientRecord{daggerSession: sess, clientID: "parent"}
	sess.clientRecords["child"] = &clientRecord{daggerSession: sess, clientID: "child", parentClientIDs: []string{"parent"}}
	exporter := sessionLogExporter{sess: sess, ps: NewPubSub(srv)}
	body, digest := serverCallPayload(t, "lookup", "retry")
	record := scopedLogRecord(t, "test.core", otellog.BytesValue(body),
		otellog.String(telemetryattrs.TelemetryOriginClientIDAttr, "child"),
		otellog.String(telemetry.ContentTypeAttr, telemetryattrs.CallPayloadContentType))
	require.Error(t, exporter.Export(t.Context(), []sdklog.Record{record}))
	require.Equal(t, []string{"parent"}, sess.callPayloadMissingTargets(digest, []string{"child", "parent"}))
	require.NoError(t, os.Remove(blocked))

	// The payload processor retries a failed batch, and a fresh closure walk
	// may re-emit the same digest in the meantime; several such exports can
	// overlap. The successful child must not get a duplicate, and the parent
	// must receive exactly one.
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			if err := exporter.Export(t.Context(), []sdklog.Record{record, record}); err != nil {
				t.Errorf("retry export: %v", err)
			}
		})
	}
	wg.Wait()
	require.Empty(t, sess.callPayloadMissingTargets(digest, []string{"child", "parent"}))
	for _, target := range []string{"child", "parent"} {
		db, err := dbs.Open(t.Context(), target)
		require.NoError(t, err)
		rows, err := db.Read().SelectLogsSince(t.Context(), clientdb.SelectLogsSinceParams{Limit: 100})
		require.NoError(t, err)
		require.NoError(t, db.Close())
		require.Len(t, rows, 1, "target %s", target)
	}
}

// A record that cannot be routed (no origin, or an origin the session does
// not know) must not poison its batch: the routable payloads still land, and
// nothing is left claimed on their behalf.
func TestSessionLogExporterSkipsUnroutableRecords(t *testing.T) {
	dbs := clientdb.NewDBs(t.TempDir())
	srv := &Server{clientDBs: dbs}
	sess := &daggerSession{clientRecords: map[string]*clientRecord{}}
	sess.clientRecords["client"] = &clientRecord{daggerSession: sess, clientID: "client"}
	exporter := sessionLogExporter{sess: sess, ps: NewPubSub(srv)}
	body, digest := serverCallPayload(t, "lookup", "retry")
	record := scopedLogRecord(t, "test.core", otellog.BytesValue(body),
		otellog.String(telemetryattrs.TelemetryOriginClientIDAttr, "client"),
		otellog.String(telemetry.ContentTypeAttr, telemetryattrs.CallPayloadContentType))
	strayBody, strayDigest := serverCallPayload(t, "lookup", "stray")
	originless := scopedLogRecord(t, "test.core", otellog.BytesValue(strayBody),
		otellog.String(telemetry.ContentTypeAttr, telemetryattrs.CallPayloadContentType))
	unknownOrigin := scopedLogRecord(t, "test.core", otellog.BytesValue(strayBody),
		otellog.String(telemetryattrs.TelemetryOriginClientIDAttr, "nobody"),
		otellog.String(telemetry.ContentTypeAttr, telemetryattrs.CallPayloadContentType))

	store := &callPayloadDeliveryStore{session: sess, targets: []string{"client"}}
	require.True(t, store.ClaimCallPayload(digest))
	require.NoError(t, exporter.Export(t.Context(), []sdklog.Record{originless, record, unknownOrigin, {}}),
		"unroutable records are skipped rather than failing the batch")
	require.Empty(t, sess.callPayloadMissingTargets(digest, []string{"client"}),
		"the routable payload in the same batch must be delivered")
	require.False(t, store.ClaimCallPayload(digest))
	require.Equal(t, []string{"client"}, sess.callPayloadMissingTargets(strayDigest, []string{"client"}))
	require.True(t, store.ClaimCallPayload(strayDigest),
		"a skipped record must not leave its digest claimed or delivered")

	db, err := dbs.Open(t.Context(), "client")
	require.NoError(t, err)
	defer db.Close()
	rows, err := db.Read().SelectLogsSince(t.Context(), clientdb.SelectLogsSinceParams{Limit: 100})
	require.NoError(t, err)
	require.Len(t, rows, 1)
}

// A control record that can never persist (malformed, outside its emission
// session/trace, or unroutable) is skipped on its own: failing the batch
// would have the control processor retry and then drop the valid revisions
// batched with it.
func TestSessionLogExporterSkipsInvalidControlRecords(t *testing.T) {
	dbs := clientdb.NewDBs(t.TempDir())
	srv := &Server{clientDBs: dbs}
	sess := &daggerSession{sessionID: "session", clientRecords: map[string]*clientRecord{}}
	sess.telemetryPubSub = NewPubSub(srv)
	sess.clientRecords["client"] = &clientRecord{daggerSession: sess, clientID: "client"}
	exporter := sessionLogExporter{sess: sess, ps: sess.telemetryPubSub}
	withOrigin := func(rec sdklog.Record, origin string) sdklog.Record {
		rec.AddAttributes(otellog.String(telemetryattrs.TelemetryOriginClientIDAttr, origin))
		return rec
	}

	valid := withOrigin(controlTestRecord(t, archiveAgent().Record()), "client")
	var bad otellog.Record
	bad.SetBody(otellog.StringValue(""))
	bad.AddAttributes(otellog.Int(agentcontrol.VersionAttr, agentcontrol.Version+1))
	malformed := withOrigin(controlTestRecord(t, bad), "client")
	stranger := archiveAgent()
	stranger.Session = "another-session"
	stranger.Handle = "stranger"
	foreign := withOrigin(controlTestRecord(t, stranger.Record()), "client")
	originless := controlTestRecord(t, archiveAgent().Record())
	unroutable := withOrigin(controlTestRecord(t, archiveAgent().Record()), "nobody")

	require.NoError(t, exporter.Export(t.Context(), []sdklog.Record{malformed, foreign, valid, originless, unroutable}),
		"invalid control records are skipped rather than failing the batch")

	db, err := dbs.Open(t.Context(), "client")
	require.NoError(t, err)
	defer db.Close()
	rows, err := db.Read().SelectLogsSince(t.Context(), clientdb.SelectLogsSinceParams{Limit: 100})
	require.NoError(t, err)
	require.Len(t, rows, 1, "only the valid control record persists")
}

// Call payloads ride almost every log batch, so they must not force the
// in-memory tail to the file (that would push live readers onto file scans);
// only rare agent control rows, which a killed engine's unsealed archive
// restores from, pay for a flush.
func TestClientLogsFlushesOnlyForControlRecords(t *testing.T) {
	dbs := clientdb.NewDBs(t.TempDir())
	ps := NewPubSub(&Server{clientDBs: dbs})
	// Hold a reference so the exporter's Close does not spill the tail.
	db, err := dbs.Open(t.Context(), "client")
	require.NoError(t, err)
	defer db.Close()
	tailRows := func() int {
		stats, err := db.AppendLogs(nil)
		require.NoError(t, err)
		return stats.SpillLagRows
	}

	body, _ := serverCallPayload(t, "lookup", "hot-path")
	payload := scopedLogRecord(t, "test.core", otellog.BytesValue(body),
		otellog.String(telemetry.ContentTypeAttr, telemetryattrs.CallPayloadContentType))
	require.NoError(t, ps.Logs("client").Export(t.Context(), []sdklog.Record{payload}))
	require.Equal(t, 1, tailRows(), "a payload-only batch must stay in the in-memory tail")

	control := controlTestRecord(t, archiveAgent().Record())
	require.NoError(t, ps.Logs("client").Export(t.Context(), []sdklog.Record{control}))
	require.Zero(t, tailRows(), "a control batch must write the tail to the file")

	rows, err := db.Read().SelectLogsSince(t.Context(), clientdb.SelectLogsSinceParams{Limit: 100})
	require.NoError(t, err)
	require.Len(t, rows, 2)
}

// A payload the producer claimed but whose write failed must be claimable
// again, so that once the payload processor gives up on the record a later
// closure walk can repair the gap.
func TestSessionLogExporterReleasesFailedPayloadClaims(t *testing.T) {
	root := filepath.Join(t.TempDir(), "store")
	require.NoError(t, os.WriteFile(root, []byte("temporarily unavailable"), 0600))
	dbs := clientdb.NewDBs(root)
	srv := &Server{clientDBs: dbs}
	sess := &daggerSession{clientRecords: map[string]*clientRecord{}}
	sess.clientRecords["client"] = &clientRecord{daggerSession: sess, clientID: "client"}
	exporter := sessionLogExporter{sess: sess, ps: NewPubSub(srv)}
	body, digest := serverCallPayload(t, "lookup", "retry")
	record := scopedLogRecord(t, "test.core", otellog.BytesValue(body),
		otellog.String(telemetryattrs.TelemetryOriginClientIDAttr, "client"),
		otellog.String(telemetry.ContentTypeAttr, telemetryattrs.CallPayloadContentType))

	store := &callPayloadDeliveryStore{session: sess, targets: []string{"client"}}
	require.True(t, store.ClaimCallPayload(digest))
	require.Error(t, exporter.Export(t.Context(), []sdklog.Record{record}))
	require.True(t, store.ClaimCallPayload(digest),
		"a failed write must release the producer's claim")
	require.NoError(t, os.Remove(root))
	require.NoError(t, exporter.Export(t.Context(), []sdklog.Record{record}))
	require.False(t, store.ClaimCallPayload(digest))
}

// When a protected lane gives up on a payload, the targets its final attempt
// failed are lost, so replays on their route walk their closures again; a
// failure the lane still retries loses nothing.
func TestSessionExportersRecordPayloadsTheProcessorsDrop(t *testing.T) {
	root := filepath.Join(t.TempDir(), "store")
	require.NoError(t, os.WriteFile(root, []byte("unavailable"), 0600))
	dbs := clientdb.NewDBs(root)
	srv := &Server{clientDBs: dbs}
	sess := &daggerSession{clientRecords: map[string]*clientRecord{}}
	sess.clientRecords["client"] = &clientRecord{daggerSession: sess, clientID: "client"}
	ps := NewPubSub(srv)
	logExporter := sessionLogExporter{sess: sess, ps: ps}
	store := &callPayloadDeliveryStore{session: sess, targets: []string{"client"}}

	logBody, logDigest := serverCallPayload(t, "lookup", "log")
	spanBody, spanDigest := serverCallPayload(t, "lookup", "span")
	record := scopedLogRecord(t, "test.core", otellog.BytesValue(logBody),
		otellog.String(telemetryattrs.TelemetryOriginClientIDAttr, "client"),
		otellog.String(telemetry.ContentTypeAttr, telemetryattrs.CallPayloadContentType))
	require.True(t, store.ClaimCallPayload(logDigest))
	require.True(t, store.ClaimCallPayload(spanDigest))

	require.Error(t, logExporter.Export(t.Context(), []sdklog.Record{record}))
	require.Zero(t, sess.callPayloadLostCount.Load(), "a failure outside a final attempt loses nothing")
	require.False(t, store.StartCallPayloadRepair("xxh3:root"))
	require.True(t, store.ClaimCallPayload(logDigest))

	logs := enginetel.NewCallPayloadBatchProcessor(logExporter)
	spans := enginetel.NewCallSpanProcessor(sessionSpanExporter{sess: sess, ps: ps})
	require.NoError(t, logs.OnEmit(t.Context(), &record))
	spans.OnEnd(callSpan(t, "client", 1, spanBody, spanDigest))
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	var logErr, spanErr error
	wg.Go(func() { logErr = logs.ForceFlush(ctx) })
	wg.Go(func() { spanErr = spans.ForceFlush(ctx) })
	wg.Wait()
	require.ErrorContains(t, logErr, "dropping 1 protected records")
	require.ErrorContains(t, spanErr, "dropping 1 protected spans")

	require.EqualValues(t, 2, sess.callPayloadLostCount.Load())
	require.True(t, store.StartCallPayloadRepair("xxh3:root"),
		"a replay of an already claimed root must walk again")
	require.True(t, store.ClaimCallPayload(logDigest), "a repair walk can claim the lost payload")
	require.True(t, store.ClaimCallPayload(spanDigest))
	require.Zero(t, sess.callPayloadLostCount.Load())

	require.ErrorContains(t, logs.Shutdown(ctx), "dropping 1 protected records")
	require.ErrorContains(t, spans.Shutdown(ctx), "dropping 1 protected spans")
}
