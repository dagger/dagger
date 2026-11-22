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

// TestErrorCauseBreadcrumbFollowsVisibility covers a module build failure:
// the breadcrumb leaves out the internal entrypoint plumbing, and the shown
// build span, which encapsulates its work, stands for the calls beneath it.
// The internal plumbing encapsulates its work too, which doesn't count.
func TestErrorCauseBreadcrumbFollowsVisibility(t *testing.T) {
	start := time.Unix(100, 0)
	end := start.Add(time.Second)
	id := prettyTestSpanID
	failed := sdktrace.Status{Code: codes.Error, Description: "exit code: 2"}
	db := dagui.NewDB()
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{ID: id(1), TraceID: prettyTestTraceID(), Name: "dagger call", StartTime: start, EndTime: end},
		{ID: id(2), TraceID: prettyTestTraceID(), ParentID: id(1), Name: "viztestGo", StartTime: start, EndTime: end, Status: failed},
		{ID: id(3), TraceID: prettyTestTraceID(), ParentID: id(2), Name: "call module entrypoint", Internal: true, Encapsulate: true, StartTime: start, EndTime: end, Status: failed},
		{ID: id(4), TraceID: prettyTestTraceID(), ParentID: id(3), Name: "build viztest-go", Encapsulate: true, StartTime: start, EndTime: end, Status: failed},
		{ID: id(5), TraceID: prettyTestTraceID(), ParentID: id(4), Name: "binary", StartTime: start, EndTime: end, Status: failed},
		{ID: id(6), TraceID: prettyTestTraceID(), ParentID: id(5), Name: "gobuild", StartTime: start, EndTime: end, Status: failed},
		{ID: id(7), TraceID: prettyTestTraceID(), ParentID: id(6), Name: "withExec", StartTime: start, EndTime: end, Status: failed},
	})
	db.Spans.Map[id(2)].AddErrorOrigin(db.Spans.Map[id(7)])
	db.SetPrimarySpan(id(1))
	fe := newWithTerminal(io.Discard, db, tuist.NewHeadlessTerminal(120, 30))
	fe.FrontendOpts.Verbosity = dagui.ShowCompletedVerbosity
	var output bytes.Buffer
	require.NoError(t, fe.FinalRender(&output))
	out := ansi.Strip(output.String())
	require.Contains(t, out, VertBoldDash3+" build viztest-go › \n")
	require.NotContains(t, out, "call module entrypoint")
	require.NotContains(t, out, "binary")
	require.NotContains(t, out, "gobuild")
}

// TestErrorCauseBreadcrumbKeepsModuleLoad covers a module whose runtime
// fails to build while the workspace loads: the breadcrumb names the module's
// load, beneath the workspace's, which doesn't stand for it, and leaves out
// the load's plumbing, whose encapsulation still stands for the build's calls.
func TestErrorCauseBreadcrumbKeepsModuleLoad(t *testing.T) {
	start := time.Unix(100, 0)
	end := start.Add(time.Second)
	id := prettyTestSpanID
	failed := sdktrace.Status{Code: codes.Error, Description: "exit code: 2"}
	db := dagui.NewDB()
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{ID: id(1), TraceID: prettyTestTraceID(), Name: "dagger call", StartTime: start, EndTime: end},
		{ID: id(2), TraceID: prettyTestTraceID(), ParentID: id(1), Name: "load workspace: .", StartTime: start, EndTime: end, Status: failed},
		{ID: id(3), TraceID: prettyTestTraceID(), ParentID: id(2), Name: dagui.WorkspaceLoadSpanName, Encapsulate: true, StartTime: start, EndTime: end, Status: failed},
		{ID: id(4), TraceID: prettyTestTraceID(), ParentID: id(3), Name: "load module: ./broken", StartTime: start, EndTime: end, Status: failed},
		{ID: id(5), TraceID: prettyTestTraceID(), ParentID: id(4), Name: "ModuleSource.asModule", StartTime: start, EndTime: end, Status: failed},
		{ID: id(6), TraceID: prettyTestTraceID(), ParentID: id(5), Name: "module SDK: load runtime", Encapsulate: true, StartTime: start, EndTime: end, Status: failed},
		{ID: id(7), TraceID: prettyTestTraceID(), ParentID: id(6), Name: "binary", StartTime: start, EndTime: end, Status: failed},
		{ID: id(8), TraceID: prettyTestTraceID(), ParentID: id(7), Name: "withExec", StartTime: start, EndTime: end, Status: failed},
	})
	db.Spans.Map[id(2)].AddErrorOrigin(db.Spans.Map[id(8)])
	db.SetPrimarySpan(id(1))
	fe := newWithTerminal(io.Discard, db, tuist.NewHeadlessTerminal(120, 30))
	fe.FrontendOpts.Verbosity = dagui.ShowCompletedVerbosity
	var output bytes.Buffer
	require.NoError(t, fe.FinalRender(&output))
	out := ansi.Strip(output.String())
	require.Contains(t, out, VertBoldDash3+" "+dagui.WorkspaceLoadSpanName+" › load module: ./broken › \n")
	require.NotContains(t, out, "ModuleSource.asModule")
	require.NotContains(t, out, "module SDK: load runtime")
	require.NotContains(t, out, "binary")
}
