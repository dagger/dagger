package idtui

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func liveTestLog(traceID dagui.TraceID, span dagui.SpanID, body string, attrs ...otellog.KeyValue) sdklog.Record {
	rec := frontendTestLogRecord(span.SpanID, otellog.StringValue(body), attrs...)
	rec.SetTraceID(traceID.TraceID)
	return rec
}

func vtermText(fe *frontendPretty, id dagui.SpanID) string {
	vt := fe.logs.Logs[id]
	if vt == nil {
		return ""
	}
	var buf strings.Builder
	_ = vt.Print(&buf)
	return buf.String()
}

// TestLiveLogsReadOnDemand: once a live session reads display text on
// demand, the frontend asks for a span's logs as it renders them, renders
// only what is read for it, drops this process's own display text (the store
// returns it), and takes the primary output at exit instead of buffering it.
func TestLiveLogsReadOnDemand(t *testing.T) {
	live := dagui.TraceID{TraceID: trace.TraceID{2}}
	archived := dagui.TraceID{TraceID: trace.TraceID{1}}
	primary, step, old := prettyTestSpanID(1), prettyTestSpanID(2), prettyTestSpanID(3)
	start := time.Unix(100, 0)
	ctx := context.Background()

	db := dagui.NewDB()
	fe := NewWithDB(io.Discard, db)
	fe.reportOnly = true // dispatch runs inline
	fe.ImportSnapshots([]dagui.SpanSnapshot{
		{ID: primary, TraceID: live, Name: "cmd", StartTime: start, Final: true},
		{ID: step, TraceID: live, ParentID: primary, Name: "step", StartTime: start, Output: "xxh3:out", Final: true},
		{ID: old, TraceID: archived, Name: "old", StartTime: start, EndTime: start.Add(time.Second), Final: true},
	})
	fe.SetPrimary(primary)

	// Before the switch, this process's own display text renders as usual.
	require.NoError(t, fe.LogExporter().Export(ctx, []sdklog.Record{
		liveTestLog(live, step, "early\n"),
		liveTestLog(live, primary, "out 1\n"),
	}))
	require.Contains(t, vtermText(fe, step), "early")
	require.Len(t, db.PrimaryLogs[primary], 1)

	var reqs []LogRequest
	read := fe.SetLiveLogs(live, func(r LogRequest) { reqs = append(reqs, r) })
	require.Empty(t, vtermText(fe, step), "own display text rendered before the switch was kept")
	require.Empty(t, db.PrimaryLogs[primary])

	// Own display text is dropped; presence flags the span; another trace's
	// display text (a restored archive's) still renders.
	require.NoError(t, fe.LogExporter().Export(ctx, []sdklog.Record{
		liveTestLog(live, step, "own\n"),
		liveTestLog(live, primary, "out 2\n"),
		liveTestLog(archived, old, "history\n"),
	}))
	require.Empty(t, vtermText(fe, step))
	require.Contains(t, vtermText(fe, old), "history")
	require.Empty(t, db.PrimaryLogs[primary], "primary output buffered while read on demand")

	// A request carries the digest the span created; what is read renders,
	// verbose rows included.
	fe.requestLogs(step)
	require.Equal(t, []LogRequest{{Span: step, Digests: []string{"xxh3:out"}}}, reqs)
	require.NoError(t, read.Export(ctx, []sdklog.Record{
		liveTestLog(live, step, "read\n"),
		liveTestLog(live, step, "verbose\n", otellog.Bool("dagger.io/ui.verbose", true)),
	}))
	require.Contains(t, vtermText(fe, step), "read")
	require.Contains(t, vtermText(fe, step), "verbose")
	require.NotContains(t, vtermText(fe, step), "own")

	// The primary span is not requested eagerly: its output is read at exit.
	fe.recalculateViewLocked()
	for _, r := range reqs {
		require.NotEqual(t, primary, r.Span, "primary output requested during the run")
	}
	fe.SetPrimaryOutput([]sdklog.Record{
		liveTestLog(live, primary, "out 1\n"),
		liveTestLog(live, step, "not primary\n"),
		liveTestLog(live, primary, "out 2\n"),
	})
	var bodies []string
	for _, rec := range db.PrimaryLogs[primary] {
		bodies = append(bodies, rec.Body().AsString())
	}
	require.Equal(t, []string{"out 1\n", "out 2\n"}, bodies)
}

func TestLiveLogsPresenceFlagsHasLogs(t *testing.T) {
	live := dagui.TraceID{TraceID: trace.TraceID{2}}
	step := prettyTestSpanID(2)
	db := dagui.NewDB()
	fe := NewWithDB(io.Discard, db)
	fe.reportOnly = true
	fe.ImportSnapshots([]dagui.SpanSnapshot{{ID: step, TraceID: live, Name: "step", StartTime: time.Unix(100, 0), Final: true}})
	fe.SetLiveLogs(live, func(LogRequest) {})
	require.NoError(t, fe.LogExporter().Export(context.Background(), []sdklog.Record{
		liveTestLog(live, step, "", otellog.String(telemetryattrs.LogRoleAttr, telemetryattrs.LogRolePresence)),
	}))
	require.True(t, db.Spans.Map[step].HasLogs)
	require.Nil(t, fe.logs.Logs[step], "presence rendered")
}

// TestLiveLogsCoverage: a read may return more than was asked for; the
// frontend tells which records route to what it requested.
func TestLiveLogsCoverage(t *testing.T) {
	live := dagui.TraceID{TraceID: trace.TraceID{2}}
	rollUp, child, other, creator := prettyTestSpanID(1), prettyTestSpanID(2), prettyTestSpanID(3), prettyTestSpanID(4)
	start := time.Unix(100, 0)
	db := dagui.NewDB()
	fe := NewWithDB(io.Discard, db)
	fe.reportOnly = true
	fe.ImportSnapshots([]dagui.SpanSnapshot{
		{ID: rollUp, TraceID: live, Name: "check", StartTime: start, RollUpLogs: true, Final: true},
		{ID: child, TraceID: live, ParentID: rollUp, Name: "exec", StartTime: start, Final: true},
		{ID: other, TraceID: live, Name: "other", StartTime: start, Final: true},
		{ID: creator, TraceID: live, Name: "asService", StartTime: start, CallDigest: "xxh3:call", Output: "xxh3:svc", Final: true},
	})
	fe.SetLiveLogs(live, func(LogRequest) {})
	fe.requestLogs(rollUp)
	fe.requestLogs(creator)
	routes := fe.LiveLogsCoverage([]LogTarget{
		{Trace: live, Span: rollUp},
		{Trace: live, Span: child},
		{Trace: live, Span: other},
		{Trace: live, Span: other, Digest: "xxh3:svc"},
		{Trace: live, Span: prettyTestSpanID(9)},
		{Trace: live, Span: other, Digest: "xxh3:unknown"},
	})
	var got []LogCoverage
	for _, route := range routes {
		got = append(got, route.Coverage)
	}
	require.Equal(t, []LogCoverage{LogCovered, LogCovered, LogUncovered, LogCovered, LogUnknown, LogUnknown}, got)
	require.Equal(t, []dagui.SpanID{rollUp, child, other, creator}, []dagui.SpanID{routes[0].Span, routes[1].Span, routes[2].Span, routes[3].Span},
		"each route names the span it routes to")
}

// TestLiveLogsActivity: a span's display text may still grow while it runs,
// while a descendant it rolls up runs, while an effect it caused (e.g. the
// service whose stdio names the digest it created) runs, or before it is
// received; otherwise it is idle. An effect that ran since, however
// briefly, moves its Through.
func TestLiveLogsActivity(t *testing.T) {
	live := dagui.TraceID{TraceID: trace.TraceID{2}}
	running, done, rollUp, child, creator, service, creatorDone, stopped :=
		prettyTestSpanID(1), prettyTestSpanID(2), prettyTestSpanID(3), prettyTestSpanID(4),
		prettyTestSpanID(5), prettyTestSpanID(6), prettyTestSpanID(7), prettyTestSpanID(8)
	start := time.Unix(100, 0)
	end := start.Add(time.Second)
	db := dagui.NewDB()
	fe := NewWithDB(io.Discard, db)
	fe.reportOnly = true
	fe.ImportSnapshots([]dagui.SpanSnapshot{
		{ID: running, TraceID: live, Name: "running", StartTime: start, Final: true},
		{ID: done, TraceID: live, Name: "done", StartTime: start, EndTime: end, Final: true},
		{ID: rollUp, TraceID: live, Name: "check", StartTime: start, EndTime: end, RollUpLogs: true, Final: true},
		{ID: child, TraceID: live, ParentID: rollUp, Name: "exec", StartTime: start, Final: true},
		{ID: creator, TraceID: live, Name: "asService", StartTime: start, EndTime: end, Output: "xxh3:svc", Final: true},
		{ID: service, TraceID: live, Name: "exec server", StartTime: start, Service: true, Final: true,
			Links: []dagui.SpanLink{{SpanContext: dagui.SpanContext{TraceID: live, SpanID: creator}, Purpose: "cause"}}},
		{ID: creatorDone, TraceID: live, Name: "asService", StartTime: start, EndTime: end, Output: "xxh3:svc2", Final: true},
		{ID: stopped, TraceID: live, Name: "exec server", StartTime: start, EndTime: end, Service: true, Final: true,
			Links: []dagui.SpanLink{{SpanContext: dagui.SpanContext{TraceID: live, SpanID: creatorDone}, Purpose: "cause"}}},
	})
	fe.SetLiveLogs(live, func(LogRequest) {})
	active := func(ids ...dagui.SpanID) []bool {
		var got []bool
		for _, a := range fe.LiveLogsActivity(ids) {
			got = append(got, a.Active)
		}
		return got
	}
	require.Equal(t, []bool{true, false, true, true, false, true},
		active(running, done, rollUp, creator, creatorDone, prettyTestSpanID(9)))
	require.Equal(t, []LogActivity{{Through: end}}, fe.LiveLogsActivity([]dagui.SpanID{done}))

	// Once the descendant ends, so does the roll-up's activity.
	fe.ImportSnapshots([]dagui.SpanSnapshot{
		{ID: child, TraceID: live, ParentID: rollUp, Name: "exec", StartTime: start, EndTime: end, Final: true},
	})
	require.Equal(t, []bool{false}, active(rollUp))

	// An effect that arrives already ended -- a quick lazy evaluation --
	// is never seen running, but moves Through.
	later := end.Add(time.Minute)
	fe.ImportSnapshots([]dagui.SpanSnapshot{
		{ID: prettyTestSpanID(10), TraceID: live, Name: "exec", StartTime: later, EndTime: later.Add(time.Millisecond), Final: true,
			Links: []dagui.SpanLink{{SpanContext: dagui.SpanContext{TraceID: live, SpanID: done}, Purpose: "cause"}}},
	})
	require.Equal(t, []LogActivity{{Through: later.Add(time.Millisecond)}}, fe.LiveLogsActivity([]dagui.SpanID{done}))
}

// TestLiveLogsBufferAfterPrimaryOutput: what this process logs after the
// primary output was read at exit (e.g. a failing cleanup) is still written.
func TestLiveLogsBufferAfterPrimaryOutput(t *testing.T) {
	live := dagui.TraceID{TraceID: trace.TraceID{2}}
	primary := prettyTestSpanID(1)
	db := dagui.NewDB()
	fe := NewWithDB(io.Discard, db)
	fe.reportOnly = true
	fe.ImportSnapshots([]dagui.SpanSnapshot{{ID: primary, TraceID: live, Name: "cmd", StartTime: time.Unix(100, 0), Final: true}})
	fe.SetPrimary(primary)
	fe.SetLiveLogs(live, func(LogRequest) {})
	fe.SetPrimaryOutput([]sdklog.Record{liveTestLog(live, primary, "result\n")})
	require.NoError(t, fe.LogExporter().Export(context.Background(), []sdklog.Record{liveTestLog(live, primary, "cleanup failed\n")}))
	var bodies []string
	for _, rec := range db.PrimaryLogs[primary] {
		bodies = append(bodies, rec.Body().AsString())
	}
	require.Equal(t, []string{"result\n", "cleanup failed\n"}, bodies)
}

// TestLiveLogsRequestCollapsedRollUp: a collapsed top-level roll-up row
// (a service, a check) shows its rolled-up output while running, with no
// LogsView mounted to ask for it: it asks as it renders.
func TestLiveLogsRequestCollapsedRollUp(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	live := dagui.TraceID{TraceID: trace.TraceID{2}}
	rootID, rollUp := prettyTestSpanID(1), prettyTestSpanID(2)
	start := time.Unix(100, 0)
	db := dagui.NewDB()
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{ID: rootID, TraceID: live, Name: "root", StartTime: start, Final: true},
		{ID: rollUp, TraceID: live, ParentID: rootID, Name: "service", StartTime: start, RollUpLogs: true, Final: true},
	})
	db.SetPrimarySpan(rootID)
	fe := NewWithDB(io.Discard, db)
	fe.FrontendOpts.GCThreshold = time.Hour
	fe.ZoomedSpan = rootID
	got := map[dagui.SpanID]bool{}
	fe.traceLogProviders = map[dagui.TraceID]func(dagui.SpanID, bool){
		live: func(id dagui.SpanID, _ bool) { got[id] = true },
	}
	fe.SpanExpanded = map[dagui.SpanID]bool{rollUp: false}
	fe.recalculateViewLocked()
	_ = fe.tui.RenderLines()
	require.True(t, got[rollUp], "the collapsed roll-up's logs were not requested; got %v", got)
}

// TestRequestFinalLogsSurfacesFailures: the interactive final render has no
// later pass, so at exit the frontend requests the failures it surfaces, as
// the report does.
func TestRequestFinalLogsSurfacesFailures(t *testing.T) {
	live := dagui.TraceID{TraceID: trace.TraceID{2}}
	primary, failed := prettyTestSpanID(1), prettyTestSpanID(2)
	start := time.Unix(100, 0)
	db := dagui.NewDB()
	fe := NewWithDB(io.Discard, db)
	fe.reportOnly = true
	fe.ImportSnapshots([]dagui.SpanSnapshot{
		{ID: primary, TraceID: live, Name: "cmd", StartTime: start, EndTime: start.Add(2 * time.Second), Final: true},
		{ID: failed, TraceID: live, ParentID: primary, Name: "boom", StartTime: start, EndTime: start.Add(time.Second),
			Status: sdktrace.Status{Code: codes.Error, Description: "boom"}, Final: true},
	})
	fe.SetPrimary(primary)
	var reqs []dagui.SpanID
	fe.SetLiveLogs(live, func(r LogRequest) { reqs = append(reqs, r.Span) })
	fe.reportOnly = false // the interactive frontend, from here on
	fe.recalculateViewLocked()
	require.NotContains(t, reqs, failed, "interactive mode fetched a collapsed failure")
	fe.finalLogs = true
	fe.recalculateViewLocked()
	require.Contains(t, reqs, failed)
}
