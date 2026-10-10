package idtui

import (
	"bytes"
	"io"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
	"github.com/vito/tuist"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/dagger/dagger/dagql/dagui"
)

// TestErrorCauseBreadcrumbBeneathCollapsedRow covers viztest's fail-multi
// shape: an error origin deep beneath a collapsed row, whose tree the view
// doesn't build. The breadcrumb leading to it must still read as its tree's
// parents: its shown parents up to the row, nearest the row first, leaving
// out the passthrough one.
func TestErrorCauseBreadcrumbBeneathCollapsedRow(t *testing.T) {
	start := time.Unix(100, 0)
	end := start.Add(time.Second)
	id := prettyTestSpanID
	failed := sdktrace.Status{Code: codes.Error, Description: "exit code: 1"}
	db := dagui.NewDB()
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{ID: id(1), TraceID: prettyTestTraceID(), Name: "dagger call", StartTime: start, EndTime: end},
		{ID: id(2), TraceID: prettyTestTraceID(), ParentID: id(1), Name: "failMulti", StartTime: start, EndTime: end, Status: failed},
		{ID: id(3), TraceID: prettyTestTraceID(), ParentID: id(2), Name: "roll-up", RollUpSpans: true, StartTime: start, EndTime: end, Status: failed},
		{ID: id(4), TraceID: prettyTestTraceID(), ParentID: id(3), Name: "sub-thing 1", StartTime: start, EndTime: end, Status: failed},
		{ID: id(5), TraceID: prettyTestTraceID(), ParentID: id(4), Name: "POST /query", Passthrough: true, StartTime: start, EndTime: end},
		{ID: id(6), TraceID: prettyTestTraceID(), ParentID: id(5), Name: "withExec", StartTime: start, EndTime: end, Status: failed},
	})
	db.Spans.Map[id(2)].AddErrorOrigin(db.Spans.Map[id(6)])
	db.SetPrimarySpan(id(1))
	fe := newWithTerminal(io.Discard, db, tuist.NewHeadlessTerminal(120, 30))
	fe.FrontendOpts.Verbosity = dagui.ShowCompletedVerbosity
	var output bytes.Buffer
	require.NoError(t, fe.FinalRender(&output))
	require.Contains(t, ansi.Strip(output.String()), "roll-up › sub-thing 1 › \n")
}
