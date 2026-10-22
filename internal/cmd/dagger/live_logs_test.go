package daggercmd

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
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
	span   uint16
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
	l.coverage = func(targets []idtui.LogTarget) []idtui.LogCoverage {
		asked++
		coverage := make([]idtui.LogCoverage, len(targets))
		for i, target := range targets {
			coverage[i] = idtui.LogUncovered
			if target.Span == liveTestSpan(1) {
				coverage[i] = idtui.LogCovered
			}
		}
		return coverage
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

// fakeLogStore is a live store: rows are appended, a query selects them by
// span (with its descendants, by parents) or digest, a cursor is a row
// number, and a follow polls for the rows stored since until it is
// cancelled.
type fakeLogStore struct {
	parents map[uint16]uint16

	mu      sync.Mutex
	rows    []liveLogRow
	queries []tracesource.LogQuery
}

func (s *fakeLogStore) add(rows ...liveLogRow) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows = append(s.rows, rows...)
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
	for _, span := range q.Spans {
		spans[span.SpanID] = true
		descendants[span.SpanID] = span.Descendants
		for _, d := range span.Digests {
			digests[d] = true
		}
	}
	selects := func(r liveLogRow) bool {
		if spans[liveTestSpan(r.span).String()] || (r.digest != "" && digests[r.digest]) {
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
func (f *fakeExitFrontend) LiveLogsCoverage([]idtui.LogTarget) []idtui.LogCoverage { return nil }
func (f *fakeExitFrontend) LiveLogsActivity([]dagui.SpanID) []idtui.LogActivity { return nil }

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
