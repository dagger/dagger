package daggercmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	otlplogsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	otlpresourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/archive"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/dagger/dagger/internal/tracesource"
	"github.com/dagger/dagger/util/cleanups"
)

type liveLogRow struct {
	span   uint16
	body   string
	digest string
	// id is the row's ID in the store (telemetryattrs.LogRowAttr); none
	// when 0.
	id int64
}

type liveLogBatch struct {
	rows   []liveLogRow
	cursor string
}

// fakeLogReader answers each query with the batches respond picks, and keeps
// a follow open until it is cancelled.
type fakeLogReader struct {
	respond func(tracesource.LogQuery) []liveLogBatch

	mu      sync.Mutex
	queries []tracesource.LogQuery
}

func (f *fakeLogReader) ReadLogs(ctx context.Context, _ string, q tracesource.LogQuery, cb func(context.Context, *collogspb.ExportLogsServiceRequest, string) error) error {
	slices.SortFunc(q.Spans, func(a, b archive.LogSpan) int { return compareStrings(a.SpanID, b.SpanID) })
	f.mu.Lock()
	f.queries = append(f.queries, q)
	f.mu.Unlock()
	for _, b := range f.respond(q) {
		if err := cb(ctx, liveLogRequest(b.rows), b.cursor); err != nil {
			return err
		}
	}
	if q.Follow {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

func compareStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func (f *fakeLogReader) seen() []tracesource.LogQuery {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.queries)
}

func liveLogRequest(rows []liveLogRow) *collogspb.ExportLogsServiceRequest {
	var records []*otlplogsv1.LogRecord
	for _, r := range rows {
		rec := &otlplogsv1.LogRecord{
			TraceId: make([]byte, 16),
			SpanId:  []byte{0, 0, 0, 0, 0, 0, byte(r.span >> 8), byte(r.span)},
			Body:    &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_StringValue{StringValue: r.body}},
		}
		rec.TraceId[15] = 1
		if r.digest != "" {
			rec.Attributes = append(rec.Attributes, &otlpcommonv1.KeyValue{
				Key:   telemetry.DagDigestAttr,
				Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_StringValue{StringValue: r.digest}},
			})
		}
		if r.id > 0 {
			rec.Attributes = append(rec.Attributes, &otlpcommonv1.KeyValue{
				Key:   telemetryattrs.LogRowAttr,
				Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_IntValue{IntValue: r.id}},
			})
		}
		records = append(records, rec)
	}
	return &collogspb.ExportLogsServiceRequest{ResourceLogs: []*otlplogsv1.ResourceLogs{{
		Resource:  &otlpresourcev1.Resource{},
		ScopeLogs: []*otlplogsv1.ScopeLogs{{Scope: &otlpcommonv1.InstrumentationScope{}, LogRecords: records}},
	}}}
}

// bodyCollector collects the bodies of the records exported to it, after
// the batch's check (idtui.LiveLogsFilter), as the frontend applies it,
// placing each record in its own span -- but for a digest's, held back as
// if its creator were unknown.
type bodyCollector struct {
	mu     sync.Mutex
	bodies []string
}

func (c *bodyCollector) Export(ctx context.Context, records []sdklog.Record) error {
	if filter := idtui.LiveLogsFilter(ctx); filter != nil {
		records = filter(slices.Clone(records), func(rec sdklog.Record) idtui.LogPlacement {
			if key, _ := logOwnerOfRecord(rec); key.digest != "" {
				return idtui.LogPlacement{}
			}
			return idtui.LogPlacement{Span: dagui.SpanID{SpanID: rec.SpanID()}}
		})
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, rec := range records {
		c.bodies = append(c.bodies, rec.Body().AsString())
	}
	return nil
}

func (c *bodyCollector) got() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.bodies)
}

func (c *bodyCollector) Shutdown(context.Context) error   { return nil }
func (c *bodyCollector) ForceFlush(context.Context) error { return nil }

func liveTestSpan(n uint16) dagui.SpanID {
	var id dagui.SpanID
	id.SpanID[6] = byte(n >> 8)
	id.SpanID[7] = byte(n)
	return id
}

// TestLiveLogsFollowCatchesUpJoiningSpans: the set is read to the store's
// end, then followed; a span joining it is caught up to the follow's cursor
// -- importing none of what the follow already imported -- and the grown set
// is read and followed from that cursor.
func TestLiveLogsFollowCatchesUpJoiningSpans(t *testing.T) {
	parent, child := liveTestSpan(1), liveTestSpan(2)
	reader := &fakeLogReader{respond: func(q tracesource.LogQuery) []liveLogBatch {
		switch {
		case !q.Follow && q.Through == "" && q.Cursor == "":
			// The parent rolls up its child: the read takes both.
			return []liveLogBatch{{rows: []liveLogRow{{span: 1, body: "p1"}, {span: 2, body: "c1"}}, cursor: "3"}}
		case !q.Follow && q.Through == "3":
			// The catch-up re-reads the child's rows the follow read, and
			// a digest-routed row only the child selects.
			return []liveLogBatch{{rows: []liveLogRow{{span: 2, body: "c1"}, {span: 9, body: "svc", digest: "xxh3:out"}}, cursor: "3"}}
		case !q.Follow && q.Cursor == "3" && len(q.Spans) == 2:
			return []liveLogBatch{{rows: []liveLogRow{{span: 2, body: "c2"}}, cursor: "5"}}
		}
		return nil
	}}
	out := &bodyCollector{}
	l := newLiveLogs(reader, "trace", out, true)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); l.run(ctx) }()
	defer func() { cancel(); <-done }()

	l.subscribe(idtui.LogRequest{Span: parent, Descendants: true})
	require.Eventually(t, func() bool { return len(out.got()) == 2 }, 5*time.Second, 10*time.Millisecond)
	// Join once the parent is followed.
	require.Eventually(t, func() bool {
		seen := reader.seen()
		return len(seen) > 0 && seen[len(seen)-1].Follow
	}, 5*time.Second, 10*time.Millisecond)
	l.subscribe(idtui.LogRequest{Span: child, Digests: []string{"xxh3:out"}})
	require.Eventually(t, func() bool { return len(out.got()) == 4 }, 5*time.Second, 10*time.Millisecond)
	require.Equal(t, []string{"p1", "c1", "svc", "c2"}, out.got(), "every row once, in order")

	parentOnly := []archive.LogSpan{{SpanID: parent.String(), Descendants: true}}
	both := []archive.LogSpan{{SpanID: parent.String(), Descendants: true}, {SpanID: child.String(), Digests: []string{"xxh3:out"}}}
	var seen []tracesource.LogQuery
	require.Eventually(t, func() bool {
		seen = reader.seen()
		return len(seen) > 0 && seen[len(seen)-1].Follow && len(seen[len(seen)-1].Spans) == 2
	}, 5*time.Second, 10*time.Millisecond)
	// The parent is read, then followed (a restart may repeat both).
	require.Equal(t, tracesource.LogQuery{Spans: parentOnly}, seen[0])
	require.Equal(t, tracesource.LogQuery{Spans: parentOnly, Follow: true, Cursor: "3"}, seen[1])
	join := slices.IndexFunc(seen, func(q tracesource.LogQuery) bool { return q.Through != "" })
	require.Equal(t, []tracesource.LogQuery{
		// The newcomer is caught up to the follow's cursor.
		{Spans: []archive.LogSpan{{SpanID: child.String(), Digests: []string{"xxh3:out"}}}, Through: "3"},
		// The grown set is read from the cursor, then followed.
		{Spans: both, Cursor: "3"},
		{Spans: both, Follow: true, Cursor: "5"},
	}, seen[join:])
}

// TestLiveLogsUnknownSpansImportedOnce: a follow over a subtree also returns
// rows of a span the engine has not placed yet, then stops once it is placed
// elsewhere. Those rows are imported without claiming the span, so rendering
// it later catches up all its rows rather than only the ones the follow saw
// -- skipping, by their row IDs, the ones the follow imported; rows the
// frontend did not ask for are dropped.
func TestLiveLogsUnknownSpansImportedOnce(t *testing.T) {
	reader := &fakeLogReader{respond: func(q tracesource.LogQuery) []liveLogBatch {
		switch {
		case q.Follow && q.Cursor == "":
			return []liveLogBatch{{rows: []liveLogRow{{span: 1, body: "p1", id: 1}, {span: 9, body: "u1", id: 2}, {span: 7, body: "noise", id: 3}}, cursor: "3"}}
		case q.Through == "3":
			return []liveLogBatch{{rows: []liveLogRow{{span: 9, body: "u1", id: 2}, {span: 9, body: "u2", id: 4}}, cursor: "3"}}
		}
		return nil
	}}
	out := &bodyCollector{}
	l := newLiveLogs(reader, "trace", out, true)
	var mu sync.Mutex
	requested := map[byte]bool{}
	l.coverage = func(targets []idtui.LogTarget) []idtui.LogRoute {
		mu.Lock()
		defer mu.Unlock()
		var got []idtui.LogRoute
		for _, target := range targets {
			switch span := target.Span.SpanID[7]; {
			case requested[span] || span == 1:
				got = append(got, idtui.LogRoute{Coverage: idtui.LogCovered, Span: target.Span})
			case span == 9:
				got = append(got, idtui.LogRoute{Coverage: idtui.LogUnknown})
			default:
				got = append(got, idtui.LogRoute{Coverage: idtui.LogUncovered, Span: target.Span})
			}
		}
		return got
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); l.run(ctx) }()
	defer func() { cancel(); <-done }()

	l.subscribe(idtui.LogRequest{Span: liveTestSpan(1), Descendants: true})
	require.Eventually(t, func() bool { return len(out.got()) == 2 }, 5*time.Second, 10*time.Millisecond)
	mu.Lock()
	requested[9] = true
	mu.Unlock()
	l.subscribe(idtui.LogRequest{Span: liveTestSpan(9)})
	require.Eventually(t, func() bool { return slices.Contains(out.got(), "u2") }, 5*time.Second, 10*time.Millisecond)
	require.Equal(t, []string{"p1", "u1", "u2"}, out.got(), "every row once")
	l.mu.Lock()
	defer l.mu.Unlock()
	require.EqualValues(t, 1, l.deduped)
	require.Empty(t, l.seen, "the catch-up took the row it skipped")
	require.Zero(t, l.seenRows)
}

// rowIDLogs is a reader without a store, whose reads the test imports
// itself (importLogs), routing each span as coverage says: covered spans to
// themselves, unknown ones without a span, as the frontend does before it
// received them.
func rowIDLogs(coverage map[uint16]idtui.LogCoverage) (*liveLogs, *bodyCollector) {
	out := &bodyCollector{}
	l := newLiveLogs(nil, "trace", out, true)
	l.coverage = func(targets []idtui.LogTarget) []idtui.LogRoute {
		routes := make([]idtui.LogRoute, len(targets))
		for i, target := range targets {
			span := uint16(target.Span.SpanID[6])<<8 | uint16(target.Span.SpanID[7])
			routes[i] = idtui.LogRoute{Coverage: coverage[span], Span: target.Span}
			if coverage[span] == idtui.LogUnknown {
				routes[i].Span = dagui.SpanID{}
			}
		}
		return routes
	}
	return l, out
}

// importRows imports rows as the read numbered read does (0 for the
// follow).
func importRows(t *testing.T, l *liveLogs, read uint64, rows ...liveLogRow) {
	t.Helper()
	require.NoError(t, l.importLogs(t.Context(), liveLogRequest(rows), read, l.beginRead()))
}

// TestLiveLogsUnclaimedReplayOnce: a roll-up's catch-up returns rows of a
// span still not placed that the follow imported, unclaimed both times:
// each is imported once. A read claiming the pair once it is placed imports
// only the rows it did not.
func TestLiveLogsUnclaimedReplayOnce(t *testing.T) {
	coverage := map[uint16]idtui.LogCoverage{9: idtui.LogUnknown}
	l, out := rowIDLogs(coverage)
	importRows(t, l, 0, liveLogRow{span: 9, body: "u1", id: 1}, liveLogRow{span: 9, body: "u2", id: 3})
	importRows(t, l, 1, liveLogRow{span: 9, body: "u1", id: 1}, liveLogRow{span: 9, body: "u2", id: 3}, liveLogRow{span: 9, body: "u3", id: 4})
	require.Equal(t, []string{"u1", "u2", "u3"}, out.got())
	require.Equal(t, []int64{1, 3, 4}, l.seen[logOwner{span: liveTestSpan(9).SpanID}].ids)

	coverage[9] = idtui.LogCovered
	importRows(t, l, 2, liveLogRow{span: 9, body: "u1", id: 1}, liveLogRow{span: 9, body: "u2", id: 3}, liveLogRow{span: 9, body: "u3", id: 4}, liveLogRow{span: 9, body: "u4", id: 6})
	require.Equal(t, []string{"u1", "u2", "u3", "u4"}, out.got())
	require.EqualValues(t, 5, l.deduped)
	require.Empty(t, l.seen)
	require.Zero(t, l.seenRows)
}

// TestLiveLogsUnclaimedGapCaughtUp: the follow can skip a pair's rows
// between two it imports -- its set lost the roll-up that selected the
// unplaced span, then got it back -- so the catch-up claiming the pair
// imports exactly the row in between.
func TestLiveLogsUnclaimedGapCaughtUp(t *testing.T) {
	coverage := map[uint16]idtui.LogCoverage{9: idtui.LogUnknown}
	l, out := rowIDLogs(coverage)
	importRows(t, l, 0, liveLogRow{span: 9, body: "u1", id: 1})
	importRows(t, l, 0, liveLogRow{span: 9, body: "u3", id: 5})
	coverage[9] = idtui.LogCovered
	importRows(t, l, 1, liveLogRow{span: 9, body: "u1", id: 1}, liveLogRow{span: 9, body: "u2", id: 3}, liveLogRow{span: 9, body: "u3", id: 5})
	require.Equal(t, []string{"u1", "u3", "u2"}, out.got(), "every row once")
	require.Empty(t, l.seen)
}

// TestLiveLogsFollowClaimForgetsRows: once the follow claims a pair it
// imported rows of unclaimed, no other read returns them: they are
// forgotten.
func TestLiveLogsFollowClaimForgetsRows(t *testing.T) {
	coverage := map[uint16]idtui.LogCoverage{9: idtui.LogUnknown}
	l, out := rowIDLogs(coverage)
	importRows(t, l, 0, liveLogRow{span: 9, body: "u1", id: 1})
	require.Len(t, l.seen, 1)
	coverage[9] = idtui.LogCovered
	importRows(t, l, 0, liveLogRow{span: 9, body: "u2", id: 2})
	require.Equal(t, []string{"u1", "u2"}, out.got())
	require.Empty(t, l.seen)
	require.Zero(t, l.seenRows)
}

// TestLiveLogsFollowDuringCatchUp: the follow imports rows of a pair a
// catch-up claimed while the catch-up is still returning its older rows --
// a reply streaming on -- so the rows held stay held until the catch-up
// returns them.
func TestLiveLogsFollowDuringCatchUp(t *testing.T) {
	coverage := map[uint16]idtui.LogCoverage{9: idtui.LogUnknown}
	l, out := rowIDLogs(coverage)
	importRows(t, l, 0, liveLogRow{span: 9, body: "u5", id: 5})
	coverage[9] = idtui.LogCovered
	importRows(t, l, 1, liveLogRow{span: 9, body: "u2", id: 2})
	importRows(t, l, 0, liveLogRow{span: 9, body: "u7", id: 7})
	importRows(t, l, 1, liveLogRow{span: 9, body: "u5", id: 5})
	require.Equal(t, []string{"u5", "u2", "u7"}, out.got(), "every row once")
	require.EqualValues(t, 1, l.deduped)
	require.Empty(t, l.seen)
}

// TestLiveLogsReleaseForgetsRows: the rows the frontend dropped are
// forgotten with it -- those of a pair routing to a span it dropped, and of
// a digest whose route it could not tell -- so the next read imports them
// again; the others stay, and so does everything when it drops nothing.
func TestLiveLogsReleaseForgetsRows(t *testing.T) {
	coverage := map[uint16]idtui.LogCoverage{7: idtui.LogUnknown, 8: idtui.LogUnknown, 9: idtui.LogUnknown}
	l, out := rowIDLogs(coverage)
	importRows(t, l, 0,
		liveLogRow{span: 7, body: "kept", id: 1},
		liveLogRow{span: 8, body: "svc", digest: "xxh3:svc", id: 2},
		liveLogRow{span: 9, body: "u1", id: 3},
	)
	require.Len(t, l.seen, 3)
	l.release(idtui.LogRelease{Spans: []dagui.SpanID{liveTestSpan(9)}})
	require.Len(t, l.seen, 3, "released without dropping its text")
	l.release(idtui.LogRelease{Spans: []dagui.SpanID{liveTestSpan(9)}, Dropped: []dagui.SpanID{liveTestSpan(9)}})
	require.Equal(t, []logOwner{{span: liveTestSpan(7).SpanID}}, slices.Collect(maps.Keys(l.seen)))
	require.Equal(t, 1, l.seenRows)
	require.Equal(t, 1, l.seenOrder.Len())

	coverage[9] = idtui.LogCovered
	importRows(t, l, 1, liveLogRow{span: 9, body: "u1", id: 3}, liveLogRow{span: 9, body: "u2", id: 4})
	importRows(t, l, 2, liveLogRow{span: 7, body: "kept", id: 1})
	require.Equal(t, []string{"kept", "svc", "u1", "u1", "u2"}, out.got(), "a dropped row is imported again")
}

// TestLiveLogsQueuedUnclaimedAfterRelease: an unclaimed batch the frontend
// applies only after it dropped the span -- recreating its text -- is
// recorded then, so the read claiming the pair skips it.
func TestLiveLogsQueuedUnclaimedAfterRelease(t *testing.T) {
	coverage := map[uint16]idtui.LogCoverage{9: idtui.LogUnknown}
	l, out := rowIDLogs(coverage)
	var queued []func() error
	l.export = exporterFunc(func(ctx context.Context, records []sdklog.Record) error {
		records = slices.Clone(records)
		queued = append(queued, func() error { return out.Export(ctx, records) })
		return nil
	})
	importRows(t, l, 0, liveLogRow{span: 9, body: "u1", id: 1})
	l.release(idtui.LogRelease{Spans: []dagui.SpanID{liveTestSpan(9)}, Dropped: []dagui.SpanID{liveTestSpan(9)}})
	require.NoError(t, queued[0]())
	coverage[9] = idtui.LogCovered
	importRows(t, l, 1, liveLogRow{span: 9, body: "u1", id: 1}, liveLogRow{span: 9, body: "u2", id: 2})
	require.NoError(t, queued[1]())
	require.Equal(t, []string{"u1", "u2"}, out.got())
}

// TestLiveLogsSeenCap: past the cap, the oldest pairs' rows are forgotten,
// so a read returning them again imports them twice -- a duplicate, never a
// lost row -- while the newer pairs' are still skipped.
func TestLiveLogsSeenCap(t *testing.T) {
	coverage := map[uint16]idtui.LogCoverage{7: idtui.LogUnknown, 8: idtui.LogUnknown, 9: idtui.LogUnknown}
	l, out := rowIDLogs(coverage)
	l.seenMax = 3
	importRows(t, l, 0, liveLogRow{span: 7, body: "a1", id: 1}, liveLogRow{span: 7, body: "a2", id: 2})
	importRows(t, l, 0, liveLogRow{span: 8, body: "b1", id: 3})
	require.Equal(t, 3, l.seenRows)
	importRows(t, l, 0, liveLogRow{span: 9, body: "c1", id: 4})
	require.Equal(t, 2, l.seenRows)
	require.NotContains(t, l.seen, logOwner{span: liveTestSpan(7).SpanID}, "the oldest pair goes first")

	coverage[7], coverage[8], coverage[9] = idtui.LogCovered, idtui.LogCovered, idtui.LogCovered
	importRows(t, l, 1,
		liveLogRow{span: 7, body: "a1", id: 1}, liveLogRow{span: 7, body: "a2", id: 2},
		liveLogRow{span: 8, body: "b1", id: 3}, liveLogRow{span: 9, body: "c1", id: 4})
	require.Equal(t, []string{"a1", "a2", "b1", "c1", "a1", "a2"}, out.got())
	require.Empty(t, l.seen)
	require.Zero(t, l.seenRows)
}

// TestLiveLogsNoRowIDs: records without row IDs (a store that sends none)
// can't be told apart: a row imported unclaimed is imported again by the
// read claiming its pair, as a duplicate rather than a loss.
func TestLiveLogsNoRowIDs(t *testing.T) {
	coverage := map[uint16]idtui.LogCoverage{9: idtui.LogUnknown}
	l, out := rowIDLogs(coverage)
	importRows(t, l, 0, liveLogRow{span: 9, body: "u1"})
	importRows(t, l, 1, liveLogRow{span: 9, body: "u1"})
	coverage[9] = idtui.LogCovered
	importRows(t, l, 2, liveLogRow{span: 9, body: "u1"}, liveLogRow{span: 9, body: "u2"})
	require.Equal(t, []string{"u1", "u1", "u1", "u2"}, out.got())
	require.Empty(t, l.seen)
	require.Zero(t, l.deduped)
}

// TestLiveLogsUnplacedReplyRendersOnce drives the headless frontend: an
// agent's reply starts streaming before the frontend received its span,
// so the follow of the conversation imports its first row unclaimed; once
// the reply is placed and read on its own, its catch-up returns that row
// again, and the reply renders it once.
func TestLiveLogsUnplacedReplyRendersOnce(t *testing.T) {
	for _, withIDs := range []bool{true, false} {
		t.Run(fmt.Sprintf("row IDs %v", withIDs), func(t *testing.T) {
			live := dagui.TraceID{TraceID: trace.TraceID{15: 1}}
			root, reply := liveTestSpan(1), liveTestSpan(2)
			start := time.Unix(100, 0)
			fe := idtui.NewASCIIReporterWithDB(io.Discard, dagui.NewDB())
			l := newLiveLogs(nil, live.String(), nil, true)
			l.coverage = fe.LiveLogsCoverage
			l.export = fe.SetLiveLogs(live, l.subscribe)
			fe.SetLogRelease(live, l.release)
			fe.ImportSnapshots([]dagui.SpanSnapshot{{ID: root, TraceID: live, Name: "agent", StartTime: start, Final: true}})
			fe.SetPrimary(root)
			fe.RequestSurfacedLogs()
			require.NoError(t, l.catchUp(t.Context()))
			row := func(body string, id int64) liveLogRow {
				r := liveLogRow{span: 2, body: body}
				if withIDs {
					r.id = id
				}
				return r
			}
			importRows(t, l, 0, row("Hello there, ", 1))
			fe.ImportSnapshots([]dagui.SpanSnapshot{{ID: reply, TraceID: live, ParentID: root, Name: "reply", LLMRole: "assistant", Message: "received", StartTime: start, Final: true}})
			fe.RequestSurfacedLogs()
			l.mu.Lock()
			l.catchUps++
			read := l.catchUps
			l.mu.Unlock()
			importRows(t, l, read, row("Hello there, ", 1), row("general Kenobi\n", 3))
			var rendered strings.Builder
			require.NoError(t, fe.FinalRender(&rendered))
			text := rendered.String()
			want := 1
			if !withIDs {
				want = 2 // the duplicate this fixes
			}
			require.Equal(t, want, strings.Count(text, "Hello there"), text)
			require.Equal(t, 1, strings.Count(text, "general Kenobi"), text)
		})
	}
}

// TestLiveLogsRollUpPlacedLater drives the headless frontend: the follow
// imports a row before its span arrived, so no roll-up shows it. The span
// then turns out to roll up into a span the frontend requests: the read
// claiming it imports the row again rather than skip it, or the roll-up
// would never show it.
func TestLiveLogsRollUpPlacedLater(t *testing.T) {
	live := dagui.TraceID{TraceID: trace.TraceID{15: 1}}
	root, rollUp, kid := liveTestSpan(1), liveTestSpan(2), liveTestSpan(3)
	start := time.Unix(100, 0)
	fe := idtui.NewASCIIReporterWithDB(io.Discard, dagui.NewDB())
	l := newLiveLogs(nil, live.String(), nil, true)
	l.coverage = fe.LiveLogsCoverage
	l.export = fe.SetLiveLogs(live, l.subscribe)
	fe.ImportSnapshots([]dagui.SpanSnapshot{{ID: root, TraceID: live, Name: "root", StartTime: start, Final: true}})
	fe.SetPrimary(root)
	importRows(t, l, 0, liveLogRow{span: 3, body: "line one\n", id: 1})
	fe.ImportSnapshots([]dagui.SpanSnapshot{
		{ID: rollUp, TraceID: live, ParentID: root, Name: "rollup", RollUpLogs: true, StartTime: start, EndTime: start.Add(time.Second), Final: true},
		{ID: kid, TraceID: live, ParentID: rollUp, Name: "kid", StartTime: start, EndTime: start.Add(time.Second), Final: true},
	})
	fe.ZoomToSpan(rollUp)
	fe.RequestSurfacedLogs()
	require.NoError(t, l.catchUp(t.Context()))
	l.mu.Lock()
	l.catchUps++
	read := l.catchUps
	l.mu.Unlock()
	importRows(t, l, read, liveLogRow{span: 3, body: "line one\n", id: 1}, liveLogRow{span: 3, body: "line two\n", id: 2})
	l.mu.Lock()
	defer l.mu.Unlock()
	require.EqualValues(t, 1, l.replayed, "imported again")
	require.Zero(t, l.deduped)
	require.Empty(t, l.seen)
}

// TestLiveLogsUnplacedToolArgs drives the headless frontend: a tool call's
// arguments stream before the frontend received its span, so the follow's
// first chunk lands in the span's output; once the span arrives, the chunk
// belongs in its arguments, and the read claiming it imports it again
// rather than skip it.
func TestLiveLogsUnplacedToolArgs(t *testing.T) {
	live := dagui.TraceID{TraceID: trace.TraceID{15: 1}}
	root, tool := liveTestSpan(1), liveTestSpan(2)
	start := time.Unix(100, 0)
	fe := idtui.NewASCIIReporterWithDB(io.Discard, dagui.NewDB())
	l := newLiveLogs(nil, live.String(), nil, true)
	l.coverage = fe.LiveLogsCoverage
	l.export = fe.SetLiveLogs(live, l.subscribe)
	fe.ImportSnapshots([]dagui.SpanSnapshot{{ID: root, TraceID: live, Name: "agent", StartTime: start, Final: true}})
	fe.SetPrimary(root)
	fe.RequestSurfacedLogs()
	require.NoError(t, l.catchUp(t.Context()))
	chunk := func(body string, id int64) *collogspb.ExportLogsServiceRequest {
		req := liveLogRequest([]liveLogRow{{span: 2, body: body, id: id}})
		rec := req.ResourceLogs[0].ScopeLogs[0].LogRecords[0]
		rec.Attributes = append(rec.Attributes, &otlpcommonv1.KeyValue{
			Key:   telemetry.ContentTypeAttr,
			Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_StringValue{StringValue: "application/json"}},
		})
		return req
	}
	require.NoError(t, l.importLogs(t.Context(), chunk(`{"path":`, 1), 0, l.beginRead()))
	fe.ImportSnapshots([]dagui.SpanSnapshot{{ID: tool, TraceID: live, ParentID: root, Name: "Read", LLMRole: "assistant", LLMTool: "Read", StartTime: start, Final: true}})
	fe.RequestSurfacedLogs()
	l.mu.Lock()
	l.catchUps++
	read := l.catchUps
	l.mu.Unlock()
	require.NoError(t, l.importLogs(t.Context(), chunk(`{"path":`, 1), read, l.beginRead()))
	l.mu.Lock()
	defer l.mu.Unlock()
	require.EqualValues(t, 1, l.replayed, "imported again into the arguments")
	require.Zero(t, l.deduped)
}

// placedLogs is rowIDLogs whose frontend renders each record where places
// says, by its span (its own span, without a roll-up, when absent).
func placedLogs(coverage map[uint16]idtui.LogCoverage, places map[uint16]idtui.LogPlacement) (*liveLogs, *bodyCollector) {
	l, out := rowIDLogs(coverage)
	l.export = exporterFunc(func(ctx context.Context, records []sdklog.Record) error {
		records = idtui.LiveLogsFilter(ctx)(slices.Clone(records), func(rec sdklog.Record) idtui.LogPlacement {
			span := uint16(rec.SpanID()[6])<<8 | uint16(rec.SpanID()[7])
			if p, ok := places[span]; ok {
				return p
			}
			return idtui.LogPlacement{Span: dagui.SpanID{SpanID: rec.SpanID()}}
		})
		return out.Export(context.Background(), records)
	})
	return l, out
}

// TestLiveLogsRowRenderedElsewhere: a held row the frontend would render
// elsewhere now is imported again -- or where it goes now would never show
// it -- once per place: a service's stdio rendered on its exec span until
// the digest's creator arrived, a row whose span was placed under a roll-up
// since.
func TestLiveLogsRowRenderedElsewhere(t *testing.T) {
	rollUp, creator := liveTestSpan(5), liveTestSpan(6)
	coverage := map[uint16]idtui.LogCoverage{8: idtui.LogUnknown, 9: idtui.LogUnknown}
	places := map[uint16]idtui.LogPlacement{}
	l, out := placedLogs(coverage, places)
	importRows(t, l, 0, liveLogRow{span: 8, body: "svc", digest: "xxh3:svc", id: 1}, liveLogRow{span: 9, body: "kid", id: 2})

	// Unclaimed again, the kid since under a roll-up: shown there once.
	places[9] = idtui.LogPlacement{Span: liveTestSpan(9), RollUp: rollUp}
	importRows(t, l, 1, liveLogRow{span: 9, body: "kid", id: 2})
	importRows(t, l, 2, liveLogRow{span: 9, body: "kid", id: 2})
	require.Equal(t, []string{"svc", "kid", "kid"}, out.got())

	// Claimed: the service's creator arrived; the kid is where it was.
	places[8] = idtui.LogPlacement{Span: creator}
	coverage[8], coverage[9] = idtui.LogCovered, idtui.LogCovered
	importRows(t, l, 3, liveLogRow{span: 8, body: "svc", digest: "xxh3:svc", id: 1}, liveLogRow{span: 9, body: "kid", id: 2}, liveLogRow{span: 9, body: "kid 2", id: 3})
	require.Equal(t, []string{"svc", "kid", "kid", "svc", "kid 2"}, out.got())
	require.EqualValues(t, 2, l.deduped)
	require.EqualValues(t, 2, l.replayed)
	require.Empty(t, l.seen)
}

// TestLiveLogsHeldBackRowPlaced: a digest's row held back while its creator
// was unknown went wherever the frontend resolved it once it could -- the
// creator, or a service's exec span arriving first -- which it does not
// tell: the read claiming it once it is placed imports it again. Held back
// still, it is skipped: the copy held back goes wherever it goes.
func TestLiveLogsHeldBackRowPlaced(t *testing.T) {
	coverage := map[uint16]idtui.LogCoverage{8: idtui.LogUnknown}
	places := map[uint16]idtui.LogPlacement{8: {}}
	l, out := placedLogs(coverage, places)
	importRows(t, l, 0, liveLogRow{span: 8, body: "svc", digest: "xxh3:svc", id: 1})
	importRows(t, l, 1, liveLogRow{span: 8, body: "svc", digest: "xxh3:svc", id: 1})
	require.Equal(t, []string{"svc"}, out.got(), "held back still")
	places[8] = idtui.LogPlacement{Span: liveTestSpan(6)}
	coverage[8] = idtui.LogCovered
	importRows(t, l, 2, liveLogRow{span: 8, body: "svc", digest: "xxh3:svc", id: 1})
	require.Equal(t, []string{"svc", "svc"}, out.got())
	require.EqualValues(t, 1, l.deduped)
	require.EqualValues(t, 1, l.replayed)
}

// TestLiveLogsReadAtFinish: without following (the report), requests only
// accumulate, and finish reads them all at once from the beginning.
func TestLiveLogsReadAtFinish(t *testing.T) {
	reader := &fakeLogReader{respond: func(q tracesource.LogQuery) []liveLogBatch {
		return []liveLogBatch{{rows: []liveLogRow{{span: 1, body: "a"}, {span: 2, body: "b"}}, cursor: "2"}}
	}}
	out := &bodyCollector{}
	l := newLiveLogs(reader, "trace", out, false)
	l.subscribe(idtui.LogRequest{Span: liveTestSpan(1)})
	l.subscribe(idtui.LogRequest{Span: liveTestSpan(2)})
	l.subscribe(idtui.LogRequest{Span: liveTestSpan(1)})
	require.Empty(t, reader.seen(), "read before finish")
	require.NoError(t, l.finish(t.Context()))
	require.Equal(t, []tracesource.LogQuery{{
		Spans: []archive.LogSpan{{SpanID: liveTestSpan(1).String()}, {SpanID: liveTestSpan(2).String()}},
	}}, reader.seen())
	require.Equal(t, []string{"a", "b"}, out.got())

	l.subscribe(idtui.LogRequest{Span: liveTestSpan(3)})
	require.NoError(t, l.finish(t.Context()))
	require.Len(t, reader.seen(), 1, "read again after finish")
}

// TestLiveLogsFinishCatchesUp: at exit, a span requested for the final
// render is caught up to the follow's cursor, then the whole set is read
// from that cursor to the end.
func TestLiveLogsFinishCatchesUp(t *testing.T) {
	reader := &fakeLogReader{respond: func(q tracesource.LogQuery) []liveLogBatch {
		switch {
		case q.Through == "" && q.Cursor == "":
			return []liveLogBatch{{rows: []liveLogRow{{span: 1, body: "a1"}}, cursor: "4"}}
		case q.Through == "4":
			return []liveLogBatch{{rows: []liveLogRow{{span: 2, body: "b1"}}, cursor: "4"}}
		case !q.Follow && q.Cursor == "4" && len(q.Spans) == 2:
			return []liveLogBatch{{rows: []liveLogRow{{span: 1, body: "a2"}, {span: 2, body: "b2"}}, cursor: "6"}}
		}
		return nil
	}}
	out := &bodyCollector{}
	l := newLiveLogs(reader, "trace", out, true)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); l.run(ctx) }()
	l.stop = func() { cancel(); <-done }

	l.subscribe(idtui.LogRequest{Span: liveTestSpan(1)})
	require.Eventually(t, func() bool { return len(out.got()) == 1 }, 5*time.Second, 10*time.Millisecond)
	// Requested while the follow runs, but only caught up at finish: stop
	// the follow first, as finish does, so the wake is not consumed.
	l.stop()
	l.stop = func() {}
	l.subscribe(idtui.LogRequest{Span: liveTestSpan(2)})
	require.NoError(t, l.finish(t.Context()))
	require.Equal(t, []string{"a1", "b1", "a2", "b2"}, out.got())
}

// joiningLogReader is a store whose catch-ups take long enough for another
// span to join the set each time, as in a long session where a catch-up
// reads the whole store and parallel tool calls keep adding rows to render.
type joiningLogReader struct {
	l *liveLogs

	mu     sync.Mutex
	joined uint16
}

func (r *joiningLogReader) ReadLogs(ctx context.Context, _ string, q tracesource.LogQuery, cb func(context.Context, *collogspb.ExportLogsServiceRequest, string) error) error {
	switch {
	case q.Through != "":
		// A catch-up: another span joins while it reads.
		r.mu.Lock()
		r.joined++
		var id dagui.SpanID
		id.SpanID[0] = 0xff
		id.SpanID[6] = byte(r.joined >> 8)
		id.SpanID[7] = byte(r.joined)
		r.mu.Unlock()
		r.l.subscribe(idtui.LogRequest{Span: id})
		return nil
	case q.Cursor == "":
		// The first read of the set.
		if err := cb(ctx, liveLogRequest([]liveLogRow{{span: 1, body: "a1"}}), "3"); err != nil {
			return err
		}
	case q.Cursor == "3":
		// Past the cursor: a row the store got since. A follow sends it
		// at its next poll.
		if q.Follow {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
		}
		if err := cb(ctx, liveLogRequest([]liveLogRow{{span: 1, body: "a2"}}), "5"); err != nil {
			return err
		}
	}
	if q.Follow {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

// TestLiveLogsFollowAdvancesWhileSpansJoin: spans joining the set faster
// than a catch-up reads must not starve the follow. Each joining span ends
// the follow to catch it up; if another joins meanwhile, the follow that
// resumes is ended again before it reads anything, so without reading the
// set up to the store's end in between, its cursor never moves and nothing
// stored since reaches any span: a long session's tool calls and replies
// stop showing output.
func TestLiveLogsFollowAdvancesWhileSpansJoin(t *testing.T) {
	reader := &joiningLogReader{}
	out := &bodyCollector{}
	l := newLiveLogs(reader, "trace", out, true)
	reader.l = l
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); l.run(ctx) }()
	defer func() { cancel(); <-done }()

	l.subscribe(idtui.LogRequest{Span: liveTestSpan(1)})
	require.Eventually(t, func() bool { return slices.Contains(out.got(), "a1") }, 5*time.Second, 10*time.Millisecond)
	l.subscribe(idtui.LogRequest{Span: liveTestSpan(2)})
	require.Eventually(t, func() bool { return slices.Contains(out.got(), "a2") }, 5*time.Second, 10*time.Millisecond,
		"the follow never read past its cursor while spans kept joining")
	require.Equal(t, []string{"a1", "a2"}, out.got())
}

// TestLiveLogsSkipEmptyBatches: a batch with no record of a requested span
// (most of a catch-up's, which reads the store from its start) costs the
// frontend nothing: it is neither asked to route it nor sent it.
func TestLiveLogsSkipEmptyBatches(t *testing.T) {
	reader := &fakeLogReader{respond: func(q tracesource.LogQuery) []liveLogBatch {
		return []liveLogBatch{
			{cursor: "1000"},
			{rows: []liveLogRow{{span: 1, body: "a"}, {span: 7, body: "noise"}}, cursor: "2000"},
			{rows: []liveLogRow{{span: 7, body: "noise"}}, cursor: "3000"},
		}
	}}
	exports := 0
	out := &bodyCollector{}
	l := newLiveLogs(reader, "trace", exporterFunc(func(ctx context.Context, records []sdklog.Record) error {
		exports++
		return out.Export(ctx, records)
	}), false)
	asked := 0
	l.coverage = func(targets []idtui.LogTarget) []idtui.LogRoute {
		asked++
		routes := make([]idtui.LogRoute, len(targets))
		for i, target := range targets {
			routes[i] = idtui.LogRoute{Coverage: idtui.LogUncovered, Span: target.Span}
			if target.Span == liveTestSpan(1) {
				routes[i].Coverage = idtui.LogCovered
			}
		}
		return routes
	}
	l.subscribe(idtui.LogRequest{Span: liveTestSpan(1)})
	require.NoError(t, l.finish(t.Context()))
	require.Equal(t, []string{"a"}, out.got())
	require.Equal(t, 2, asked, "the frontend was asked about a batch with no records")
	require.Equal(t, 1, exports, "a batch with nothing to import was exported")
	require.Equal(t, "3000", l.cursor, "the cursor did not advance past skipped batches")
}

type exporterFunc func(context.Context, []sdklog.Record) error

func (f exporterFunc) Export(ctx context.Context, records []sdklog.Record) error {
	return f(ctx, records)
}
func (exporterFunc) Shutdown(context.Context) error   { return nil }
func (exporterFunc) ForceFlush(context.Context) error { return nil }

// fakeLogStore is a live store: rows are appended, numbered from 1 as their
// row IDs, a query selects them by span (with its descendants, by parents)
// or digest, a cursor is a row number, and a follow polls for the rows
// stored since until it is cancelled.
type fakeLogStore struct {
	parents map[uint16]uint16
	// placing makes the store answer like the engine's (clientdb's
	// LogQueryScope): a query selecting any span with its descendants also
	// returns the rows of spans it has no snapshot of yet (not known), whose
	// place it cannot tell. Spans get snapshots with snapshot.
	placing bool
	known   map[uint16]bool

	mu      sync.Mutex
	rows    []liveLogRow
	queries []tracesource.LogQuery
}

func (s *fakeLogStore) add(rows ...liveLogRow) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range rows {
		r.id = int64(len(s.rows) + 1)
		s.rows = append(s.rows, r)
	}
}

// snapshot stores span's snapshot, placing it beneath parent (0: a root).
func (s *fakeLogStore) snapshot(span, parent uint16) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.known == nil {
		s.known = map[uint16]bool{}
	}
	s.known[span] = true
	if parent != 0 {
		if s.parents == nil {
			s.parents = map[uint16]uint16{}
		}
		s.parents[span] = parent
	}
}

func (s *fakeLogStore) seen() []tracesource.LogQuery {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.queries)
}

func (s *fakeLogStore) ReadLogs(ctx context.Context, _ string, q tracesource.LogQuery, cb func(context.Context, *collogspb.ExportLogsServiceRequest, string) error) error {
	q.Spans = slices.Clone(q.Spans)
	slices.SortFunc(q.Spans, func(a, b archive.LogSpan) int { return compareStrings(a.SpanID, b.SpanID) })
	spans, descendants, digests := map[string]bool{}, map[string]bool{}, map[string]bool{}
	var tree bool
	for _, span := range q.Spans {
		spans[span.SpanID] = true
		descendants[span.SpanID] = span.Descendants
		tree = tree || span.Descendants
		for _, d := range span.Digests {
			digests[d] = true
		}
	}
	// selects runs with s.mu held.
	selects := func(r liveLogRow) bool {
		if spans[liveTestSpan(r.span).String()] || (r.digest != "" && digests[r.digest]) {
			return true
		}
		if s.placing && tree && !s.known[r.span] {
			return true
		}
		for n, ok := s.parents[r.span]; ok; n, ok = s.parents[n] {
			if descendants[liveTestSpan(n).String()] {
				return true
			}
		}
		return false
	}
	pos, _ := strconv.Atoi(q.Cursor)
	s.mu.Lock()
	s.queries = append(s.queries, q)
	end := len(s.rows)
	s.mu.Unlock()
	if q.Through != "" {
		through, _ := strconv.Atoi(q.Through)
		end = min(end, through)
	}
	for {
		if end > pos {
			var batch []liveLogRow
			s.mu.Lock()
			for _, r := range s.rows[pos:end] {
				if selects(r) {
					batch = append(batch, r)
				}
			}
			s.mu.Unlock()
			if err := cb(ctx, liveLogRequest(batch), strconv.Itoa(end)); err != nil {
				return err
			}
			pos = end
		}
		if !q.Follow {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Millisecond):
		}
		s.mu.Lock()
		end = len(s.rows)
		s.mu.Unlock()
	}
}

// Queued exports retain claim lifetimes, not read numbers or the transient
// released map. Exercise these transitions without timers or a running reader.
func TestLiveLogsQueuedClaimLifetime(t *testing.T) {
	for _, transition := range []string{"held", "unsubscribed", "dropped", "next read", "reclaimed", "retired"} {
		t.Run(transition, func(t *testing.T) {
			live := dagui.TraceID{TraceID: trace.TraceID{15: 1}}
			root, victim, held := liveTestSpan(1), liveTestSpan(2), liveTestSpan(3)
			start := time.Unix(100, 0)
			fe := idtui.NewASCIIReporterWithDB(io.Discard, dagui.NewDB())
			l := newLiveLogs(nil, live.String(), nil, true)
			l.coverage = fe.LiveLogsCoverage
			export := fe.SetLiveLogs(live, l.subscribe)
			var queued []func() error
			l.export = exporterFunc(func(ctx context.Context, records []sdklog.Record) error {
				records = slices.Clone(records)
				queued = append(queued, func() error { return export.Export(ctx, records) })
				return nil
			})
			fe.ImportSnapshots([]dagui.SpanSnapshot{
				{ID: root, TraceID: live, Name: "agent", StartTime: start, Final: true},
				{ID: victim, TraceID: live, ParentID: root, Name: "victim", LLMRole: "assistant", Message: "received", CallDigest: "xxh3:call", Output: "xxh3:out", StartTime: start, Final: true},
				{ID: held, TraceID: live, ParentID: root, Name: "held", LLMRole: "assistant", Message: "received", StartTime: start.Add(time.Second), Final: true},
			})
			fe.SetPrimary(root)
			fe.RequestSurfacedLogs()
			require.NoError(t, l.catchUp(t.Context()))
			require.NoError(t, l.importLogs(t.Context(), liveLogRequest([]liveLogRow{
				{span: 2, body: "original own\n"},
				{span: 9, digest: "xxh3:out", body: "original digest\n"},
				{span: 3, body: "unaffected\n"},
			}), 0, l.beginRead()))
			key := logOwner{span: victim.SpanID}
			before := l.owned[key]
			switch transition {
			case "unsubscribed":
				l.release(idtui.LogRelease{Spans: []dagui.SpanID{victim}})
			case "dropped", "next read", "reclaimed":
				l.release(idtui.LogRelease{Spans: []dagui.SpanID{victim}, Dropped: []dagui.SpanID{victim}})
				if transition != "dropped" {
					l.beginRead()
					require.Empty(t, l.released, "a queued batch must not rely on read-scoped tombstones")
				}
				if transition == "reclaimed" {
					l.subscribe(idtui.LogRequest{Span: victim, Digests: []string{"xxh3:out"}})
					require.NoError(t, l.importLogs(t.Context(), liveLogRequest([]liveLogRow{
						{span: 2, body: "fresh own\n"},
						{span: 9, digest: "xxh3:out", body: "fresh digest\n"},
					}), 0, l.beginRead()))
					require.Equal(t, before.read, l.owned[key].read, "both claims use the follow's read number")
					require.NotEqual(t, before.generation, l.owned[key].generation)
					// The new lifetime can even render before the stale batch.
					require.NoError(t, queued[1]())
				}
			case "retired":
				l.idleSince[victim.String()] = start
				l.retire(start.Add(l.grace))
				require.NotEqual(t, before.read, l.owned[key].read)
				require.Equal(t, before.generation, l.owned[key].generation)
				// A gap read keeps the same retained text lifetime.
				r := l.retired[victim.String()]
				require.NoError(t, l.importLogs(t.Context(), liveLogRequest([]liveLogRow{
					{span: 2, body: "gap own\n"},
				}), r.read, l.beginRead()))
			}
			require.NoError(t, queued[0]())
			if transition == "retired" {
				require.NoError(t, queued[1]())
			}
			var rendered strings.Builder
			require.NoError(t, fe.FinalRender(&rendered))
			text := rendered.String()
			require.Equal(t, 1, strings.Count(text, "unaffected"), text)
			wantOriginal := 0
			if transition == "held" || transition == "unsubscribed" || transition == "retired" {
				wantOriginal = 1
			}
			require.Equal(t, wantOriginal, strings.Count(text, "original own"), text)
			require.Equal(t, wantOriginal, strings.Count(text, "original digest"), text)
			if transition == "reclaimed" {
				require.Equal(t, 1, strings.Count(text, "fresh own"), text)
				require.Equal(t, 1, strings.Count(text, "fresh digest"), text)
			}
			if transition == "retired" {
				require.Equal(t, 1, strings.Count(text, "gap own"), text)
			}
		})
	}
}

// TestLiveLogsQueuedExportAfterRealEviction drives the running headless
// frontend's real cache and view changes. It pauses a selected follow batch
// after ownership is claimed, but before its export is enqueued, while a
// zoom hides enough conversation messages for the normal sweep to evict one.
func TestLiveLogsQueuedExportAfterRealEviction(t *testing.T) {
	t.Setenv("DAGGER_TUI_CONSOLE", "127.0.0.1:0")
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	live := dagui.TraceID{TraceID: trace.TraceID{15: 1}}
	root, victim, zoom := liveTestSpan(1), liveTestSpan(2), liveTestSpan(41)
	fe := idtui.NewWithDB(io.Discard, dagui.NewDB())
	feCtx, stopFE := context.WithCancel(context.Background())
	feDone, ready := make(chan struct{}), make(chan struct{})
	var feErr error
	go func() {
		defer close(feDone)
		feErr = fe.Run(feCtx, dagui.FrontendOpts{}, func(ctx context.Context) (cleanups.CleanupF, error) {
			close(ready)
			<-ctx.Done()
			return nil, nil
		})
	}()
	<-ready
	lCtx, stopLogs := context.WithCancel(ctx)
	logsDone := make(chan struct{})
	unblock := make(chan struct{})
	var unblockOnce sync.Once
	t.Cleanup(func() {
		unblockOnce.Do(func() { close(unblock) })
		stopLogs()
		<-logsDone
		stopFE()
		<-feDone
	})

	store := &fakeLogStore{}
	out := &bodyCollector{}
	reader := logReaderFunc(func(ctx context.Context, traceID string, q tracesource.LogQuery, cb func(context.Context, *collogspb.ExportLogsServiceRequest, string) error) error {
		for _, span := range q.Spans {
			if span.Descendants || len(span.Digests) != 0 {
				return fmt.Errorf("unexpected non-own-only selection: %+v", span)
			}
		}
		return store.ReadLogs(ctx, traceID, q, cb)
	})
	l := newLiveLogs(reader, live.String(), nil, true)
	l.coverage = fe.LiveLogsCoverage
	l.activity = fe.LiveLogsActivity
	export := fe.SetLiveLogs(live, l.subscribe)
	released := make(chan idtui.LogRelease, 10)
	fe.SetLogRelease(live, func(r idtui.LogRelease) {
		l.release(r)
		if slices.Contains(r.Dropped, victim) {
			select {
			case released <- r:
			default:
			}
		}
	})
	blocked, delivered := make(chan struct{}), make(chan struct{})
	var blockOnce sync.Once
	l.export = exporterFunc(func(ctx context.Context, records []sdklog.Record) error {
		intercept := false
		for _, record := range records {
			if record.Body().AsString() == "intercepted prefix " {
				blockOnce.Do(func() {
					intercept = true
					close(blocked)
				})
			}
		}
		if intercept {
			<-unblock
		}
		if err := out.Export(context.Background(), records); err != nil {
			return err
		}
		if err := export.Export(ctx, records); err != nil {
			return err
		}
		if intercept {
			// Queue the user's return immediately behind the stale export,
			// before another sweep could discard that recreated terminal.
			fe.ZoomToSpan(root)
			fe.RequestSurfacedLogs()
			close(delivered)
		}
		return nil
	})
	go func() { defer close(logsDone); l.run(lCtx) }()
	start := time.Now().Add(-time.Minute)
	snaps := []dagui.SpanSnapshot{{ID: root, TraceID: live, Name: "agent", StartTime: start, Final: true}}
	for n := uint16(2); n <= 41; n++ {
		snaps = append(snaps, dagui.SpanSnapshot{ID: liveTestSpan(n), TraceID: live, ParentID: root, Name: fmt.Sprintf("message %d", n), LLMRole: "assistant", Message: "received", StartTime: start.Add(time.Duration(n) * time.Millisecond), EndTime: start.Add(time.Second), Final: true})
		store.add(liveLogRow{span: n, body: fmt.Sprintf("message %d initial\n", n)})
	}
	fe.ImportSnapshots(snaps)
	fe.SetPrimary(root)
	fe.ZoomToSpan(root)
	fe.RequestSurfacedLogs()
	require.Eventually(t, func() bool { return len(out.got()) == 40 }, 5*time.Second, 10*time.Millisecond)
	require.NoError(t, fe.WaitForEventLoop(ctx))
	require.Empty(t, released, "the shown conversation must stay pinned")

	fe.ZoomToSpan(zoom)
	fe.RequestSurfacedLogs()
	// Let the real two-second shown grace expire before intercepting any
	// export. Opening a new span then schedules an ordinary 500ms sweep.
	time.Sleep(2500 * time.Millisecond)
	other := liveTestSpan(42)
	fe.ImportSnapshots([]dagui.SpanSnapshot{{ID: other, TraceID: live, ParentID: root, Name: "other", StartTime: start.Add(2 * time.Second), HasLogs: true, Final: true}})
	fe.ZoomToSpan(other)
	fe.RequestSurfacedLogs()
	store.add(liveLogRow{span: 2, body: "intercepted prefix "})
	select {
	case <-blocked:
	case <-ctx.Done():
		t.Fatal("follow did not reach export")
	}
	key := logOwner{span: victim.SpanID}
	l.mu.Lock()
	claim, owned := l.owned[key]
	l.mu.Unlock()
	require.True(t, owned, "the selected batch has claimed its pair before export")
	require.Equal(t, victim, claim.target)
	select {
	case r := <-released:
		require.Contains(t, r.Spans, victim, "actual frontend cache eviction must unsubscribe the victim")
	case <-ctx.Done():
		t.Fatal("normal cache sweep did not evict the hidden message")
	}
	l.mu.Lock()
	_, owned = l.owned[key]
	l.mu.Unlock()
	require.False(t, owned, "the real frontend drop forgot the pair before its export")
	unblockOnce.Do(func() { close(unblock) })
	select {
	case <-delivered:
	case <-ctx.Done():
		t.Fatal("stale export did not reach frontend")
	}
	require.Eventually(t, func() bool {
		return strings.Count(strings.Join(out.got(), ""), "message 2 initial") == 2
	}, 5*time.Second, 10*time.Millisecond, "returning to the conversation must re-read the evicted message")
	require.NoError(t, fe.WaitForEventLoop(ctx))
	stopLogs()
	<-logsDone
	stopFE()
	<-feDone
	require.NoError(t, feErr)
	var rendered strings.Builder
	require.NoError(t, fe.FinalRender(&rendered))
	require.Equal(t, 1, strings.Count(rendered.String(), "intercepted prefix"), "a batch selected before actual eviction must not be appended after the terminal is dropped")
}

// fakeActivity is a frontend's view of which spans are active: every span,
// until it is marked idle.
type fakeActivity struct {
	mu      sync.Mutex
	idle    map[dagui.SpanID]bool
	through map[dagui.SpanID]time.Time
}

func (a *fakeActivity) activity(ids []dagui.SpanID) []idtui.LogActivity {
	a.mu.Lock()
	defer a.mu.Unlock()
	activity := make([]idtui.LogActivity, len(ids))
	for i, id := range ids {
		activity[i] = idtui.LogActivity{Active: !a.idle[id], Through: a.through[id]}
	}
	return activity
}

func (a *fakeActivity) set(idle bool, spans ...uint16) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, n := range spans {
		a.idle[liveTestSpan(n)] = idle
	}
}

// ran records work a span's logs include that ended at through, e.g. an
// effect too brief for a poll to see running.
func (a *fakeActivity) ran(span uint16, through time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.through[liveTestSpan(span)] = through
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// startRetiringLogs follows store, retiring the spans activity reports idle
// once clock passes the grace period.
func startRetiringLogs(t *testing.T, store *fakeLogStore, opts ...func(*liveLogs)) (*liveLogs, *bodyCollector, *fakeActivity, *fakeClock) {
	out := &bodyCollector{}
	l := newLiveLogs(store, "trace", out, true)
	activity := &fakeActivity{idle: map[dagui.SpanID]bool{}, through: map[dagui.SpanID]time.Time{}}
	clock := &fakeClock{t: time.Unix(1000, 0)}
	l.activity = activity.activity
	l.now = clock.now
	l.pollEvery = time.Millisecond
	l.grace = 3 * time.Second
	for _, opt := range opts {
		opt(l)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); l.run(ctx) }()
	l.stop = func() { cancel(); <-done }
	t.Cleanup(func() { cancel(); <-done })
	return l, out, activity, clock
}

func liveTestSpanIDs(spans ...uint16) []string {
	ids := make([]string, len(spans))
	for i, n := range spans {
		ids[i] = liveTestSpan(n).String()
	}
	slices.Sort(ids)
	return ids
}

func querySpanIDs(q tracesource.LogQuery) []string {
	ids := make([]string, len(q.Spans))
	for i, span := range q.Spans {
		ids[i] = span.SpanID
	}
	return ids
}

// waitFollowing waits until the store's last query follows exactly spans.
func waitFollowing(t *testing.T, store *fakeLogStore, spans ...uint16) {
	t.Helper()
	want := liveTestSpanIDs(spans...)
	require.Eventually(t, func() bool {
		seen := store.seen()
		if len(seen) == 0 {
			return false
		}
		last := seen[len(seen)-1]
		return last.Follow && slices.Equal(querySpanIDs(last), want)
	}, 10*time.Second, time.Millisecond)
}

// waitIdle waits until the follow saw every span idle.
func waitIdle(t *testing.T, l *liveLogs, spans ...uint16) {
	t.Helper()
	require.Eventually(t, func() bool {
		l.mu.Lock()
		defer l.mu.Unlock()
		for _, n := range spans {
			if _, ok := l.idleSince[liveTestSpan(n).String()]; !ok {
				return false
			}
		}
		return true
	}, 10*time.Second, time.Millisecond)
}

// TestLiveLogsRetireFinishedSpans: a span the frontend reports idle stays
// followed through the grace period, then leaves the follow once a read of
// the set took everything stored up to then; its logs stay, and the
// frontend asking again reads nothing. An active span is never retired.
func TestLiveLogsRetireFinishedSpans(t *testing.T) {
	store := &fakeLogStore{}
	store.add(liveLogRow{span: 1, body: "a1"}, liveLogRow{span: 2, body: "b1"})
	l, out, activity, clock := startRetiringLogs(t, store)
	l.subscribe(idtui.LogRequest{Span: liveTestSpan(1)})
	l.subscribe(idtui.LogRequest{Span: liveTestSpan(2)})
	waitFollowing(t, store, 1, 2)
	require.Eventually(t, func() bool { return len(out.got()) == 2 }, 5*time.Second, time.Millisecond)

	// Span 1 ends; what is stored within the grace period is still read.
	activity.set(true, 1)
	waitIdle(t, l, 1)
	store.add(liveLogRow{span: 1, body: "a2"})
	clock.advance(time.Second)
	require.Eventually(t, func() bool { return slices.Contains(out.got(), "a2") }, 5*time.Second, time.Millisecond)
	waitFollowing(t, store, 1, 2)

	// Past it, the restart reads the set to the store's end once more, with
	// a row stored just before, then follows only span 2.
	store.add(liveLogRow{span: 1, body: "a3"})
	clock.advance(2 * time.Second)
	waitFollowing(t, store, 2)
	store.add(liveLogRow{span: 2, body: "b2"})
	require.Eventually(t, func() bool { return slices.Contains(out.got(), "b2") }, 5*time.Second, time.Millisecond)
	require.Equal(t, []string{"a1", "b1", "a2", "a3", "b2"}, out.got(), "every row once")
	l.mu.Lock()
	retired := l.retired[liveTestSpan(1).String()]
	l.mu.Unlock()
	require.Equal(t, archive.LogSpan{SpanID: liveTestSpan(1).String()}, retired.span)
	require.Equal(t, "4", retired.cursor, "the cursor it was read up to")

	// Asked again (a view remounting), the retired span is not caught up
	// again; span 2, active however long it runs, stays followed.
	n := len(store.seen())
	l.subscribe(idtui.LogRequest{Span: liveTestSpan(1)})
	clock.advance(time.Hour)
	require.Never(t, func() bool { return len(store.seen()) != n }, 100*time.Millisecond, 5*time.Millisecond)
	l.mu.Lock()
	require.Empty(t, l.pending)
	l.mu.Unlock()
	waitFollowing(t, store, 2)
}

// TestLiveLogsReactivateRetiredSpan: a retired span that selects a digest
// (e.g. an asService call) gets rows again once the service it created
// starts: seen active, it is read from where it retired up to the follow's
// cursor -- without the rows the follow read meanwhile -- and followed again.
func TestLiveLogsReactivateRetiredSpan(t *testing.T) {
	store := &fakeLogStore{}
	store.add(liveLogRow{span: 5, body: "created"}, liveLogRow{span: 6, body: "c1"})
	l, out, activity, clock := startRetiringLogs(t, store)
	creator := idtui.LogRequest{Span: liveTestSpan(5), Digests: []string{"xxh3:svc"}}
	l.subscribe(creator)
	l.subscribe(idtui.LogRequest{Span: liveTestSpan(6)})
	waitFollowing(t, store, 5, 6)
	activity.set(true, 5)
	waitIdle(t, l, 5)
	clock.advance(3 * time.Second)
	waitFollowing(t, store, 6)
	l.mu.Lock()
	retiredAt := l.retired[creator.Span.String()].cursor
	l.mu.Unlock()
	require.Equal(t, "2", retiredAt)

	// The service starts while its creator is retired. Its stdio names the
	// creator's digest; one row span 6 selects too, so the follow read it.
	store.add(
		liveLogRow{span: 9, body: "svc1", digest: "xxh3:svc"},
		liveLogRow{span: 6, body: "both", digest: "xxh3:svc"},
		liveLogRow{span: 6, body: "c2"},
	)
	require.Eventually(t, func() bool { return slices.Contains(out.got(), "c2") }, 5*time.Second, time.Millisecond)
	activity.set(false, 5)
	waitFollowing(t, store, 5, 6)
	store.add(liveLogRow{span: 9, body: "svc2", digest: "xxh3:svc"})
	require.Eventually(t, func() bool { return slices.Contains(out.got(), "svc2") }, 5*time.Second, time.Millisecond)
	require.Equal(t, []string{"created", "c1", "both", "c2", "svc1", "svc2"}, out.got(), "every row once")

	var gaps []tracesource.LogQuery
	for _, q := range store.seen() {
		if q.Through != "" && slices.Contains(querySpanIDs(q), creator.Span.String()) {
			gaps = append(gaps, q)
		}
	}
	require.Equal(t, []tracesource.LogQuery{{
		Spans:   []archive.LogSpan{{SpanID: creator.Span.String(), Digests: creator.Digests}},
		Cursor:  "2",
		Through: "5",
	}}, gaps, "the gap is read once")
}

// TestLiveLogsReactivateAfterBriefWork: work a retired span's logs include
// that ran too briefly for any poll to see it running -- a quick lazy
// evaluation -- still moves its activity forward, which reactivates it,
// even at exit.
func TestLiveLogsReactivateAfterBriefWork(t *testing.T) {
	store := &fakeLogStore{}
	store.add(liveLogRow{span: 5, body: "created"}, liveLogRow{span: 6, body: "c1"})
	l, out, activity, clock := startRetiringLogs(t, store)
	l.subscribe(idtui.LogRequest{Span: liveTestSpan(5), Digests: []string{"xxh3:out"}})
	l.subscribe(idtui.LogRequest{Span: liveTestSpan(6)})
	waitFollowing(t, store, 5, 6)
	activity.set(true, 5)
	waitIdle(t, l, 5)
	clock.advance(3 * time.Second)
	waitFollowing(t, store, 6)

	// Retired spans are polled while following...
	store.add(liveLogRow{span: 9, body: "eval1", digest: "xxh3:out"})
	activity.ran(5, time.Unix(2000, 0))
	waitFollowing(t, store, 5, 6)
	require.Eventually(t, func() bool { return slices.Contains(out.got(), "eval1") }, 5*time.Second, time.Millisecond)
	waitIdle(t, l, 5)
	clock.advance(3 * time.Second)
	waitFollowing(t, store, 6)

	// ...and once more at exit.
	l.stop()
	store.add(liveLogRow{span: 10, body: "eval2", digest: "xxh3:out"})
	activity.ran(5, time.Unix(3000, 0))
	require.NoError(t, l.finish(t.Context()))
	require.Equal(t, []string{"created", "c1", "eval1", "eval2"}, out.got())
}

// TestLiveLogsReactivateReadsOnlyNewRows: a reactivated span's gap read
// skips what other spans' follow read meanwhile, whether they select it by
// descendants, or were followed for part of the gap and retired since.
func TestLiveLogsReactivateReadsOnlyNewRows(t *testing.T) {
	// The roll-up 10 holds the asService call 5, and the service 9 it
	// starts.
	store := &fakeLogStore{parents: map[uint16]uint16{5: 10, 9: 10}}
	store.add(liveLogRow{span: 5, body: "created"}, liveLogRow{span: 10, body: "r0"})
	l, out, activity, clock := startRetiringLogs(t, store)
	l.subscribe(idtui.LogRequest{Span: liveTestSpan(10), Descendants: true})
	l.subscribe(idtui.LogRequest{Span: liveTestSpan(5), Digests: []string{"xxh3:svc"}})
	l.subscribe(idtui.LogRequest{Span: liveTestSpan(6)})
	waitFollowing(t, store, 5, 6, 10)
	activity.set(true, 5)
	waitIdle(t, l, 5)
	clock.advance(3 * time.Second)
	waitFollowing(t, store, 6, 10)

	store.add(
		liveLogRow{span: 9, body: "svc1", digest: "xxh3:svc"},
		liveLogRow{span: 6, body: "both", digest: "xxh3:svc"},
	)
	require.Eventually(t, func() bool { return len(out.got()) == 4 }, 5*time.Second, time.Millisecond)
	activity.set(true, 6)
	waitIdle(t, l, 6)
	clock.advance(3 * time.Second)
	waitFollowing(t, store, 10)

	activity.set(false, 5)
	waitFollowing(t, store, 5, 10)
	store.add(liveLogRow{span: 9, body: "svc2", digest: "xxh3:svc"})
	require.Eventually(t, func() bool { return slices.Contains(out.got(), "svc2") }, 5*time.Second, time.Millisecond)
	require.Equal(t, []string{"created", "r0", "svc1", "both", "svc2"}, out.got(), "every row once")
}

// TestLiveLogsReactivateRestartedService: a service writes its stdio under
// the span that installed it, so a restart after that span retired stores
// more rows of a pair the follow read before. They pass to the span's gap
// read on retiring, which imports the restart's first lines, once.
func TestLiveLogsReactivateRestartedService(t *testing.T) {
	store := &fakeLogStore{}
	store.add(liveLogRow{span: 5, body: "run1", digest: "xxh3:svc"}, liveLogRow{span: 6, body: "c1"})
	l, out, activity, clock := startRetiringLogs(t, store)
	l.subscribe(idtui.LogRequest{Span: liveTestSpan(5)})
	l.subscribe(idtui.LogRequest{Span: liveTestSpan(6)})
	waitFollowing(t, store, 5, 6)
	require.Eventually(t, func() bool { return len(out.got()) == 2 }, 5*time.Second, time.Millisecond)
	activity.set(true, 5)
	waitIdle(t, l, 5)
	clock.advance(3 * time.Second)
	waitFollowing(t, store, 6)

	store.add(liveLogRow{span: 5, body: "run2 banner", digest: "xxh3:svc"}, liveLogRow{span: 6, body: "c2"})
	require.Eventually(t, func() bool { return slices.Contains(out.got(), "c2") }, 5*time.Second, time.Millisecond)
	activity.set(false, 5)
	waitFollowing(t, store, 5, 6)
	store.add(liveLogRow{span: 5, body: "run2 later", digest: "xxh3:svc"})
	require.Eventually(t, func() bool { return slices.Contains(out.got(), "run2 later") }, 5*time.Second, time.Millisecond)
	require.Equal(t, []string{"run1", "c1", "c2", "run2 banner", "run2 later"}, out.got(), "every row once")
}

// TestLiveLogsReactivateCreatorAndInstaller: a service's stdio names the
// span that installed it, which its exec span links, and the digest of the
// call that created it. Both retired, a restart reactivates both, and its
// first lines are read once.
func TestLiveLogsReactivateCreatorAndInstaller(t *testing.T) {
	store := &fakeLogStore{}
	store.add(liveLogRow{span: 6, body: "run1", digest: "xxh3:svc"}, liveLogRow{span: 7, body: "c1"})
	l, out, activity, clock := startRetiringLogs(t, store)
	l.subscribe(idtui.LogRequest{Span: liveTestSpan(5), Digests: []string{"xxh3:svc"}})
	l.subscribe(idtui.LogRequest{Span: liveTestSpan(6)})
	l.subscribe(idtui.LogRequest{Span: liveTestSpan(7)})
	waitFollowing(t, store, 5, 6, 7)
	require.Eventually(t, func() bool { return len(out.got()) == 2 }, 5*time.Second, time.Millisecond)
	activity.set(true, 5)
	waitIdle(t, l, 5)
	clock.advance(3 * time.Second)
	waitFollowing(t, store, 6, 7)
	activity.set(true, 6)
	waitIdle(t, l, 6)
	clock.advance(3 * time.Second)
	waitFollowing(t, store, 7)

	store.add(liveLogRow{span: 6, body: "run2 banner", digest: "xxh3:svc"}, liveLogRow{span: 7, body: "c2"})
	require.Eventually(t, func() bool { return slices.Contains(out.got(), "c2") }, 5*time.Second, time.Millisecond)
	activity.set(false, 5, 6)
	waitFollowing(t, store, 5, 6, 7)
	store.add(liveLogRow{span: 6, body: "run2 later", digest: "xxh3:svc"})
	require.Eventually(t, func() bool { return slices.Contains(out.got(), "run2 later") }, 5*time.Second, time.Millisecond)
	require.Equal(t, []string{"run1", "c1", "c2", "run2 banner", "run2 later"}, out.got(), "every row once")
}

// TestLiveLogsSetStaysWithinQueryLimit: spans requested all at once -- a
// resumed session's whole conversation -- join the set only as far as one
// query may select, and the rest as earlier ones retire.
func TestLiveLogsSetStaysWithinQueryLimit(t *testing.T) {
	const spans, limit = 25, 10
	store := &fakeLogStore{}
	var want []string
	for n := range uint16(spans) {
		body := fmt.Sprintf("row %d", n+1)
		want = append(want, body)
		store.add(liveLogRow{span: n + 1, body: body})
	}
	l, out, activity, clock := startRetiringLogs(t, store, func(l *liveLogs) { l.maxSpans = limit })
	for n := range uint16(spans) {
		activity.set(true, n+1)
		l.subscribe(idtui.LogRequest{Span: liveTestSpan(n + 1)})
	}
	require.Eventually(t, func() bool {
		clock.advance(time.Second)
		return len(out.got()) == spans
	}, 10*time.Second, time.Millisecond)
	got := out.got()
	slices.Sort(got)
	slices.Sort(want)
	require.Equal(t, want, got, "every row once")
	for _, q := range store.seen() {
		require.LessOrEqual(t, len(q.Spans), limit)
	}
}

// TestLiveLogsReadAtFinishInChunks: without following, more requests than
// one query may select are read at finish in several.
func TestLiveLogsReadAtFinishInChunks(t *testing.T) {
	const spans, limit = 25, 10
	store := &fakeLogStore{}
	var want []string
	for n := range uint16(spans) {
		body := fmt.Sprintf("row %d", n+1)
		want = append(want, body)
		store.add(liveLogRow{span: n + 1, body: body})
	}
	out := &bodyCollector{}
	l := newLiveLogs(store, "trace", out, false)
	l.maxSpans = limit
	for n := range uint16(spans) {
		l.subscribe(idtui.LogRequest{Span: liveTestSpan(n + 1)})
	}
	require.NoError(t, l.finish(t.Context()))
	got := out.got()
	slices.Sort(got)
	slices.Sort(want)
	require.Equal(t, want, got, "every row once")
	var sizes []int
	for _, q := range store.seen() {
		sizes = append(sizes, len(q.Spans))
	}
	require.Equal(t, []int{10, 10, 5}, sizes)
}

// TestLiveLogsFollowOnlyActiveSpans: in a long session, thousands of spans
// join and finish -- more than one query may select -- yet the follow only
// ever selects the spans still active or within their grace period, and
// every row is read once.
func TestLiveLogsFollowOnlyActiveSpans(t *testing.T) {
	const rounds, perRound = 20, 500
	require.Greater(t, rounds*perRound, archive.MaxLogQuerySpans)
	store := &fakeLogStore{}
	l, out, activity, clock := startRetiringLogs(t, store)
	var want []string
	var prev []uint16
	for r := range rounds {
		var cur []uint16
		for i := range perRound {
			n := uint16(1 + r*perRound + i)
			cur = append(cur, n)
			body := fmt.Sprintf("row %d", n)
			want = append(want, body)
			store.add(liveLogRow{span: n, body: body})
		}
		for _, n := range cur {
			l.subscribe(idtui.LogRequest{Span: liveTestSpan(n)})
		}
		// The previous round finishes as this one runs.
		activity.set(true, prev...)
		waitIdle(t, l, prev...)
		clock.advance(l.grace)
		waitFollowing(t, store, cur...)
		prev = cur
	}
	require.Eventually(t, func() bool { return len(out.got()) == len(want) }, 10*time.Second, time.Millisecond)
	got := out.got()
	slices.Sort(got)
	slices.Sort(want)
	require.Equal(t, want, got, "every row once")
	var most int
	for _, q := range store.seen() {
		most = max(most, len(q.Spans))
	}
	require.LessOrEqual(t, most, 2*perRound, "a query selected spans that finished long ago")
}

type exitEvents struct {
	mu     sync.Mutex
	events []string
}

func (e *exitEvents) add(event string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, event)
}

func (e *exitEvents) got() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.events)
}

type fakeExitSession struct {
	events  *exitEvents
	barrier error
}

func (s fakeExitSession) TelemetryBarrier(context.Context) error {
	s.events.add("barrier")
	return s.barrier
}

func (s fakeExitSession) WaitTelemetryIdle(context.Context, time.Duration) {
	s.events.add("wait idle")
}

// fakeExitFrontend requests span 2 for its final render, and takes span 9's
// output as the primary output.
type fakeExitFrontend struct {
	events  *exitEvents
	live    *liveLogs
	primary []string
}

func (f *fakeExitFrontend) LiveLogs() (bool, bool) { return true, false }
func (f *fakeExitFrontend) SetLiveLogs(dagui.TraceID, func(idtui.LogRequest)) sdklog.Exporter {
	return nil
}
func (f *fakeExitFrontend) RequestFinalLogs() {
	f.events.add("request final logs")
	f.live.subscribe(idtui.LogRequest{Span: liveTestSpan(2)})
}
func (f *fakeExitFrontend) PrimarySpan() dagui.SpanID { return liveTestSpan(9) }
func (f *fakeExitFrontend) SetPrimaryOutput(records []sdklog.Record) {
	f.events.add("set primary output")
	for _, rec := range records {
		f.primary = append(f.primary, rec.Body().AsString())
	}
}
func (f *fakeExitFrontend) LiveLogsCoverage([]idtui.LogTarget) []idtui.LogRoute { return nil }
func (f *fakeExitFrontend) LiveLogsActivity([]dagui.SpanID) []idtui.LogActivity { return nil }

// TestFinishLiveLogsInterrupted: against a hung engine, an interrupt during
// the exit ends it promptly -- the TUI's Ctrl-C canceling the run's context,
// or a SIGINT after the run was already canceled -- whether it hangs in the
// barrier or in the primary output's read, whose final render gets what was
// read so far. A run canceled before exit still gets its reads.
func TestFinishLiveLogsInterrupted(t *testing.T) {
	for _, tc := range []struct {
		name string
		// canceledRun cancels the run's context before exit; interrupting
		// is then a SIGINT, and otherwise canceling the run's context.
		canceledRun, hangBarrier, hangPrimary bool
	}{
		{name: "ctrl-c in barrier", hangBarrier: true},
		{name: "ctrl-c in primary read", hangPrimary: true},
		{name: "sigint in barrier", canceledRun: true, hangBarrier: true},
		{name: "sigint in primary read", canceledRun: true, hangPrimary: true},
		{name: "run canceled before exit", canceledRun: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sigintCtx, sigint := context.WithCancel(t.Context())
			oldInterrupts := exitInterrupts
			exitInterrupts = func(ctx context.Context) (context.Context, context.CancelFunc) {
				ctx, stop := context.WithCancel(ctx)
				stopAfter := context.AfterFunc(sigintCtx, stop)
				return ctx, func() { stopAfter(); stop() }
			}
			t.Cleanup(func() { exitInterrupts = oldInterrupts })

			events := &exitEvents{}
			hung := make(chan struct{}, 2)
			hang := func(ctx context.Context) error {
				hung <- struct{}{}
				<-ctx.Done()
				return ctx.Err()
			}
			sess := hangingExitSession{events: events}
			if tc.hangBarrier {
				sess.hang = hang
			}
			reader := logReaderFunc(func(ctx context.Context, _ string, q tracesource.LogQuery, cb func(context.Context, *collogspb.ExportLogsServiceRequest, string) error) error {
				span := q.Spans[0].SpanID
				events.add("read " + span[len(span)-2:])
				if span != liveTestSpan(9).String() {
					return nil
				}
				if err := cb(ctx, liveLogRequest([]liveLogRow{{span: 9, body: "partial"}}), "1"); err != nil {
					return err
				}
				if tc.hangPrimary {
					return hang(ctx)
				}
				return nil
			})
			live := newLiveLogs(reader, "trace", &bodyCollector{}, false)
			fe := &fakeExitFrontend{events: events, live: live}
			live.frontend = fe

			runCtx, cancelRun := context.WithCancel(t.Context())
			defer cancelRun()
			interrupt := cancelRun
			if tc.canceledRun {
				cancelRun()
				interrupt = sigint
			}
			done := make(chan struct{})
			go func() {
				defer close(done)
				finishLiveLogs(runCtx, sess, live)
			}()
			if tc.hangBarrier || tc.hangPrimary {
				<-hung
				interrupt()
			}
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("exit not interrupted")
			}
			require.NotContains(t, events.got(), "wait idle", "no quiet wait once interrupted")
			require.Contains(t, events.got(), "set primary output")
			if !tc.hangBarrier {
				require.Equal(t, []string{"partial"}, fe.primary, "the primary output read so far is written")
			}
		})
	}
}

type hangingExitSession struct {
	events *exitEvents
	hang   func(context.Context) error
}

func (s hangingExitSession) TelemetryBarrier(ctx context.Context) error {
	s.events.add("barrier")
	if s.hang != nil {
		return s.hang(ctx)
	}
	return nil
}

func (s hangingExitSession) WaitTelemetryIdle(context.Context, time.Duration) {
	s.events.add("wait idle")
}

// TestFlushableSpanForwarder: the frontend's batching processors hand what
// the session delivered on when the barrier flushes the forwarder.
func TestFlushableSpanForwarder(t *testing.T) {
	frontend := tracetest.NewInMemoryExporter()
	batch := sdktrace.NewBatchSpanProcessor(frontend, sdktrace.WithBatchTimeout(time.Hour))
	t.Cleanup(func() { _ = batch.Shutdown(context.Background()) })
	fwd := flushableSpanForwarder{telemetry.SpanForwarder{Processors: []sdktrace.SpanProcessor{batch}}}
	sampled := trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{1}, TraceFlags: trace.FlagsSampled})
	require.NoError(t, fwd.ExportSpans(t.Context(), tracetest.SpanStubs{{Name: "just ended", SpanContext: sampled}}.Snapshots()))
	require.Empty(t, frontend.GetSpans())
	require.NoError(t, fwd.ForceFlush(t.Context()))
	require.Len(t, frontend.GetSpans(), 1)
}

type logReaderFunc func(context.Context, string, tracesource.LogQuery, func(context.Context, *collogspb.ExportLogsServiceRequest, string) error) error

func (f logReaderFunc) ReadLogs(ctx context.Context, traceID string, q tracesource.LogQuery, cb func(context.Context, *collogspb.ExportLogsServiceRequest, string) error) error {
	return f(ctx, traceID, q, cb)
}

// TestFinishLiveLogs: at exit, the barrier comes first -- with no quiet
// wait unless the engine lacks it or it fails -- then the final render's
// requests and their read, then the primary output, whose failed read is
// retried whole.
func TestFinishLiveLogs(t *testing.T) {
	oldDelay := liveLogsRetryDelay
	liveLogsRetryDelay = time.Millisecond
	t.Cleanup(func() { liveLogsRetryDelay = oldDelay })

	for _, tc := range []struct {
		name    string
		barrier error
		want    []string
	}{
		{name: "barrier", want: []string{"barrier"}},
		{name: "older engine", barrier: client.ErrTelemetryBarrierUnsupported, want: []string{"barrier", "wait idle"}},
		{name: "barrier fails", barrier: errors.New("boom"), want: []string{"barrier", "wait idle"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := &exitEvents{}
			primaryReads := 0
			reader := logReaderFunc(func(ctx context.Context, _ string, q tracesource.LogQuery, cb func(context.Context, *collogspb.ExportLogsServiceRequest, string) error) error {
				span := q.Spans[0].SpanID
				events.add("read " + span[len(span)-2:])
				switch span {
				case liveTestSpan(2).String():
					return cb(ctx, liveLogRequest([]liveLogRow{{span: 2, body: "failure"}}), "3")
				case liveTestSpan(9).String():
					primaryReads++
					if err := cb(ctx, liveLogRequest([]liveLogRow{{span: 9, body: "out"}}), "1"); err != nil {
						return err
					}
					if primaryReads == 1 {
						return errors.New("connection reset")
					}
				}
				return nil
			})
			out := &bodyCollector{}
			live := newLiveLogs(reader, "trace", out, false)
			fe := &fakeExitFrontend{events: events, live: live}
			live.frontend = fe

			finishLiveLogs(t.Context(), fakeExitSession{events: events, barrier: tc.barrier}, live)

			want := append(slices.Clone(tc.want),
				"request final logs",
				"read 02",
				"read 09",
				"read 09",
				"set primary output",
			)
			require.Equal(t, want, events.got())
			require.Equal(t, []string{"failure"}, out.got())
			require.Equal(t, []string{"out"}, fe.primary, "a retried read replaces the failed one's records")
		})
	}
}
