package dagui

import (
	"testing"
	"time"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// TestInternalFailureGivesPlaceToItsCause covers a module whose runtime
// fails to compile. The SDK builds the runtime, within its encapsulating
// span, through internal calls the view hides, but a failed span is shown
// even when it's internal, so every internal call between the module's load
// and the failed compile was shown, and with it everything beneath it. The
// internal calls that only pass the compile's failure on give their place to
// it. A failed internal span outside the SDK's encapsulation, such as the
// engine's own load of a dependency, still shows, even within the CLI's
// encapsulation of module loading as a whole, as does one with no failure
// beneath it, and all of it at -vvv.
func TestInternalFailureGivesPlaceToItsCause(t *testing.T) {
	traceID := TraceID{TraceID: trace.TraceID{1}}
	primaryID := testID(1)
	runtimeID := testID(2)
	sdkCallID := testID(3)
	processID := testID(4)
	siblingID := testID(5)
	binaryID := testID(6)
	compileID := testID(7)
	execRunID := testID(8)
	getModDefID := testID(9)
	queryID := testID(10)
	depsID := testID(11)
	extraID := testID(12)

	start := time.Now().Add(-time.Minute)
	end := start.Add(time.Second)
	ok := sdktrace.Status{Code: codes.Ok}
	failed := sdktrace.Status{Code: codes.Error, Description: "./main.go:6:6: undefined: ctx"}

	db := NewDB()
	db.ImportSnapshots([]SpanSnapshot{
		// The CLI encapsulates module loading as a whole.
		{ID: primaryID, TraceID: traceID, Name: "loading type definitions", StartTime: start, EndTime: end, Status: failed,
			Encapsulate: true},
		// A step of its own, and the engine's own internal step within it,
		// outside the SDK's encapsulation.
		{ID: extraID, TraceID: traceID, ParentID: primaryID, Name: "load extra module: ./broken-dep",
			StartTime: start, EndTime: end, Status: failed},
		{ID: depsID, TraceID: traceID, ParentID: extraID, Name: "load dep modules",
			StartTime: start, EndTime: end, Status: failed, Internal: true},
		{ID: runtimeID, TraceID: traceID, ParentID: depsID, Name: "module SDK: load runtime",
			StartTime: start, EndTime: end, Status: failed, Encapsulate: true},
		{ID: sdkCallID, TraceID: traceID, ParentID: runtimeID, Name: "GoSdk.moduleRuntime",
			StartTime: start, EndTime: end, Status: failed, Internal: true},
		{ID: processID, TraceID: traceID, ParentID: sdkCallID, Name: "go-sdk:GoSdk.moduleRuntime",
			StartTime: start, EndTime: end, Status: failed, Passthrough: true},
		// The engine's span for a request ends OK even when a call under it
		// failed.
		{ID: queryID, TraceID: traceID, ParentID: processID, Name: "POST /query",
			StartTime: start, EndTime: end, Status: ok, Passthrough: true},
		{ID: siblingID, TraceID: traceID, ParentID: queryID, Name: "Directory.withoutFile",
			StartTime: start, EndTime: end, Status: ok, Internal: true},
		{ID: binaryID, TraceID: traceID, ParentID: queryID, Name: "GoPackage.binary",
			StartTime: start, EndTime: end, Status: failed, Internal: true},
		{ID: compileID, TraceID: traceID, ParentID: binaryID, Name: "withExec sh /gobuild/build.sh dagger/broken",
			StartTime: start, EndTime: end, Status: failed, Internal: true},
		{ID: execRunID, TraceID: traceID, ParentID: compileID, Name: "exec.run",
			StartTime: start, EndTime: end, Status: failed, Passthrough: true},
		{ID: getModDefID, TraceID: traceID, ParentID: extraID, Name: "asModule getModDef",
			StartTime: start, EndTime: end, Status: failed, Internal: true},
	})
	db.SetPrimarySpan(primaryID)

	view := db.RowsView(FrontendOpts{ZoomedSpan: primaryID, Verbosity: ShowCompletedVerbosity})
	home := func(view *RowsView, id SpanID) *TraceTree {
		return view.HomeTree(db.Spans.Map[id])
	}
	compile := home(view, compileID)
	if compile == nil {
		t.Fatal("the failed compile is not shown")
	}
	if compile.Parent == nil || compile.Parent.Span.ID != runtimeID {
		t.Errorf("the failed compile is not shown in place of the internal calls above it, under %q", runtimeID)
	}
	for _, id := range []SpanID{sdkCallID, binaryID, siblingID} {
		if tree := home(view, id); tree != nil {
			t.Errorf("internal span %q is shown", tree.Span.Name)
		}
	}
	if home(view, depsID) == nil {
		t.Error("a failed internal span outside the encapsulation is not shown")
	}
	if home(view, getModDefID) == nil {
		t.Error("a failed internal span with no failure beneath it is not shown")
	}

	internal := db.RowsView(FrontendOpts{ZoomedSpan: primaryID, Verbosity: ShowInternalVerbosity})
	for _, id := range []SpanID{sdkCallID, binaryID, siblingID, compileID} {
		if home(internal, id) == nil {
			t.Errorf("span %s is not shown at -vvv", id)
		}
	}
}
