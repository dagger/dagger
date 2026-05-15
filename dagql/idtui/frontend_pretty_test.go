package idtui

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/vito/tuist"
	"go.opentelemetry.io/otel/trace"
)

func TestSortErrorOriginsUsesCurrentSpanData(t *testing.T) {
	spanID := func(id byte) dagui.SpanID {
		return dagui.SpanID{SpanID: trace.SpanID{id}}
	}
	start := time.Unix(100, 0)
	firstParent := &dagui.Span{SpanSnapshot: dagui.SpanSnapshot{ID: spanID(1), Name: "sub-thing 1"}}
	first := &dagui.Span{SpanSnapshot: dagui.SpanSnapshot{ID: spanID(2), Name: "withExec first", StartTime: start}, ParentSpan: firstParent}
	secondParent := &dagui.Span{SpanSnapshot: dagui.SpanSnapshot{ID: spanID(3), Name: "sub-thing 2"}}
	second := &dagui.Span{SpanSnapshot: dagui.SpanSnapshot{ID: spanID(4), Name: "withExec second", StartTime: start.Add(time.Second)}, ParentSpan: secondParent}

	origins := []*dagui.Span{second, first}
	sortErrorOrigins(origins)
	if origins[0] != first || origins[1] != second {
		t.Fatalf("origins sorted by current start time = %q, %q; want first, second", origins[0].Name, origins[1].Name)
	}

	first.StartTime = time.Time{}
	second.StartTime = time.Time{}
	origins = []*dagui.Span{second, first}
	sortErrorOrigins(origins)
	if origins[0] != first || origins[1] != second {
		t.Fatalf("origins sorted by path tie-breaker = %q, %q; want first, second", origins[0].Name, origins[1].Name)
	}
}

func TestRenderShowsLiveGlobalTestsForPlainCall(t *testing.T) {
	db := dagui.NewDB()
	rootID := prettyTestSpanID(1)
	testID := prettyTestSpanID(2)
	start := time.Unix(100, 0)
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{
			ID:        rootID,
			TraceID:   prettyTestTraceID(),
			Name:      "plain dagger call",
			StartTime: start,
			EndTime:   start.Add(2 * time.Second),
			Final:     true,
		},
		{
			ID:           testID,
			TraceID:      prettyTestTraceID(),
			Name:         "unit failure",
			StartTime:    start.Add(time.Second),
			EndTime:      start.Add(2 * time.Second),
			ParentID:     rootID,
			TestCaseName: "unit failure",
			TestStatus:   dagui.TestStatusFailure,
			Final:        true,
		},
	})
	db.SetPrimarySpan(rootID)

	fe := NewWithDB(io.Discard, db)
	fe.FrontendOpts.Verbosity = dagui.ShowCompletedVerbosity
	fe.FrontendOpts.GCThreshold = time.Hour
	fe.recalculateViewLocked()

	got := strings.Join(fe.tui.RenderLines(), "\n")
	if !strings.Contains(got, "TESTS") || !strings.Contains(got, "unit failure") || !strings.Contains(got, "FAIL") {
		t.Fatalf("live render did not include global test report:\n%s", got)
	}
}

func TestLiveGlobalTestsSkipCheckScopedTests(t *testing.T) {
	db := dagui.NewDB()
	checkID := prettyTestSpanID(1)
	testID := prettyTestSpanID(2)
	start := time.Unix(100, 0)
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{
			ID:        checkID,
			TraceID:   prettyTestTraceID(),
			Name:      "check unit",
			StartTime: start,
			EndTime:   start.Add(2 * time.Second),
			CheckName: "unit",
			Final:     true,
		},
		{
			ID:           testID,
			TraceID:      prettyTestTraceID(),
			Name:         "unit failure",
			StartTime:    start.Add(time.Second),
			EndTime:      start.Add(2 * time.Second),
			ParentID:     checkID,
			TestCaseName: "unit failure",
			TestStatus:   dagui.TestStatusFailure,
			Final:        true,
		},
	})
	db.SetPrimarySpan(checkID)

	fe := NewWithDB(io.Discard, db)
	if lines := fe.renderLiveGlobalTests(tuist.Context{Width: 80}); len(lines) != 0 {
		t.Fatalf("expected no global live report for check-scoped tests, got:\n%s", strings.Join(lines, "\n"))
	}
}

func TestLiveGlobalTestsSkipCheckScopedNoTestSuites(t *testing.T) {
	db := dagui.NewDB()
	checkID := prettyTestSpanID(1)
	suiteID := prettyTestSpanID(2)
	start := time.Unix(100, 0)
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{
			ID:        checkID,
			TraceID:   prettyTestTraceID(),
			Name:      "check unit",
			StartTime: start,
			EndTime:   start.Add(2 * time.Second),
			CheckName: "unit",
			Final:     true,
		},
		{
			ID:            suiteID,
			TraceID:       prettyTestTraceID(),
			Name:          "empty suite",
			StartTime:     start.Add(time.Second),
			EndTime:       start.Add(2 * time.Second),
			ParentID:      checkID,
			TestSuiteName: "empty suite",
			TestStatus:    dagui.TestStatusSkipped,
			Final:         true,
		},
	})
	db.SetPrimarySpan(checkID)

	fe := NewWithDB(io.Discard, db)
	if lines := fe.renderLiveGlobalTests(tuist.Context{Width: 80}); len(lines) != 0 {
		t.Fatalf("expected no global live report for check-scoped no-test suite, got:\n%s", strings.Join(lines, "\n"))
	}
}

func prettyTestSpanID(id byte) dagui.SpanID {
	return dagui.SpanID{SpanID: trace.SpanID{id}}
}

func prettyTestTraceID() dagui.TraceID {
	return dagui.TraceID{TraceID: trace.TraceID{1}}
}

func TestDoQuitInvalidatesCachedRender(t *testing.T) {
	db := dagui.NewDB()
	spanID := dagui.SpanID{SpanID: trace.SpanID{1}}
	db.ImportSnapshots([]dagui.SpanSnapshot{{
		ID:        spanID,
		Name:      "cached-live-row",
		StartTime: time.Now(),
		EndTime:   time.Now(),
		Final:     true,
	}})
	db.SetPrimarySpan(spanID)

	fe := NewWithDB(io.Discard, db)
	fe.FrontendOpts.Verbosity = dagui.ShowCompletedVerbosity
	fe.FrontendOpts.GCThreshold = time.Hour
	fe.recalculateViewLocked()

	before := fe.tui.RenderLines()
	if got := strings.Join(before, "\n"); !strings.Contains(got, "cached-live-row") {
		t.Fatalf("initial render = %q, want live row", got)
	}

	fe.quit = make(chan struct{})
	fe.quitting = true
	fe.doQuit()

	if after := fe.tui.RenderLines(); len(after) != 0 {
		t.Fatalf("render after quit = %q, want no live rows", strings.Join(after, "\n"))
	}
}
