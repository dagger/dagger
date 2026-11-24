package dagui

import (
	"runtime/debug"
	"testing"
	"time"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// TestInternalFailureGivesPlaceToItsCause covers a module whose dependency's
// runtime fails to compile. The SDK builds the runtime, within its
// encapsulating span, through internal calls the view hides, but a failed
// span is shown even when it's internal, so every internal call between the
// module's load and the failed compile was shown, and with it everything
// beneath it. The internal calls that only pass the compile's failure on give
// their place to it, and the module entrypoint's plumbing, which fails
// because the function did, drops out beside it. So does the engine's own
// load of the dependency, within the module load's encapsulation: the
// dependency's build, which names it, takes its place beneath the load. A
// failed internal span with no failure beneath it still shows, and all of it
// at -vvv.
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
	fnProcessID := testID(13)
	plumbingID := testID(14)
	dispatchID := testID(15)
	fnQueryID := testID(16)

	start := time.Now().Add(-time.Minute)
	end := start.Add(time.Second)
	ok := sdktrace.Status{Code: codes.Ok}
	failed := sdktrace.Status{Code: codes.Error, Description: "./main.go:6:6: undefined: ctx"}

	db := NewDB()
	db.ImportSnapshots([]SpanSnapshot{
		// The CLI encapsulates the workspace's load...
		{ID: primaryID, TraceID: traceID, Name: "load workspace: .", StartTime: start, EndTime: end, Status: failed,
			Encapsulate: true},
		// ...and the engine each module's load, with its own internal step
		// within it, outside the SDK's encapsulation.
		{ID: extraID, TraceID: traceID, ParentID: primaryID, Name: "load module: ./broken-dep",
			StartTime: start, EndTime: end, Status: failed, Encapsulate: true},
		{ID: depsID, TraceID: traceID, ParentID: extraID, Name: "load dep modules",
			StartTime: start, EndTime: end, Status: failed, Internal: true},
		{ID: runtimeID, TraceID: traceID, ParentID: depsID, Name: "build broken",
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
		// The function's body, whose compile fails, beneath the requests of
		// its process...
		{ID: fnQueryID, TraceID: traceID, ParentID: binaryID, Name: "POST /query",
			StartTime: start, EndTime: end, Status: ok, Passthrough: true},
		{ID: compileID, TraceID: traceID, ParentID: fnQueryID, Name: "withExec sh /gobuild/build.sh dagger/broken",
			StartTime: start, EndTime: end, Status: failed, Internal: true},
		// ...and beside it, beneath the module process's span, the module
		// entrypoint's plumbing, which reports the function's error as its
		// process's failed exec.
		{ID: fnProcessID, TraceID: traceID, ParentID: binaryID, Name: "gobuild:Gobuild.binary",
			StartTime: start, EndTime: end, Status: failed, Passthrough: true},
		{ID: plumbingID, TraceID: traceID, ParentID: fnProcessID, Name: "call module entrypoint",
			StartTime: start, EndTime: end, Status: failed, Internal: true, Encapsulate: true, FunctionPlumbing: true},
		{ID: dispatchID, TraceID: traceID, ParentID: plumbingID, Name: "withExec /usr/local/bin/dispatch engine-call",
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
	for _, id := range []SpanID{sdkCallID, binaryID, siblingID, plumbingID, dispatchID} {
		if tree := home(view, id); tree != nil {
			t.Errorf("internal span %q is shown", tree.Span.Name)
		}
	}
	if tree := home(view, depsID); tree != nil {
		t.Error("the engine's internal load of the dependency is shown")
	}
	if build := home(view, runtimeID); build == nil || build.Parent == nil || build.Parent.Span.ID != extraID {
		t.Error("the dependency's build is not shown in its load's place, beneath the module's load")
	}
	if home(view, getModDefID) == nil {
		t.Error("a failed internal span with no failure beneath it is not shown")
	}

	internal := db.RowsView(FrontendOpts{ZoomedSpan: primaryID, Verbosity: ShowInternalVerbosity})
	for _, id := range []SpanID{depsID, sdkCallID, binaryID, siblingID, compileID, plumbingID, dispatchID} {
		if home(internal, id) == nil {
			t.Errorf("span %s is not shown at -vvv", id)
		}
	}
}

// TestInternalFailureBeneathAStepShows covers a failed internal span beneath
// a step the view shows that doesn't encapsulate its work, within a failed
// encapsulating span: the step's own internal span tells where its failure
// happened, so it keeps its row, even with a failure beneath it.
func TestInternalFailureBeneathAStepShows(t *testing.T) {
	traceID := TraceID{TraceID: trace.TraceID{1}}
	start := time.Now().Add(-time.Minute)
	end := start.Add(time.Second)
	failed := sdktrace.Status{Code: codes.Error, Description: "boom"}

	db := NewDB()
	db.ImportSnapshots([]SpanSnapshot{
		{ID: testID(1), TraceID: traceID, Name: "load workspace: .", StartTime: start, EndTime: end, Status: failed,
			Encapsulate: true},
		{ID: testID(2), TraceID: traceID, ParentID: testID(1), Name: "a step",
			StartTime: start, EndTime: end, Status: failed},
		{ID: testID(3), TraceID: traceID, ParentID: testID(2), Name: "its internal step",
			StartTime: start, EndTime: end, Status: failed, Internal: true},
		{ID: testID(4), TraceID: traceID, ParentID: testID(3), Name: "withExec",
			StartTime: start, EndTime: end, Status: failed},
	})
	db.SetPrimarySpan(testID(1))

	view := db.RowsView(FrontendOpts{ZoomedSpan: testID(1), Verbosity: ShowCompletedVerbosity})
	if view.HomeTree(db.Spans.Map[testID(3)]) == nil {
		t.Error("a failed internal span beneath a shown step is not shown")
	}
}

// TestFailedPlumbingAloneShows covers a module entrypoint whose plumbing fails
// on its own, before the function runs: with no failure of the function
// beside it, the plumbing's failure is the one to tell, and it shows.
func TestFailedPlumbingAloneShows(t *testing.T) {
	traceID := TraceID{TraceID: trace.TraceID{1}}
	primaryID := testID(1)
	runtimeID := testID(2)
	callID := testID(3)
	processID := testID(4)
	plumbingID := testID(5)
	dispatchID := testID(6)
	queryID := testID(7)
	okExecID := testID(8)

	start := time.Now().Add(-time.Minute)
	end := start.Add(time.Second)
	ok := sdktrace.Status{Code: codes.Ok}
	failed := sdktrace.Status{Code: codes.Error, Description: "dispatcher crashed"}

	db := NewDB()
	db.ImportSnapshots([]SpanSnapshot{
		{ID: primaryID, TraceID: traceID, Name: "load module", StartTime: start, EndTime: end, Status: failed},
		{ID: runtimeID, TraceID: traceID, ParentID: primaryID, Name: "build gobuild",
			StartTime: start, EndTime: end, Status: failed, Encapsulate: true},
		{ID: callID, TraceID: traceID, ParentID: runtimeID, Name: "Gobuild.binary",
			StartTime: start, EndTime: end, Status: failed, Internal: true},
		// The function's body ran nothing that failed.
		{ID: queryID, TraceID: traceID, ParentID: callID, Name: "POST /query",
			StartTime: start, EndTime: end, Status: ok, Passthrough: true},
		{ID: processID, TraceID: traceID, ParentID: callID, Name: "gobuild:Gobuild.binary",
			StartTime: start, EndTime: end, Status: failed, Passthrough: true},
		{ID: okExecID, TraceID: traceID, ParentID: queryID, Name: "Directory.file",
			StartTime: start, EndTime: end, Status: ok, Internal: true},
		{ID: plumbingID, TraceID: traceID, ParentID: processID, Name: "call module entrypoint",
			StartTime: start, EndTime: end, Status: failed, Internal: true, Encapsulate: true, FunctionPlumbing: true},
		{ID: dispatchID, TraceID: traceID, ParentID: plumbingID, Name: "withExec /usr/local/bin/dispatch engine-call",
			StartTime: start, EndTime: end, Status: failed, Internal: true},
	})
	db.SetPrimarySpan(primaryID)

	view := db.RowsView(FrontendOpts{ZoomedSpan: primaryID, Verbosity: ShowCompletedVerbosity})
	if view.HomeTree(db.Spans.Map[dispatchID]) == nil {
		t.Error("the plumbing's own failure is not shown")
	}
}

// TestRetriedPlumbingDoesNotWaitOnEachOther covers two retries of a module
// call within a failed SDK step, after the call's own span was emitted
// elsewhere in the session: the engine emits a call's span once, but runs the
// call again. Both retries' plumbing then sits beneath the same caller, each
// beside the other. Neither may decide whether it shows by asking whether the
// other does, which recursed until the stack ran out. With no failure of the
// function's own beside them, both show.
func TestRetriedPlumbingDoesNotWaitOnEachOther(t *testing.T) {
	// Keep a recursion from taking the whole test binary's stack.
	defer debug.SetMaxStack(debug.SetMaxStack(256 << 10))

	traceID := TraceID{TraceID: trace.TraceID{1}}
	start := time.Now().Add(-time.Minute)
	end := start.Add(time.Second)
	failed := sdktrace.Status{Code: codes.Error, Description: "dispatcher failed"}

	db := NewDB()
	db.ImportSnapshots([]SpanSnapshot{
		{ID: testID(1), TraceID: traceID, Name: "build helper",
			StartTime: start, EndTime: end, Status: failed, Encapsulate: true},
		{ID: testID(2), TraceID: traceID, ParentID: testID(1), Name: "GoSdk.moduleRuntime",
			StartTime: start, EndTime: end, Status: failed, Internal: true},
		{ID: testID(3), TraceID: traceID, ParentID: testID(2), Name: "POST /query",
			StartTime: start, EndTime: end, Passthrough: true},
		{ID: testID(4), TraceID: traceID, ParentID: testID(3), Name: "helper:Helper.build",
			StartTime: start, EndTime: end, Status: failed, Passthrough: true},
		{ID: testID(5), TraceID: traceID, ParentID: testID(4), Name: "call module entrypoint",
			StartTime: start, EndTime: end, Status: failed, Internal: true, Encapsulate: true, FunctionPlumbing: true},
		{ID: testID(6), TraceID: traceID, ParentID: testID(5), Name: "withExec /usr/local/bin/dispatch engine-call",
			StartTime: start, EndTime: end, Status: failed, Internal: true},
		{ID: testID(7), TraceID: traceID, ParentID: testID(2), Name: "POST /query",
			StartTime: start, EndTime: end, Passthrough: true},
		{ID: testID(8), TraceID: traceID, ParentID: testID(7), Name: "helper:Helper.build",
			StartTime: start, EndTime: end, Status: failed, Passthrough: true},
		{ID: testID(9), TraceID: traceID, ParentID: testID(8), Name: "call module entrypoint",
			StartTime: start, EndTime: end, Status: failed, Internal: true, Encapsulate: true, FunctionPlumbing: true},
		{ID: testID(10), TraceID: traceID, ParentID: testID(9), Name: "withExec /usr/local/bin/dispatch engine-call",
			StartTime: start, EndTime: end, Status: failed, Internal: true},
	})
	db.SetPrimarySpan(testID(1))

	view := db.RowsView(FrontendOpts{ZoomedSpan: testID(1), Verbosity: ShowCompletedVerbosity})
	for _, id := range []SpanID{testID(6), testID(10)} {
		if view.HomeTree(db.Spans.Map[id]) == nil {
			t.Errorf("retry's failed dispatch %s is not shown", id)
		}
	}
}
