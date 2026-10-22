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
