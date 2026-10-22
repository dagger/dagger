package daggercmd

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	otlplogsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	otlpresourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/archive"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/internal/tracesource"
)

type liveLogRow struct {
	span   byte
	body   string
	digest string
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
			SpanId:  []byte{0, 0, 0, 0, 0, 0, 0, r.span},
			Body:    &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_StringValue{StringValue: r.body}},
		}
		rec.TraceId[15] = 1
		if r.digest != "" {
			rec.Attributes = append(rec.Attributes, &otlpcommonv1.KeyValue{
				Key:   telemetry.DagDigestAttr,
				Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_StringValue{StringValue: r.digest}},
			})
		}
		records = append(records, rec)
	}
	return &collogspb.ExportLogsServiceRequest{ResourceLogs: []*otlplogsv1.ResourceLogs{{
		Resource:  &otlpresourcev1.Resource{},
		ScopeLogs: []*otlplogsv1.ScopeLogs{{Scope: &otlpcommonv1.InstrumentationScope{}, LogRecords: records}},
	}}}
}

type bodyCollector struct {
	mu     sync.Mutex
	bodies []string
}

func (c *bodyCollector) Export(_ context.Context, records []sdklog.Record) error {
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

func liveTestSpan(b byte) dagui.SpanID {
	var id dagui.SpanID
	id.SpanID[7] = b
	return id
}

// TestLiveLogsFollowCatchesUpJoiningSpans: one follow reads the set; a span
// joining it is caught up to the follow's cursor -- importing none of what
// the follow already imported -- and the follow resumes over the grown set
// from that cursor.
func TestLiveLogsFollowCatchesUpJoiningSpans(t *testing.T) {
	parent, child := liveTestSpan(1), liveTestSpan(2)
	reader := &fakeLogReader{respond: func(q tracesource.LogQuery) []liveLogBatch {
		switch {
		case q.Follow && q.Cursor == "":
			// The parent rolls up its child: the follow reads both.
			return []liveLogBatch{{rows: []liveLogRow{{span: 1, body: "p1"}, {span: 2, body: "c1"}}, cursor: "3"}}
		case !q.Follow && q.Through == "3":
			// The catch-up re-reads the child's rows the follow read, and
			// a digest-routed row only the child selects.
			return []liveLogBatch{{rows: []liveLogRow{{span: 2, body: "c1"}, {span: 9, body: "svc", digest: "xxh3:out"}}, cursor: "3"}}
		case q.Follow && q.Cursor == "3" && len(q.Spans) == 2:
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
	l.subscribe(idtui.LogRequest{Span: child, Digests: []string{"xxh3:out"}})
	require.Eventually(t, func() bool { return len(out.got()) == 4 }, 5*time.Second, 10*time.Millisecond)
	require.Equal(t, []string{"p1", "c1", "svc", "c2"}, out.got(), "every row once, in order")

	queries := reader.seen()
	require.Len(t, queries, 3)
	require.Equal(t, tracesource.LogQuery{
		Spans:   []archive.LogSpan{{SpanID: child.String(), Digests: []string{"xxh3:out"}}},
		Through: "3",
	}, queries[1], "the newcomer is caught up to the follow's cursor")
	require.Equal(t, tracesource.LogQuery{
		Spans:  []archive.LogSpan{{SpanID: parent.String(), Descendants: true}, {SpanID: child.String(), Digests: []string{"xxh3:out"}}},
		Follow: true,
		Cursor: "3",
	}, queries[2], "the follow resumes over the grown set from its cursor")
}

// TestLiveLogsUnknownSpansStayUnclaimed: a follow over a subtree also returns
// rows of a span the engine has not placed yet, then stops once it is placed
// elsewhere. Those rows are imported without claiming the span, so rendering
// it later catches up all its rows rather than only the ones the follow saw;
// rows the frontend did not ask for are dropped.
func TestLiveLogsUnknownSpansStayUnclaimed(t *testing.T) {
	reader := &fakeLogReader{respond: func(q tracesource.LogQuery) []liveLogBatch {
		switch {
		case q.Follow && q.Cursor == "":
			return []liveLogBatch{{rows: []liveLogRow{{span: 1, body: "p1"}, {span: 9, body: "u1"}, {span: 7, body: "noise"}}, cursor: "3"}}
		case q.Through == "3":
			return []liveLogBatch{{rows: []liveLogRow{{span: 9, body: "u1"}, {span: 9, body: "u2"}}, cursor: "3"}}
		}
		return nil
	}}
	out := &bodyCollector{}
	l := newLiveLogs(reader, "trace", out, true)
	var mu sync.Mutex
	requested := map[byte]bool{}
	l.coverage = func(targets []idtui.LogTarget) []idtui.LogCoverage {
		mu.Lock()
		defer mu.Unlock()
		var got []idtui.LogCoverage
		for _, target := range targets {
			switch span := target.Span.SpanID[7]; {
			case requested[span] || span == 1:
				got = append(got, idtui.LogCovered)
			case span == 9:
				got = append(got, idtui.LogUnknown)
			default:
				got = append(got, idtui.LogUncovered)
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
	require.Eventually(t, func() bool { return len(out.got()) == 4 }, 5*time.Second, 10*time.Millisecond)
	require.Equal(t, []string{"p1", "u1", "u1", "u2"}, out.got(), "the race repeats a line rather than losing the rest")
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
		case q.Follow:
			return []liveLogBatch{{rows: []liveLogRow{{span: 1, body: "a1"}}, cursor: "4"}}
		case q.Through == "4":
			return []liveLogBatch{{rows: []liveLogRow{{span: 2, body: "b1"}}, cursor: "4"}}
		case q.Cursor == "4":
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
func (f *fakeExitFrontend) LiveLogsCoverage([]idtui.LogTarget) []idtui.LogCoverage { return nil }

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
