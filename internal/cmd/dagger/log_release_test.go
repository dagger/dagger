package daggercmd

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/archive"
	"github.com/dagger/dagger/internal/tracesource"
)

func startTestLiveLogs(t *testing.T, l *liveLogs) {
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); l.run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
}

// TestLiveLogsReleaseRereadsDroppedSpans: a span the frontend dropped
// leaves the follow, and its next request reads it again from the start;
// a span it only stopped requesting, keeping what it holds, is not read
// twice.
func TestLiveLogsReleaseRereadsDroppedSpans(t *testing.T) {
	reader := &fakeLogReader{respond: func(q tracesource.LogQuery) []liveLogBatch {
		switch {
		case q.Follow && q.Cursor == "":
			return []liveLogBatch{{rows: []liveLogRow{{span: 1, body: "a1"}, {span: 2, body: "b1"}}, cursor: "2"}}
		case q.Through == "2":
			var rows []liveLogRow
			for _, s := range q.Spans {
				switch s.SpanID {
				case liveTestSpan(1).String():
					rows = append(rows, liveLogRow{span: 1, body: "a1"})
				case liveTestSpan(2).String():
					rows = append(rows, liveLogRow{span: 2, body: "b1"})
				}
			}
			return []liveLogBatch{{rows: rows, cursor: "2"}}
		case q.Follow && q.Cursor == "2" && len(q.Spans) == 2:
			return []liveLogBatch{{rows: []liveLogRow{{span: 1, body: "a2"}, {span: 2, body: "b2"}}, cursor: "4"}}
		}
		return nil
	}}
	out := &bodyCollector{}
	l := newLiveLogs(reader, "trace", out, true)
	l.subscribe(idtui.LogRequest{Span: liveTestSpan(1)})
	l.subscribe(idtui.LogRequest{Span: liveTestSpan(2)})
	startTestLiveLogs(t, l)
	require.Eventually(t, func() bool { return len(out.got()) == 2 }, 5*time.Second, 10*time.Millisecond)

	// Span 1 is dropped, span 2 only unsubscribed: both leave the follow.
	l.release(idtui.LogRelease{Spans: []dagui.SpanID{liveTestSpan(1), liveTestSpan(2)}, Dropped: []dagui.SpanID{liveTestSpan(1)}})
	require.Eventually(t, func() bool {
		l.mu.Lock()
		defer l.mu.Unlock()
		return len(l.spans) == 0
	}, 5*time.Second, 10*time.Millisecond)
	l.subscribe(idtui.LogRequest{Span: liveTestSpan(1)})
	l.subscribe(idtui.LogRequest{Span: liveTestSpan(2)})
	require.Eventually(t, func() bool { return len(out.got()) == 5 }, 5*time.Second, 10*time.Millisecond)
	require.Equal(t, []string{"a1", "b1", "a1", "a2", "b2"}, out.got(),
		"the dropped span is read again from the start, the kept one is not")
}

// TestLiveLogsReleaseDropsInFlightRows: rows of a span dropped while a read
// is in flight are not imported: its next request reads them.
func TestLiveLogsReleaseDropsInFlightRows(t *testing.T) {
	var l *liveLogs
	var once sync.Once
	reader := &fakeLogReader{respond: func(q tracesource.LogQuery) []liveLogBatch {
		if !q.Follow || q.Cursor != "" {
			return nil
		}
		once.Do(func() { l.release(idtui.LogRelease{Dropped: []dagui.SpanID{liveTestSpan(1)}}) })
		return []liveLogBatch{{rows: []liveLogRow{{span: 1, body: "a1"}, {span: 2, body: "b1"}}, cursor: "2"}}
	}}
	out := &bodyCollector{}
	l = newLiveLogs(reader, "trace", out, true)
	l.subscribe(idtui.LogRequest{Span: liveTestSpan(1)})
	l.subscribe(idtui.LogRequest{Span: liveTestSpan(2)})
	startTestLiveLogs(t, l)
	require.Eventually(t, func() bool { return len(out.got()) == 1 }, 5*time.Second, 10*time.Millisecond)
	require.Equal(t, []string{"b1"}, out.got())
	l.mu.Lock()
	defer l.mu.Unlock()
	require.NotContains(t, l.owned, logOwner{span: liveTestSpan(1).SpanID}, "a dropped span's rows are not claimed")
}

// TestLiveLogsReleaseRetiredSpan: a span released after it retired is
// forgotten like a followed one -- no longer watched, so work it includes
// starting again does not bring it back, and the pairs its retirement
// reserved for its gap read are forgotten, whether the frontend dropped its
// text or only stopped requesting it -- and requested again, it is caught up
// from the start like a new request, then followed.
func TestLiveLogsReleaseRetiredSpan(t *testing.T) {
	for _, tc := range []struct {
		name    string
		release idtui.LogRelease
	}{
		{name: "dropped", release: idtui.LogRelease{Spans: []dagui.SpanID{liveTestSpan(5)}, Dropped: []dagui.SpanID{liveTestSpan(5)}}},
		{name: "unrequested", release: idtui.LogRelease{Spans: []dagui.SpanID{liveTestSpan(5)}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
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

			id := liveTestSpan(5).String()
			pair := logOwner{span: liveTestSpan(5).SpanID, digest: "xxh3:svc"}
			l.mu.Lock()
			r, retired := l.retired[id]
			claim := l.owned[pair]
			l.mu.Unlock()
			require.True(t, retired)
			require.Equal(t, r.read, claim.read, "the pair passed to the gap read")

			l.release(tc.release)
			l.mu.Lock()
			require.NotContains(t, l.retired, id)
			require.NotContains(t, l.reactivated, id)
			require.NotContains(t, l.idleSince, id)
			require.NotContains(t, l.through, id)
			require.NotContains(t, l.owned, pair, "the gap read's pairs are forgotten")
			l.mu.Unlock()

			// The service restarts: a released span is not watched, so it
			// is not read, nor followed again.
			n := len(store.seen())
			store.add(liveLogRow{span: 5, body: "run2", digest: "xxh3:svc"}, liveLogRow{span: 6, body: "c2"})
			activity.set(false, 5)
			activity.ran(5, time.Unix(5000, 0))
			require.Eventually(t, func() bool { return slices.Contains(out.got(), "c2") }, 5*time.Second, time.Millisecond)
			require.Never(t, func() bool {
				l.mu.Lock()
				defer l.mu.Unlock()
				_, followed := l.spans[id]
				_, reactivated := l.reactivated[id]
				return followed || reactivated || l.reactivating == id
			}, 100*time.Millisecond, 5*time.Millisecond)
			for _, q := range store.seen()[n:] {
				require.NotContains(t, querySpanIDs(q), id, "the released span was read")
			}

			// Requested again, it is a new request: caught up from the
			// start, not from where it retired, then followed.
			l.subscribe(idtui.LogRequest{Span: liveTestSpan(5)})
			waitFollowing(t, store, 5, 6)
			store.add(liveLogRow{span: 5, body: "run3", digest: "xxh3:svc"})
			require.Eventually(t, func() bool { return slices.Contains(out.got(), "run3") }, 5*time.Second, time.Millisecond)
			require.Equal(t, []string{"run1", "c1", "c2", "run1", "run2", "run3"}, out.got())
		})
	}
}

// TestLogSearchFunc: a frontend's search is a case-insensitive substring
// search of the store, its matches deduplicated by what they route by.
func TestLogSearchFunc(t *testing.T) {
	var got archive.LogSearch
	search := logSearchFunc(func(_ context.Context, s archive.LogSearch) (archive.LogSearchResult, error) {
		got = s
		return archive.LogSearchResult{Matches: []archive.LogMatch{
			{TraceID: "01000000000000000000000000000000", SpanID: "0000000000000001", Row: 1},
			{TraceID: "01000000000000000000000000000000", SpanID: "0000000000000001", Row: 2},
			{TraceID: "01000000000000000000000000000000", SpanID: "0000000000000009", Digest: "xxh3:out", Row: 3},
			{TraceID: "bogus", SpanID: "0000000000000002", Row: 4},
		}}, nil
	})
	targets, err := search(t.Context(), "Needle")
	require.NoError(t, err)
	require.Equal(t, archive.LogSearch{Pattern: "Needle", IgnoreCase: true, Limit: archive.MaxLogSearchLimit}, got)
	require.Len(t, targets, 2)
	require.Equal(t, liveTestSpan(1), targets[0].Span)
	require.Equal(t, "xxh3:out", targets[1].Digest)
}

// TestTraceLoaderForgetLogs: spans the frontend dropped are fetched again on
// their next request, a roll-up with its subtree, rather than staying owned
// by the fetch that imported them first.
func TestTraceLoaderForgetLogs(t *testing.T) {
	rollUp, child := []byte{1, 0, 0, 0, 0, 0, 0, 1}, []byte{2, 0, 0, 0, 0, 0, 0, 2}
	id := func(b []byte) dagui.SpanID {
		var id dagui.SpanID
		copy(id.SpanID[:], b)
		return id
	}
	text := func(span []byte) *logspb.LogRecord {
		return &logspb.LogRecord{SpanId: span, Body: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "out\n"}}}
	}
	source := &logSource{records: map[string][]*logspb.LogRecord{
		id(child).String():  {text(child)},
		id(rollUp).String(): {text(rollUp), text(child)},
	}}
	loader, _, _ := testTraceLoader(t)
	loader.client = source
	loader.fullTree = true

	var fg fetchGroup
	loader.fetchLogs(&fg, id(rollUp), true)
	require.NoError(t, fg.Wait())
	loader.fetchLogs(&fg, id(child), false)
	require.NoError(t, fg.Wait())
	require.Len(t, source.sels, 1, "the roll-up owns its child")

	loader.forgetLogs(idtui.LogRelease{Spans: []dagui.SpanID{id(rollUp)}, Dropped: []dagui.SpanID{id(rollUp), id(child)}})
	require.Empty(t, loader.logReq)
	loader.fetchLogs(&fg, id(rollUp), true)
	require.NoError(t, fg.Wait())
	require.Len(t, source.sels, 2)
	require.Equal(t, [][]byte{rollUp, child, rollUp, child}, source.kept, "the dropped group is imported again, once")
	require.Empty(t, loader.logReleased, "nothing in flight, nothing to drop")
}
