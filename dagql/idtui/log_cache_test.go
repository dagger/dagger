package idtui

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/stretchr/testify/require"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/trace"
)

// fakeLogClock makes each reading of the log cache's clock a second later,
// so the order spans were shown in is the order of the LRU.
func fakeLogClock(fe *frontendPretty, size int) {
	tick := time.Now()
	fe.logCache.now = func() time.Time {
		tick = tick.Add(time.Second)
		return tick
	}
	fe.logCache.size = size
	fe.logCache.grace = 0
}

// sweepInteractive sweeps a frontend the test drives in report mode (so its
// dispatch runs inline) as the interactive one it stands for.
func sweepInteractive(fe *frontendPretty) {
	fe.reportOnly = false
	defer func() { fe.reportOnly = true }()
	fe.sweepLogs()
}

// TestLogCacheBoundsManyLogHeavySpans: showing 1,000 log-heavy spans one at
// a time keeps only the shown span and the few recently shown ones in
// memory; showing an evicted span again reads it again, and shows its whole
// output exactly once.
func TestLogCacheBoundsManyLogHeavySpans(t *testing.T) {
	const spans, lines, cacheSize = 1000, 20, 8
	start := time.Unix(100, 0)
	traceID := prettyTestTraceID()
	rootID := prettyTestSpanID(1)
	src := &staticSource{
		id: traceID.String(),
		priority: []dagui.SpanSnapshot{{
			ID: rootID, TraceID: traceID, Name: "root",
			StartTime: start, EndTime: start.Add(time.Hour), Final: true,
		}},
		own: map[string][]traceLog{},
	}
	ids := make([]dagui.SpanID, spans)
	for i := range spans {
		var id dagui.SpanID
		binary.BigEndian.PutUint64(id.SpanID[:], uint64(0x1000+i))
		ids[i] = id
		src.priority = append(src.priority, dagui.SpanSnapshot{
			ID: id, TraceID: traceID, ParentID: rootID, Name: fmt.Sprintf("step %d", i),
			StartTime: start.Add(time.Duration(i) * time.Millisecond), EndTime: start.Add(time.Hour), HasLogs: true, Final: true,
		})
		var logs []traceLog
		for l := range lines {
			logs = append(logs, traceLog{SpanID: id.String(), Body: fmt.Sprintf("step %04d line %02d\n", i, l)})
		}
		src.own[id.String()] = logs
	}

	sess := newTraceSession(t, src, nil)
	var released []dagui.SpanID
	sess.fe.SetLogRelease(dagui.TraceID{}, func(r LogRelease) {
		// The provider fetches a dropped span again on its next request.
		for _, id := range r.Dropped {
			delete(sess.requestedLogs, id.String())
			released = append(released, id)
		}
	})
	sess.Render()
	fakeLogClock(sess.fe, cacheSize)

	for i, id := range ids {
		out := sess.Zoom(id)
		require.Contains(t, out, fmt.Sprintf("step %04d line %02d", i, lines-1), "the zoomed span's logs render")
		sess.fe.sweepLogs()
		// Shown: the zoomed span, and the root (the primary span).
		require.LessOrEqual(t, len(sess.fe.logs.Logs), cacheSize+2, "at most the LRU and the shown spans hold terminals (step %d)", i)
		require.LessOrEqual(t, len(sess.fe.requestedLogs), cacheSize+2, "at most the LRU and the shown spans stay requested (step %d)", i)
	}
	require.Len(t, released, spans-cacheSize-1, "every span past the LRU was dropped once")
	require.Nil(t, sess.fe.logs.Logs[ids[0]], "the first span's terminal was dropped")

	// Showing it again reads it again, from the start, once.
	before := strings.Count(strings.Join(sess.Network().logRequests, ","), ids[0].String())
	out := sess.Zoom(ids[0])
	require.Equal(t, before+1, strings.Count(strings.Join(sess.Network().logRequests, ","), ids[0].String()))
	require.Contains(t, out, "step 0000 line 19")
	text := vtermText(sess.fe, ids[0])
	for l := range lines {
		require.Equal(t, 1, strings.Count(text, fmt.Sprintf("step 0000 line %02d\n", l)), "line %d shown once:\n%s", l, text)
	}
}

// TestLogCacheDropsRollUpGroups: a roll-up's terminal holds its
// descendants' records too, so a roll-up and its descendants are evicted
// and dropped together, and showing the roll-up again rebuilds both from
// one read, with every line once.
func TestLogCacheDropsRollUpGroups(t *testing.T) {
	live := dagui.TraceID{TraceID: trace.TraceID{2}}
	primary, rollUp, child, other1, other2 := prettyTestSpanID(1), prettyTestSpanID(2), prettyTestSpanID(3), prettyTestSpanID(4), prettyTestSpanID(5)
	start := time.Unix(100, 0)
	ctx := context.Background()
	fe := NewWithDB(io.Discard, dagui.NewDB())
	fe.reportOnly = true // dispatch runs inline
	fe.ImportSnapshots([]dagui.SpanSnapshot{
		{ID: primary, TraceID: live, Name: "cmd", StartTime: start, Final: true},
		{ID: rollUp, TraceID: live, ParentID: primary, Name: "check", StartTime: start, RollUpLogs: true, Final: true},
		{ID: child, TraceID: live, ParentID: rollUp, Name: "exec", StartTime: start, Final: true},
		{ID: other1, TraceID: live, ParentID: primary, Name: "other 1", StartTime: start, Final: true},
		{ID: other2, TraceID: live, ParentID: primary, Name: "other 2", StartTime: start, Final: true},
	})
	fe.SetPrimary(primary)
	var reqs []LogRequest
	read := fe.SetLiveLogs(live, func(r LogRequest) { reqs = append(reqs, r) })
	var releases []LogRelease
	fe.SetLogRelease(live, func(r LogRelease) { releases = append(releases, r) })
	fakeLogClock(fe, 1)

	// The child is shown first: its lines land in its terminal, and a
	// prefixed copy in the roll-up's.
	fe.requestLogs(child)
	require.NoError(t, read.Export(ctx, []sdklog.Record{liveTestLog(live, child, "child line\n")}))
	fe.requestLogs(rollUp)
	require.NoError(t, read.Export(ctx, []sdklog.Record{liveTestLog(live, rollUp, "roll-up line\n")}))
	require.Equal(t, 1, strings.Count(vtermText(fe, rollUp), "child line"))

	// Two other spans shown since push the group out of an LRU of one.
	for _, id := range []dagui.SpanID{other1, other2} {
		fe.requestLogs(id)
		fe.touchLogs(id)
	}
	fe.FocusedSpan = dagui.SpanID{}
	sweepInteractive(fe)
	require.Len(t, releases, 1)
	require.ElementsMatch(t, []dagui.SpanID{child, rollUp, other1}, releases[0].Spans)
	require.ElementsMatch(t, []dagui.SpanID{child, rollUp}, releases[0].Dropped, "the group is dropped whole")
	require.Nil(t, fe.logs.Logs[rollUp])
	require.Nil(t, fe.logs.Logs[child])
	require.False(t, fe.requestedLogs[rollUp])

	// Shown again, the roll-up is read again, its subtree with it.
	reqs = nil
	fe.requestLogs(rollUp)
	require.Equal(t, []LogRequest{{Span: rollUp, Descendants: true}}, reqs)
	require.NoError(t, read.Export(ctx, []sdklog.Record{
		liveTestLog(live, child, "child line\n"),
		liveTestLog(live, rollUp, "roll-up line\n"),
	}))
	require.Equal(t, 1, strings.Count(vtermText(fe, rollUp), "child line"), vtermText(fe, rollUp))
	require.Equal(t, 1, strings.Count(vtermText(fe, rollUp), "roll-up line"))
	require.Equal(t, 1, strings.Count(vtermText(fe, child), "child line"))
}

// TestLogCachePinsShownSpans: a mounted view, the zoomed and focused spans
// and the pager's span are never dropped, however full the LRU.
func TestLogCachePinsShownSpans(t *testing.T) {
	live := dagui.TraceID{TraceID: trace.TraceID{2}}
	primary := prettyTestSpanID(1)
	start := time.Unix(100, 0)
	fe := NewWithDB(io.Discard, dagui.NewDB())
	fe.reportOnly = true
	snaps := []dagui.SpanSnapshot{{ID: primary, TraceID: live, Name: "cmd", StartTime: start, Final: true}}
	var spans []dagui.SpanID
	for i := range 6 {
		id := prettyTestSpanID(byte(10 + i))
		spans = append(spans, id)
		snaps = append(snaps, dagui.SpanSnapshot{ID: id, TraceID: live, ParentID: primary, Name: "step", StartTime: start, Final: true})
	}
	fe.ImportSnapshots(snaps)
	fe.SetPrimary(primary)
	read := fe.SetLiveLogs(live, func(LogRequest) {})
	fe.SetLogRelease(live, func(LogRelease) {})
	fakeLogClock(fe, 0)

	for _, id := range spans {
		fe.requestLogs(id)
		require.NoError(t, read.Export(context.Background(), []sdklog.Record{liveTestLog(live, id, "out\n")}))
	}
	mounted, zoomed, focused, paged := spans[0], spans[1], spans[2], spans[3]
	fe.mountLogs(mounted)
	fe.ZoomedSpan = zoomed
	fe.FocusedSpan = focused
	fe.logPager = &LogPagerView{SpanID: paged}
	sweepInteractive(fe)
	for _, id := range []dagui.SpanID{mounted, zoomed, focused, paged} {
		require.NotNil(t, fe.logs.Logs[id], "a shown span was dropped")
	}
	require.Nil(t, fe.logs.Logs[spans[4]])
	require.Nil(t, fe.logs.Logs[spans[5]])

	// Dismounted, it is let go too.
	fe.dismountLogs(mounted)
	sweepInteractive(fe)
	require.Nil(t, fe.logs.Logs[mounted])
}
