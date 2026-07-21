package idtui

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/key"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/muesli/termenv"
	"github.com/vito/tuist"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
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
	t.Setenv("NO_COLOR", "1")
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

	lines := fe.tui.RenderLines()
	got := strings.Join(lines, "\n")
	if !strings.Contains(got, "unit failure") || !strings.Contains(got, "FAIL") {
		t.Fatalf("live render did not include global test report:\n%s", got)
	}
	testsLine, ok := findPrettyTestLine(lines, "TESTS")
	if !ok {
		t.Fatalf("live render did not include TESTS line:\n%s", got)
	}
	if testsLine != "TESTS T inspect" {
		t.Fatalf("global live TESTS line = %q, want hint with no indentation", testsLine)
	}
}

func TestInlineLogsViewFetchesOnMountAndRenders(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	db := dagui.NewDB()
	rootID := prettyTestSpanID(1)
	childID := prettyTestSpanID(2)
	start := time.Unix(100, 0)
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{
			ID:        rootID,
			TraceID:   prettyTestTraceID(),
			Name:      "root call",
			StartTime: start,
			EndTime:   start.Add(2 * time.Second),
			Final:     true,
		},
		{
			ID:        childID,
			TraceID:   prettyTestTraceID(),
			Name:      "child op",
			StartTime: start.Add(time.Second),
			EndTime:   start.Add(2 * time.Second),
			ParentID:  rootID,
			Final:     true,
		},
	})
	db.SetPrimarySpan(rootID)

	fe := NewWithDB(io.Discard, db)
	fe.FrontendOpts.Verbosity = dagui.ShowCompletedVerbosity
	fe.FrontendOpts.GCThreshold = time.Hour
	// Force the child expanded directly, NOT via setExpanded -- whose own
	// requestLogs would mask the LogsView's mount-driven fetch we're testing.
	fe.FrontendOpts.SpanExpanded = map[dagui.SpanID]bool{rootID: true, childID: true}

	var mu sync.Mutex
	fetched := map[dagui.SpanID]bool{}
	fe.logProvider = func(id dagui.SpanID, _ bool) {
		mu.Lock()
		fetched[id] = true
		mu.Unlock()
	}

	fe.recalculateViewLocked()
	_ = fe.tui.RenderLines()

	mu.Lock()
	gotChild := fetched[childID]
	mu.Unlock()
	if !gotChild {
		t.Fatalf("expanded child's logs were not requested on LogsView mount; fetched=%v", fetched)
	}
	if fe.logsViews[childID] == nil {
		t.Fatal("no LogsView created for expanded child")
	}

	// Deliver the logs and re-render: they should surface via the LogsView.
	logs := NewVterm(termenv.Ascii)
	logs.SetWidth(80)
	_, _ = logs.Write([]byte("hello from child\n"))
	fe.logs.Logs[childID] = logs
	fe.updateSpanTreesForLogs(childID)

	got := strings.Join(fe.tui.RenderLines(), "\n")
	if !strings.Contains(got, "hello from child") {
		t.Fatalf("LogsView did not render delivered logs:\n%s", got)
	}

	// Memoization: writing to the Vterm and repainting the owning tree -- WITHOUT
	// notifying the LogsView -- must not change its output. The expensive
	// Vterm.View() is served from cache until an explicit Update. This is the
	// whole point of the component: unrelated parent repaints (spinner ticks,
	// focus moves) don't re-render logs.
	_, _ = logs.Write([]byte("NOT_YET_VISIBLE\n"))
	fe.spanTrees[childID].Update()
	if got2 := strings.Join(fe.tui.RenderLines(), "\n"); strings.Contains(got2, "NOT_YET_VISIBLE") {
		t.Fatal("LogsView re-rendered on an unrelated repaint -- not memoized")
	}

	// The push-Update on log arrival invalidates the cache.
	fe.updateSpanTreesForLogs(childID)
	if got3 := strings.Join(fe.tui.RenderLines(), "\n"); !strings.Contains(got3, "NOT_YET_VISIBLE") {
		t.Fatalf("LogsView did not refresh after updateSpanTreesForLogs:\n%s", got3)
	}
}

// TestInlineLogsReactToScreenHeight drives a real resize through the headless
// terminal and asserts a row's inline log window (a third of the screen) tracks
// the new height instead of sticking at the height it first saw. It reads the
// window height the owner synced onto the LogsView, sidestepping the outer
// viewport crop. Regression guard for sizing the window off the imperatively
// cached fe.window.Height (which leaves the memoized row cache-keyed on width
// alone) rather than ctx.ScreenHeight().
func TestInlineLogsReactToScreenHeight(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	db := dagui.NewDB()
	rootID := prettyTestSpanID(1)
	childID := prettyTestSpanID(2)
	start := time.Unix(100, 0)
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{
			ID:        rootID,
			TraceID:   prettyTestTraceID(),
			Name:      "root call",
			StartTime: start,
			EndTime:   start.Add(2 * time.Second),
			Final:     true,
		},
		{
			ID:        childID,
			TraceID:   prettyTestTraceID(),
			Name:      "child op",
			StartTime: start.Add(time.Second),
			EndTime:   start.Add(2 * time.Second),
			ParentID:  rootID,
			Final:     true,
		},
	})
	db.SetPrimarySpan(rootID)

	term := tuist.NewHeadlessTerminal(120, 60)
	fe := newWithTerminal(io.Discard, db, term)
	fe.FrontendOpts.Verbosity = dagui.ShowCompletedVerbosity
	fe.FrontendOpts.GCThreshold = time.Hour
	fe.FrontendOpts.SpanExpanded = map[dagui.SpanID]bool{rootID: true, childID: true}

	// Deliver a tall log so the third-of-screen window actually clips it.
	logs := NewVterm(termenv.Ascii)
	logs.SetWidth(80)
	for i := 0; i < 60; i++ {
		_, _ = logs.Write([]byte(fmt.Sprintf("log line %02d\n", i)))
	}
	fe.logs.Logs[childID] = logs

	fe.recalculateViewLocked()

	// Tall screen: the window is a third of it.
	_ = fe.tui.Frame()
	lv := fe.logsViews[childID]
	if lv == nil {
		t.Fatal("no LogsView created for expanded child")
	}
	tall := lv.height

	// Shrink the screen and repaint: the window must follow it down.
	term.Resize(120, 30)
	_ = fe.tui.Frame()
	short := lv.height

	if tall != 20 || short != 10 {
		t.Fatalf("inline log window did not track screen height: tall=%d (want 20), short=%d (want 10)", tall, short)
	}
}

// TestInTreeMessageLogsReactToScreenHeight is the in-tree counterpart of
// TestInlineLogsReactToScreenHeight: a message span renders its logs straight in
// the progress tree (renderStepLogs, not the LogsView), windowed to a third of
// the screen. Driving a real resize, the "...N lines hidden..." trim marker --
// anchored at the top of the block, so not subject to the outer crop -- must
// grow as the screen (and thus the window) shrinks.
func TestInTreeMessageLogsReactToScreenHeight(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	db := dagui.NewDB()
	rootID := prettyTestSpanID(1)
	msgID := prettyTestSpanID(2)
	start := time.Unix(100, 0)
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{
			ID:        rootID,
			TraceID:   prettyTestTraceID(),
			Name:      "root call",
			StartTime: start,
			EndTime:   start.Add(2 * time.Second),
			Final:     true,
		},
		{
			ID:        msgID,
			TraceID:   prettyTestTraceID(),
			Name:      "output",
			Message:   "output",
			StartTime: start.Add(time.Second),
			EndTime:   start.Add(2 * time.Second),
			ParentID:  rootID,
			Final:     true,
		},
	})
	db.SetPrimarySpan(rootID)

	term := tuist.NewHeadlessTerminal(120, 60)
	fe := newWithTerminal(io.Discard, db, term)
	fe.FrontendOpts.Verbosity = dagui.ShowCompletedVerbosity
	fe.FrontendOpts.GCThreshold = time.Hour
	fe.FrontendOpts.SpanExpanded = map[dagui.SpanID]bool{rootID: true, msgID: true}

	logs := NewVterm(termenv.Ascii)
	logs.SetWidth(80)
	for i := 0; i < 60; i++ {
		_, _ = logs.Write([]byte(fmt.Sprintf("log line %02d\n", i)))
	}
	fe.logs.Logs[msgID] = logs

	fe.recalculateViewLocked()

	hidden := func() int {
		re := regexp.MustCompile(`\.\.\.(\d+) lines hidden\.\.\.`)
		for _, line := range fe.tui.Frame() {
			if m := re.FindStringSubmatch(line); m != nil {
				n, _ := strconv.Atoi(m[1])
				return n
			}
		}
		return -1
	}

	tall := hidden()
	term.Resize(120, 30)
	short := hidden()

	if tall < 0 || short < 0 {
		t.Fatalf("no log trim marker found (tall=%d short=%d); message-span logs not windowed", tall, short)
	}
	// A taller screen hides fewer lines: window grows with the screen.
	if short <= tall {
		t.Fatalf("in-tree log window did not track screen height: hidden tall=%d, short=%d (want short > tall)", tall, short)
	}
}

func TestInteractiveDoesNotEagerlyFetchFailureLogs(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	rootID := prettyTestSpanID(1)
	testID := prettyTestSpanID(2)
	start := time.Unix(100, 0)
	mkDB := func() *dagui.DB {
		db := dagui.NewDB()
		db.ImportSnapshots([]dagui.SpanSnapshot{
			{
				ID:        rootID,
				TraceID:   prettyTestTraceID(),
				Name:      "run tests",
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
		return db
	}

	recorder := func(fe *frontendPretty) *map[dagui.SpanID]bool {
		fetched := map[dagui.SpanID]bool{}
		fe.logProvider = func(id dagui.SpanID, _ bool) { fetched[id] = true }
		return &fetched
	}

	// Interactive: recalculateViewLocked must NOT eagerly fetch the failing test
	// case's logs. They are fetched lazily when the TestView actually renders
	// them (re-render on arrival), so a collapsed/off-screen failure costs no
	// fetch -- the over-fetch this change eliminates.
	feI := NewWithDB(io.Discard, mkDB())
	feI.FrontendOpts.Verbosity = dagui.ShowCompletedVerbosity
	fetchedI := recorder(feI)
	feI.recalculateViewLocked()
	if (*fetchedI)[testID] {
		t.Fatalf("interactive recalc eagerly fetched failing test case logs; fetched=%v", *fetchedI)
	}

	// Report: the single final render can't wait for a lazy fetch, so it still
	// pre-fetches the failing test case eagerly.
	feR := NewWithDB(io.Discard, mkDB())
	feR.reportOnly = true
	feR.FrontendOpts.Verbosity = dagui.ShowCompletedVerbosity
	fetchedR := recorder(feR)
	feR.recalculateViewLocked()
	if !(*fetchedR)[testID] {
		t.Fatalf("report recalc did not eagerly fetch failing test case logs; fetched=%v", *fetchedR)
	}
}

func TestFinalGlobalTestsUnindented(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
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
	// Render live first so the final report proves it does not inherit the live
	// "T inspect" hint from the cached inline TestView.
	if lines := fe.renderGlobalTests(tuist.Context{Width: 80}, false); len(lines) == 0 {
		t.Fatal("live global tests did not render")
	}
	// Claims are per-render-pass state; the real frontend resets them before each
	// pass. Reset here so the final render isn't suppressed by the live render
	// having claimed these same orphan cases.
	fe.claims = newRenderClaims()
	fe.finalRender = true
	lines := fe.renderGlobalTests(tuist.Context{Width: 80}, true)
	testsLine, ok := findPrettyTestLine(lines, "TESTS")
	if !ok {
		t.Fatalf("final global tests did not include TESTS line:\n%s", strings.Join(lines, "\n"))
	}
	if testsLine != "TESTS" {
		t.Fatalf("global final TESTS line = %q, want no indentation", testsLine)
	}
}

// TestOrphanTestsSectionTitledAndWarned verifies that when a check claims its
// tests but some test cases dangle (an ancestor span is missing from the trace
// data), the leftover global section is titled "ORPHANED TESTS" and the warning
// renders indented beneath that heading -- not floating, headerless, above it.
func TestOrphanTestsSectionTitledAndWarned(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	db := dagui.NewDB()
	start := time.Unix(100, 0)
	end := start.Add(2 * time.Second)

	checkID := prettyTestSpanID(1)
	claimedTestID := prettyTestSpanID(2)
	orphanTestID := prettyTestSpanID(3)
	missingID := prettyTestSpanID(99) // referenced as a parent but never imported

	db.ImportSnapshots([]dagui.SpanSnapshot{
		{
			ID: checkID, TraceID: prettyTestTraceID(), Name: "ci:bootstrap",
			StartTime: start, EndTime: end, CheckName: "ci:bootstrap", Final: true,
		},
		{
			ID: claimedTestID, TraceID: prettyTestTraceID(), Name: "claimed test",
			StartTime: start, EndTime: end, ParentID: checkID,
			TestCaseName: "claimed test", TestStatus: dagui.TestStatusSuccess, Final: true,
		},
		{
			// Parented to a span that's never imported, so its ancestor chain has a
			// genuine gap (FirstMissingAncestor != nil) -- a true orphan.
			ID: orphanTestID, TraceID: prettyTestTraceID(), Name: "orphan test",
			StartTime: start, EndTime: end, ParentID: missingID,
			TestCaseName: "orphan test", TestStatus: dagui.TestStatusSuccess, Final: true,
		},
	})
	db.SetPrimarySpan(checkID)

	fe := NewWithDB(io.Discard, db)
	fe.finalRender = true
	fe.recalculateViewLocked()

	// The checks report claims its own test first, mirroring Render's order, so the
	// global section is left with just the orphan.
	r := newRenderer(fe.db, 0, fe.FrontendOpts, true)
	fe.checksReport(tuist.Context{Width: 100}, r, false)

	lines := fe.renderGlobalTests(tuist.Context{Width: 100}, true)
	if len(lines) < 3 {
		t.Fatalf("orphan global tests rendered too few lines:\n%s", strings.Join(lines, "\n"))
	}
	if lines[0] != "ORPHANED TESTS" {
		t.Fatalf("heading = %q, want %q\n%s", lines[0], "ORPHANED TESTS", strings.Join(lines, "\n"))
	}
	if !strings.HasPrefix(lines[1], "  ! ") ||
		!strings.Contains(lines[1], "could not be attributed to a check") {
		t.Fatalf("warning line = %q, want indented under the heading\n%s", lines[1], strings.Join(lines, "\n"))
	}
	if lines[2] != "    (run with --debug to list them)" {
		t.Fatalf("hint line = %q, want indented under the warning\n%s", lines[2], strings.Join(lines, "\n"))
	}
}

func TestTraceHeaderShowsVerdict(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	start := time.Unix(100, 0)
	for _, tc := range []struct {
		name     string
		status   sdktrace.Status
		wantWord string
		wantErr  string
	}{
		{
			name:     "passing call",
			status:   sdktrace.Status{Code: codes.Ok},
			wantWord: "PASSED",
		},
		{
			name:     "failing call",
			status:   sdktrace.Status{Code: codes.Error, Description: `call function "phpstan": exit code: 1 [traceparent:0123456789abcdef0123456789abcdef-0123456789abcdef]`},
			wantWord: "FAILED",
			wantErr:  `call function "phpstan": exit code: 1`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := dagui.NewDB()
			rootID := prettyTestSpanID(1)
			db.ImportSnapshots([]dagui.SpanSnapshot{{
				ID:        rootID,
				TraceID:   prettyTestTraceID(),
				Name:      "test phpstan",
				StartTime: start,
				EndTime:   start.Add(2 * time.Second),
				Status:    tc.status,
				Final:     true,
			}})
			db.SetPrimarySpan(rootID)

			fe := NewWithDB(io.Discard, db)
			fe.finalRender = true
			r := newRenderer(fe.db, 40, fe.FrontendOpts, true)
			joined := strings.Join(fe.renderTraceHeader(r), "\n")

			if !strings.Contains(joined, "TRACE") || !strings.Contains(joined, tc.wantWord) {
				t.Fatalf("header missing TRACE/%s:\n%s", tc.wantWord, joined)
			}
			if !strings.Contains(joined, "test phpstan") {
				t.Fatalf("header missing command:\n%s", joined)
			}
			if tc.wantErr != "" && !strings.Contains(joined, tc.wantErr) {
				t.Fatalf("header missing error %q:\n%s", tc.wantErr, joined)
			}
			if strings.Contains(joined, "traceparent") {
				t.Fatalf("header should strip traceparent markers:\n%s", joined)
			}
		})
	}
}

func TestLiveInlineCheckTestsIndentedUnderTrace(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
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
	fe.FrontendOpts.Verbosity = dagui.ShowCompletedVerbosity
	fe.FrontendOpts.GCThreshold = time.Hour
	fe.recalculateViewLocked()

	lines := fe.tui.RenderLines()
	testsLine, ok := findPrettyTestLine(lines, "TESTS")
	if !ok {
		t.Fatalf("live check render did not include inline TESTS line:\n%s", strings.Join(lines, "\n"))
	}
	idx := strings.Index(testsLine, "TESTS")
	if idx < 2 || testsLine[idx-2:idx] != "  " || !strings.Contains(testsLine[:idx], VertBoldBar) {
		t.Fatalf("inline TESTS line = %q, want trace pipe plus two-space test indent", testsLine)
	}
	if !strings.Contains(testsLine[idx:], "TESTS T inspect") {
		t.Fatalf("inline TESTS line = %q, want test viewer hint", testsLine)
	}
}

// TestChecksReportNestsSubCheckHeader verifies the final report introduces a
// check's sub-checks with their own CHECKS header -- mirroring how a check nests
// a TESTS header for its tests -- indented one level under the parent, and that
// each header tallies the checks directly beneath it: the top header counts the
// roots only (1 here), not every descendant (which would read "2 passed").
func TestChecksReportNestsSubCheckHeader(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	db := dagui.NewDB()
	bootstrapID := prettyTestSpanID(1)
	goLintID := prettyTestSpanID(2)
	helmLintID := prettyTestSpanID(3)
	start := time.Unix(100, 0)
	end := start.Add(2 * time.Second)
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{
			ID:        bootstrapID,
			TraceID:   prettyTestTraceID(),
			Name:      "ci:bootstrap",
			StartTime: start,
			EndTime:   end,
			CheckName: "ci:bootstrap",
			Final:     true,
		},
		{
			ID:        goLintID,
			TraceID:   prettyTestTraceID(),
			Name:      "go:lint",
			StartTime: start,
			EndTime:   end,
			ParentID:  bootstrapID,
			CheckName: "go:lint",
			Final:     true,
		},
		{
			ID:        helmLintID,
			TraceID:   prettyTestTraceID(),
			Name:      "helm:lint",
			StartTime: start,
			EndTime:   end,
			ParentID:  bootstrapID,
			CheckName: "helm:lint",
			Final:     true,
		},
	})
	db.SetPrimarySpan(bootstrapID)

	fe := NewWithDB(io.Discard, db)
	fe.recalculateViewLocked()

	r := newRenderer(fe.db, 0, fe.FrontendOpts, true)
	lines := fe.checksReport(tuist.Context{Width: 120}, r, false)
	if len(lines) == 0 {
		t.Fatal("checksReport returned no lines")
	}
	joined := strings.Join(lines, "\n")

	// Top header: roots only.
	if !strings.HasPrefix(lines[0], "CHECKS") || !strings.Contains(lines[0], "1 passed") {
		t.Fatalf("top CHECKS header = %q, want roots-only tally (1 passed)\n%s", lines[0], joined)
	}

	bootstrapIdx, nestedIdx, goLintIdx, helmLintIdx := -1, -1, -1, -1
	for i, l := range lines {
		switch {
		case bootstrapIdx == -1 && strings.Contains(l, "ci:bootstrap"):
			bootstrapIdx = i
		case nestedIdx == -1 && strings.HasPrefix(l, "  CHECKS"):
			nestedIdx = i
		case goLintIdx == -1 && strings.Contains(l, "go:lint"):
			goLintIdx = i
		case helmLintIdx == -1 && strings.Contains(l, "helm:lint"):
			helmLintIdx = i
		}
	}
	if bootstrapIdx == -1 || nestedIdx == -1 || goLintIdx == -1 || helmLintIdx == -1 {
		t.Fatalf("missing expected rows (bootstrap=%d nested=%d go=%d helm=%d):\n%s",
			bootstrapIdx, nestedIdx, goLintIdx, helmLintIdx, joined)
	}
	// Parent, then its nested CHECKS header, then the children.
	if bootstrapIdx >= nestedIdx || nestedIdx >= goLintIdx || nestedIdx >= helmLintIdx {
		t.Fatalf("rows out of order (bootstrap=%d nested=%d go=%d helm=%d):\n%s",
			bootstrapIdx, nestedIdx, goLintIdx, helmLintIdx, joined)
	}
	// The nested header tallies the parent's direct children.
	if !strings.Contains(lines[nestedIdx], "2 passed") {
		t.Fatalf("nested CHECKS header = %q, want direct-children tally (2 passed)", lines[nestedIdx])
	}
	// The parent sits at the margin; its sub-checks indent one level under the
	// nested header (four spaces: depth-2 tree indent).
	if strings.HasPrefix(lines[bootstrapIdx], " ") {
		t.Fatalf("parent check line = %q, want no indent", lines[bootstrapIdx])
	}
	if !strings.HasPrefix(lines[goLintIdx], "    ") {
		t.Fatalf("sub-check line = %q, want four-space indent under the nested header", lines[goLintIdx])
	}
}

// rerunReportDB builds a DB with a failed outermost check (ci:bootstrap) whose
// sub-check (go:lint) also failed, for exercising the RE-RUN section.
func rerunReportDB(t *testing.T) *dagui.DB {
	t.Helper()
	db := dagui.NewDB()
	bootstrapID := prettyTestSpanID(1)
	goLintID := prettyTestSpanID(2)
	start := time.Unix(100, 0)
	end := start.Add(2 * time.Second)
	failed := sdktrace.Status{Code: codes.Error}
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{
			ID:        bootstrapID,
			TraceID:   prettyTestTraceID(),
			Name:      "ci:bootstrap",
			StartTime: start,
			EndTime:   end,
			CheckName: "ci:bootstrap",
			Status:    failed,
			Final:     true,
		},
		{
			ID:        goLintID,
			TraceID:   prettyTestTraceID(),
			Name:      "go:lint",
			StartTime: start,
			EndTime:   end,
			ParentID:  bootstrapID,
			CheckName: "go:lint",
			Status:    failed,
			Final:     true,
		},
	})
	db.SetPrimarySpan(bootstrapID)
	return db
}

func TestRerunSectionCloudAndLocalForNativeCI(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	fe := NewWithDB(io.Discard, rerunReportDB(t))
	fe.recalculateViewLocked()
	fe.ciMeta = &ciContext{commit: "abc123", isNativeCI: true}

	lines := fe.renderRerunSection(nil)
	joined := strings.Join(lines, "\n")
	// Only the outermost check is re-runnable; the sub-check rolls up to it.
	if strings.Contains(joined, "go:lint") {
		t.Fatalf("RE-RUN listed a sub-check:\n%s", joined)
	}
	// Two distinct sections so the CI re-run and local reproduce don't read as one.
	if !strings.Contains(joined, "RE-RUN IN CI") || !strings.Contains(joined, "RUN LOCALLY") {
		t.Fatalf("missing one of the two section headings:\n%s", joined)
	}
	if !strings.Contains(joined, `dagger cloud rerun --commit abc123 --check "ci:bootstrap"`) {
		t.Fatalf("missing cloud rerun line:\n%s", joined)
	}
	if !strings.Contains(joined, `dagger check "ci:bootstrap"`) {
		t.Fatalf("missing local check line:\n%s", joined)
	}
	// The CI re-run section leads; the local reproduce section follows.
	if ciIdx, localIdx := indexOfLine(lines, "RE-RUN IN CI"), indexOfLine(lines, "RUN LOCALLY"); ciIdx < 0 || ciIdx >= localIdx {
		t.Fatalf("expected RE-RUN IN CI before RUN LOCALLY (ci=%d local=%d):\n%s", ciIdx, localIdx, joined)
	}
}

func TestRerunSectionLocalOnlyWithoutNativeCI(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	fe := NewWithDB(io.Discard, rerunReportDB(t))
	fe.recalculateViewLocked()
	// No ciMeta (live/local run): only a local check, no cloud rerun.

	lines := fe.renderRerunSection(nil)
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, "cloud rerun") || strings.Contains(joined, "RE-RUN IN CI") {
		t.Fatalf("did not expect a CI re-run section without native CI:\n%s", joined)
	}
	if !strings.Contains(joined, "RUN LOCALLY") || !strings.Contains(joined, `dagger check "ci:bootstrap"`) {
		t.Fatalf("missing local reproduce section:\n%s", joined)
	}
}

func TestOutermostSurfacedCheckRollsUpNestedName(t *testing.T) {
	db := rerunReportDB(t)
	fe := NewWithDB(io.Discard, db)
	fe.recalculateViewLocked()
	roots := fe.db.SurfacedChecks()
	root := outermostSurfacedCheck(roots, "go:lint")
	if root == nil || root.Name != "ci:bootstrap" {
		t.Fatalf("outermostSurfacedCheck(go:lint) = %v, want ci:bootstrap", root)
	}
}

func indexOfLine(lines []string, substr string) int {
	for i, l := range lines {
		if strings.Contains(l, substr) {
			return i
		}
	}
	return -1
}

// TestInlineChecksRollupSurfacesSubChecks drives a full interactive render and
// checks that a collapsed parent check surfaces its sub-checks as an inline
// CHECKS rollup -- a CHECKS header and the sub-check rows beneath it, carrying
// the row's tree pipe -- the same way a check surfaces its tests.
func TestInlineChecksRollupSurfacesSubChecks(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	db := dagui.NewDB()
	bootstrapID := prettyTestSpanID(1)
	goLintID := prettyTestSpanID(2)
	helmLintID := prettyTestSpanID(3)
	start := time.Unix(100, 0)
	end := start.Add(2 * time.Second)
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{
			ID:        bootstrapID,
			TraceID:   prettyTestTraceID(),
			Name:      "ci:bootstrap",
			StartTime: start,
			EndTime:   end,
			CheckName: "ci:bootstrap",
			Final:     true,
		},
		{
			ID:        goLintID,
			TraceID:   prettyTestTraceID(),
			Name:      "go:lint",
			StartTime: start,
			EndTime:   end,
			ParentID:  bootstrapID,
			CheckName: "go:lint",
			Final:     true,
		},
		{
			ID:        helmLintID,
			TraceID:   prettyTestTraceID(),
			Name:      "helm:lint",
			StartTime: start,
			EndTime:   end,
			ParentID:  bootstrapID,
			CheckName: "helm:lint",
			Final:     true,
		},
	})
	db.SetPrimarySpan(bootstrapID)

	fe := NewWithDB(io.Discard, db)
	fe.FrontendOpts.Verbosity = dagui.ShowCompletedVerbosity
	fe.FrontendOpts.GCThreshold = time.Hour
	fe.recalculateViewLocked()

	lines := fe.tui.RenderLines()
	checksLine, ok := findPrettyTestLine(lines, "CHECKS")
	if !ok {
		t.Fatalf("interactive render did not surface an inline CHECKS rollup:\n%s", strings.Join(lines, "\n"))
	}
	// The rollup carries the row's tree pipe, like the inline TESTS rollup.
	if idx := strings.Index(checksLine, "CHECKS"); idx < 0 || !strings.Contains(checksLine[:idx], VertBoldBar) {
		t.Fatalf("inline CHECKS line = %q, want the trace pipe before the header", checksLine)
	}
	if _, ok := findPrettyTestLine(lines, "go:lint"); !ok {
		t.Fatalf("inline CHECKS rollup missing sub-check go:lint:\n%s", strings.Join(lines, "\n"))
	}
	if _, ok := findPrettyTestLine(lines, "helm:lint"); !ok {
		t.Fatalf("inline CHECKS rollup missing sub-check helm:lint:\n%s", strings.Join(lines, "\n"))
	}
}

// TestChecksRollupCondenses verifies the inline rollup condenses to fit a height
// budget: unbounded shows every sub-check, a tight budget never overflows, keeps
// the CHECKS header (whose tally is the at-a-glance outcome), and marks the
// dropped sub-checks; the floor is the header alone.
func TestChecksRollupCondenses(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	db := dagui.NewDB()
	bootstrapID := prettyTestSpanID(1)
	start := time.Unix(100, 0)
	end := start.Add(time.Second)
	snaps := []dagui.SpanSnapshot{{
		ID:        bootstrapID,
		TraceID:   prettyTestTraceID(),
		Name:      "ci:bootstrap",
		StartTime: start,
		EndTime:   end,
		CheckName: "ci:bootstrap",
		Final:     true,
	}}
	const n = 12
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("check-%02d", i)
		snaps = append(snaps, dagui.SpanSnapshot{
			ID:        prettyTestSpanID(byte(2 + i)),
			TraceID:   prettyTestTraceID(),
			Name:      name,
			StartTime: start,
			EndTime:   end,
			ParentID:  bootstrapID,
			CheckName: name,
			Final:     true,
		})
	}
	db.ImportSnapshots(snaps)
	db.SetPrimarySpan(bootstrapID)

	fe := NewWithDB(io.Discard, db)
	fe.recalculateViewLocked()
	node := fe.checkNodeForSpan(db.Spans.Map[bootstrapID])
	if node == nil || len(node.Children) != n {
		t.Fatalf("checkNodeForSpan = %v, want %d children", node, n)
	}
	r := newRenderer(fe.db, 0, fe.FrontendOpts, false)
	ctx := tuist.Context{Width: 120}

	// Unbounded: the header plus every sub-check.
	full := fe.checksRollupLines(ctx, r, node.Children, 0)
	if len(full) != n+1 {
		t.Fatalf("unbounded rollup = %d lines, want %d:\n%s", len(full), n+1, strings.Join(full, "\n"))
	}
	if !strings.Contains(full[0], "CHECKS") {
		t.Fatalf("rollup header = %q, want CHECKS", full[0])
	}

	// Tight budgets: never overflow, always keep the header, mark the remainder.
	for _, h := range []int{8, 5, 3, 2} {
		got := fe.checksRollupLines(ctx, r, node.Children, h)
		if len(got) > h {
			t.Fatalf("rollup at height %d = %d lines, want <= %d:\n%s", h, len(got), h, strings.Join(got, "\n"))
		}
		if !strings.Contains(got[0], "CHECKS") {
			t.Fatalf("rollup at height %d dropped the header: %q", h, got[0])
		}
		if !strings.Contains(strings.Join(got, "\n"), "more") {
			t.Fatalf("rollup at height %d hid sub-checks without a 'more' marker:\n%s", h, strings.Join(got, "\n"))
		}
	}

	// Floor: the header alone.
	floor := fe.checksRollupLines(ctx, r, node.Children, 1)
	if len(floor) != 1 || !strings.Contains(floor[0], "CHECKS") {
		t.Fatalf("floor rollup = %q, want the header alone", strings.Join(floor, "\n"))
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
	fe.recalculateViewLocked()
	// Drive the real seeding the interactive render runs before the global
	// section: the check's inline rollup claims this case, so the global section
	// must skip it. Manually claiming here would mask a render-order regression
	// (the global section is emitted before the rollups that claim) -- the very
	// bug that let check tests duplicate into the global section.
	fe.claimInlineTestCases()
	if lines := fe.renderGlobalTests(tuist.Context{Width: 80}, false); len(lines) != 0 {
		t.Fatalf("expected no global live report for check-scoped tests, got:\n%s", strings.Join(lines, "\n"))
	}
}

// TestInteractiveCheckScopedTestsNotDuplicated drives a full interactive render
// and guards the render order itself: the live global tests section is emitted
// before the trace rows (so its log claims suppress duplicate logs above it),
// so Render must seed the inline rollups' case claims first or every check's
// tests repeat in a second, unindented global section. A single check with one
// failing case must therefore produce exactly one TESTS section -- the inline
// rollup -- and no global copy.
func TestInteractiveCheckScopedTestsNotDuplicated(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
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
	fe.recalculateViewLocked()
	out := strings.Join(fe.tui.RenderLines(), "\n")
	if !strings.Contains(out, "TESTS") {
		t.Fatalf("expected the check's inline tests rollup, got:\n%s", out)
	}
	if n := strings.Count(out, "TESTS"); n != 1 {
		t.Fatalf("check-scoped tests duplicated into a global section: got %d TESTS sections, want 1:\n%s", n, out)
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
	if lines := fe.renderGlobalTests(tuist.Context{Width: 80}, false); len(lines) != 0 {
		t.Fatalf("expected no global live report for check-scoped no-test suite, got:\n%s", strings.Join(lines, "\n"))
	}
}

func findPrettyTestLine(lines []string, want string) (string, bool) {
	for _, line := range lines {
		if strings.Contains(line, want) {
			return line, true
		}
	}
	return "", false
}

func prettyTestSpanID(id byte) dagui.SpanID {
	return dagui.SpanID{SpanID: trace.SpanID{id}}
}

func prettyTestTraceID() dagui.TraceID {
	return dagui.TraceID{TraceID: trace.TraceID{1}}
}

func TestLogPagerQClosesLikeEscape(t *testing.T) {
	fe := NewWithDB(io.Discard, dagui.NewDB())
	restored := false
	fe.logPager = &LogPagerView{}
	fe.logPagerReturn = func() { restored = true }

	fe.handleNavKeyUV(uv.KeyPressEvent(uv.Key{Text: "q", Code: 'q'}))

	if fe.logPager != nil {
		t.Fatal("expected q to close log pager")
	}
	if !restored {
		t.Fatal("expected q to restore prior focus like escape")
	}
}

func TestTestsModeQClosesLikeEscape(t *testing.T) {
	fe := NewWithDB(io.Discard, dagui.NewDB())
	fe.testsMode = true
	fe.fullscreenTests = &TestView{}

	fe.handleNavKeyUV(uv.KeyPressEvent(uv.Key{Text: "q", Code: 'q'}))

	if fe.testsMode {
		t.Fatal("expected q to close tests mode")
	}
	if fe.fullscreenTests != nil {
		t.Fatal("expected q to clear fullscreen tests")
	}
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

// stubShellHandler is a no-op ShellHandler used to put the frontend into shell
// (flowing) mode for rendering tests, without standing up the real shell.
type stubShellHandler struct{}

func (stubShellHandler) Handle(context.Context, string) error { return nil }
func (stubShellHandler) AutoComplete(string, int) tuist.CompletionResult {
	return tuist.CompletionResult{}
}
func (stubShellHandler) IsComplete(string) bool { return true }
func (stubShellHandler) Prompt(context.Context, TermOutput, termenv.Color) (string, func()) {
	return "⋈ ", nil
}
func (stubShellHandler) KeyBindings(TermOutput) []key.Binding { return nil }
func (stubShellHandler) ReactToInput(context.Context, uv.KeyPressEvent, string, bool) func() {
	return nil
}
func (stubShellHandler) EncodeHistory(entry string) string { return entry }
func (stubShellHandler) DecodeHistory(entry string) string { return entry }
func (stubShellHandler) SaveBeforeHistory()                {}
func (stubShellHandler) RestoreAfterHistory()              {}
func (stubShellHandler) BranchFromID(context.Context, string, BranchSummary) func() {
	return nil
}

// TestFlowingModeDoesNotCropOverflowingTree is the regression guard for the
// shell/prompt REPL behaviour: when the rendered tree is taller than the
// terminal, the flowing (shell) renderer must NOT crop it to the viewport. It
// returns every line so tuist pushes the overflow into the terminal's native
// scrollback and the newest rows stay onscreen -- unlike the non-shell
// renderer, which clips to the viewport so the frame never exceeds the terminal
// height, dropping the rows furthest from the focus.
func TestFlowingModeDoesNotCropOverflowingTree(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	const rows = 16
	const steps = 50

	mkFrontend := func(shell bool) *frontendPretty {
		db := dagui.NewDB()
		rootID := prettyTestSpanID(1)
		start := time.Unix(100, 0)
		snapshots := []dagui.SpanSnapshot{
			{
				ID:        rootID,
				TraceID:   prettyTestTraceID(),
				Name:      "root call",
				StartTime: start,
				EndTime:   start.Add(time.Duration(steps+2) * time.Second),
				Final:     true,
			},
		}
		// Enough sibling rows to overflow the terminal several times over.
		for i := range steps {
			snapshots = append(snapshots, dagui.SpanSnapshot{
				ID:        prettyTestSpanID(byte(2 + i)),
				TraceID:   prettyTestTraceID(),
				Name:      fmt.Sprintf("step %02d", i),
				StartTime: start.Add(time.Duration(i+1) * time.Second),
				EndTime:   start.Add(time.Duration(i+2) * time.Second),
				ParentID:  rootID,
				Final:     true,
			})
		}
		db.ImportSnapshots(snapshots)
		db.SetPrimarySpan(rootID)

		term := tuist.NewHeadlessTerminal(120, rows)
		fe := newWithTerminal(io.Discard, db, term)
		fe.FrontendOpts.Verbosity = dagui.ShowCompletedVerbosity
		fe.FrontendOpts.GCThreshold = time.Hour
		// Keep the root expanded: in shell mode a completed depth-0 span otherwise
		// rolls up to a summary, so the tree wouldn't overflow the viewport.
		fe.FrontendOpts.SpanExpanded = map[dagui.SpanID]bool{rootID: true}
		// Focus an early row so the non-shell crop window sits near the top and
		// the final steps fall below the fold (and so get cropped).
		fe.FocusedSpan = prettyTestSpanID(2)
		if shell {
			fe.shell = stubShellHandler{}
		}

		fe.recalculateViewLocked()
		return fe
	}

	// The non-shell view keeps the newest rows onscreen, so it crops from the
	// top: the first step is the row that falls off.
	firstStep := fmt.Sprintf("step %02d", 0)
	lastStep := fmt.Sprintf("step %02d", steps-1)

	// Non-shell: the tree is clipped to the viewport, so the frame never exceeds
	// the terminal height and the oldest rows (the first steps) are dropped.
	feClip := mkFrontend(false)
	if feClip.flowingMode() {
		t.Fatal("non-shell frontend should not be in flowing mode")
	}
	clipLines := feClip.tui.Frame()
	clip := strings.Join(clipLines, "\n")
	if len(clipLines) > rows {
		t.Fatalf("non-shell frame = %d lines, want <= %d (viewport-clipped)", len(clipLines), rows)
	}
	if strings.Contains(clip, firstStep) {
		t.Fatalf("non-shell frame unexpectedly kept cropped row %q:\n%s", firstStep, clip)
	}

	// Shell (flowing): the tree is not cropped -- the frame runs taller than the
	// terminal, and every row (including the first, which non-shell dropped) is
	// present so tuist can flow the overflow into scrollback while the newest
	// rows stay onscreen.
	feFlow := mkFrontend(true)
	if !feFlow.flowingMode() {
		t.Fatal("shell frontend should be in flowing mode")
	}
	flowLines := feFlow.tui.Frame()
	flow := strings.Join(flowLines, "\n")
	if len(flowLines) <= rows {
		t.Fatalf("flowing frame = %d lines, want > %d (uncropped, overflowing into scrollback)", len(flowLines), rows)
	}
	if !strings.Contains(flow, firstStep) {
		t.Fatalf("flowing frame dropped row %q:\n%s", firstStep, flow)
	}
	if !strings.Contains(flow, lastStep) {
		t.Fatalf("flowing frame dropped row %q:\n%s", lastStep, flow)
	}
}

// newFlowingNavFrontend builds a live shell (flowing-mode) frontend with a root
// call and `steps` sibling rows that overflow a `rows`-tall terminal, then
// enters nav mode (autoFocus off) focused on step `focusStep`. It returns the
// frontend and the bottom-`rows` visible window that tuist would show.
func newFlowingNavFrontend(t *testing.T, rows, steps, focusStep int) (*frontendPretty, []string) {
	t.Helper()
	db := dagui.NewDB()
	rootID := prettyTestSpanID(1)
	start := time.Unix(100, 0)
	snapshots := []dagui.SpanSnapshot{
		{
			ID:        rootID,
			TraceID:   prettyTestTraceID(),
			Name:      "root call",
			StartTime: start,
			EndTime:   start.Add(time.Duration(steps+2) * time.Second),
			Final:     true,
		},
	}
	for i := range steps {
		snapshots = append(snapshots, dagui.SpanSnapshot{
			ID:        prettyTestSpanID(byte(2 + i)),
			TraceID:   prettyTestTraceID(),
			Name:      fmt.Sprintf("step %02d", i),
			StartTime: start.Add(time.Duration(i+1) * time.Second),
			EndTime:   start.Add(time.Duration(i+2) * time.Second),
			ParentID:  rootID,
			Final:     true,
		})
	}
	db.ImportSnapshots(snapshots)
	db.SetPrimarySpan(rootID)

	term := tuist.NewHeadlessTerminal(120, rows)
	fe := newWithTerminal(io.Discard, db, term)
	fe.FrontendOpts.Verbosity = dagui.ShowCompletedVerbosity
	fe.FrontendOpts.GCThreshold = time.Hour
	fe.FrontendOpts.SpanExpanded = map[dagui.SpanID]bool{rootID: true}
	fe.shell = stubShellHandler{}
	// Navigate up into the history: leave the bottom (autoFocus off) and focus
	// the requested row.
	fe.autoFocus = false
	fe.FocusedSpan = prettyTestSpanID(byte(2 + focusStep))

	fe.recalculateViewLocked()

	if !fe.flowingMode() {
		t.Fatal("shell frontend should be in flowing mode")
	}

	// The visible window is the bottom `rows` lines of the (over-tall) frame --
	// exactly what tuist shows on the terminal.
	frameLines := fe.tui.Frame()
	visibleLines := frameLines
	if len(frameLines) > rows {
		visibleLines = frameLines[len(frameLines)-rows:]
	}
	return fe, visibleLines
}

// TestFlowingModeNavKeepsFocusOnscreen is the regression guard for navigating
// up through the history in flowing (shell/prompt) mode: once the focused item
// would scroll off the top, the flowing renderer must crop everything below it
// so it stays onscreen. Without the crop the focused row scrolls off the top of
// the viewport into scrollback -- moving "up and offscreen" -- because tuist
// only shows the bottom `height` lines of the over-tall frame.
func TestFlowingModeNavKeepsFocusOnscreen(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	const rows = 16
	const steps = 50
	const focusStep = 25

	fe, visibleLines := newFlowingNavFrontend(t, rows, steps, focusStep)
	visible := strings.Join(visibleLines, "\n")

	focusRow := fmt.Sprintf("step %02d", focusStep)
	belowRow := fmt.Sprintf("step %02d", steps-1)

	// The focused row must be onscreen (within the visible window).
	if !strings.Contains(visible, focusRow) {
		t.Fatalf("focused row %q scrolled offscreen; visible window:\n%s", focusRow, visible)
	}
	// Everything below the focused item is cropped: rows further down must not
	// appear anywhere in the frame (they've been cropped, not just scrolled
	// into scrollback).
	frame := strings.Join(fe.tui.Frame(), "\n")
	if strings.Contains(frame, belowRow) {
		t.Fatalf("row %q below the focus was not cropped:\n%s", belowRow, frame)
	}

	// A "… N lines below …" hint marks the cropped remainder so the user can
	// tell content was hidden (and watch the count grow as output streams in).
	hintRe := regexp.MustCompile(`… (\d+) lines below …`)
	m := hintRe.FindStringSubmatch(visible)
	if m == nil {
		t.Fatalf("missing '… N lines below …' hint in visible window:\n%s", visible)
	}
	gotBelow, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("bad hint count %q: %v", m[1], err)
	}
	if gotBelow <= 0 {
		t.Fatalf("hint reported %d lines below, want a positive count:\n%s", gotBelow, visible)
	}
}

// TestFlowingModeNavNoCropWhenFocusOnscreen verifies the crop only kicks in when
// it's actually needed: moving up a row or two while the focused item is still
// within the visible window must NOT crop the newest output or show the "lines
// below" hint -- that would be jarring.
func TestFlowingModeNavNoCropWhenFocusOnscreen(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	const rows = 16
	const steps = 50
	// A row very close to the bottom: its header stays within the bottom
	// viewportHeight lines, so nothing needs cropping.
	const focusStep = steps - 2

	_, visibleLines := newFlowingNavFrontend(t, rows, steps, focusStep)
	visible := strings.Join(visibleLines, "\n")

	focusRow := fmt.Sprintf("step %02d", focusStep)
	lastRow := fmt.Sprintf("step %02d", steps-1)

	// The focused row is onscreen without any cropping...
	if !strings.Contains(visible, focusRow) {
		t.Fatalf("focused row %q not visible:\n%s", focusRow, visible)
	}
	// ...and the newest row below it stays visible (it was NOT cropped away).
	if !strings.Contains(visible, lastRow) {
		t.Fatalf("newest row %q was cropped even though the focus was onscreen:\n%s", lastRow, visible)
	}
	// No "lines below" hint, since nothing was hidden.
	if strings.Contains(visible, "lines below") || strings.Contains(visible, "line below") {
		t.Fatalf("unexpected crop hint when the focus was already onscreen:\n%s", visible)
	}
}
// TestFlowingModeScopedToLiveUnzoomedShell verifies flowingMode is only active
// for live, un-zoomed shell rendering -- the final report and an explicitly
// zoomed span keep the viewport-clipped behaviour that's correct for those
// focused views.
func TestFlowingModeScopedToLiveUnzoomedShell(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	db := dagui.NewDB()
	rootID := prettyTestSpanID(1)
	childID := prettyTestSpanID(2)
	start := time.Unix(100, 0)
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{
			ID: rootID, TraceID: prettyTestTraceID(), Name: "root call",
			StartTime: start, EndTime: start.Add(2 * time.Second), Final: true,
		},
		{
			ID: childID, TraceID: prettyTestTraceID(), Name: "child op",
			StartTime: start.Add(time.Second), EndTime: start.Add(2 * time.Second),
			ParentID: rootID, Final: true,
		},
	})
	db.SetPrimarySpan(rootID)

	fe := NewWithDB(io.Discard, db)
	fe.shell = stubShellHandler{}
	fe.recalculateViewLocked()

	if !fe.flowingMode() {
		t.Fatal("live un-zoomed shell should be in flowing mode")
	}

	// The final report keeps the clipped behaviour.
	fe.finalRender = true
	if fe.flowingMode() {
		t.Fatal("final render must not use flowing mode")
	}
	fe.finalRender = false

	// A zoomed span keeps the clipped, top-anchored behaviour.
	fe.rowsView.Zoomed = db.Spans.Map[childID]
	if fe.flowingMode() {
		t.Fatal("a zoomed span must not use flowing mode")
	}
	fe.rowsView.Zoomed = nil
	if !fe.flowingMode() {
		t.Fatal("un-zooming should restore flowing mode")
	}
}

// TestConversationTranscriptStyling verifies the conversation-mode message
// rendering reads as a transcript rather than a span tree: no bold-pipe
// gutters and no "user"/"assistant"/"tool" role labels, with the subtle
// per-role cues in their place -- a shaded background behind the user's
// prompt, dim italic for thinking, plain indented prose for the assistant,
// and a faint dot for tool calls.
func TestConversationTranscriptStyling(t *testing.T) {
	db := dagui.NewDB()
	rootID := prettyTestSpanID(1)
	userID := prettyTestSpanID(2)
	thinkID := prettyTestSpanID(3)
	asstID := prettyTestSpanID(4)
	toolID := prettyTestSpanID(5)
	start := time.Unix(100, 0)
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{
			ID: rootID, TraceID: prettyTestTraceID(), Name: "shell",
			StartTime: start, EndTime: start.Add(10 * time.Second), Final: true,
		},
		{
			ID: userID, TraceID: prettyTestTraceID(), Name: "LLM prompt",
			Message: "received", LLMRole: "user", ParentID: rootID,
			StartTime: start.Add(time.Second), EndTime: start.Add(2 * time.Second), Final: true,
		},
		{
			ID: thinkID, TraceID: prettyTestTraceID(), Name: "thinking",
			Message: "received", LLMRole: "assistant", LLMThinking: true, ParentID: rootID,
			StartTime: start.Add(3 * time.Second), EndTime: start.Add(4 * time.Second), Final: true,
		},
		{
			ID: asstID, TraceID: prettyTestTraceID(), Name: "LLM response",
			Message: "received", LLMRole: "assistant", ParentID: rootID,
			StartTime: start.Add(5 * time.Second), EndTime: start.Add(6 * time.Second), Final: true,
		},
		{
			ID: toolID, TraceID: prettyTestTraceID(), Name: "Find",
			LLMRole: "assistant", LLMTool: "Find", ParentID: rootID,
			StartTime: start.Add(7 * time.Second), EndTime: start.Add(8 * time.Second), Final: true,
		},
	})
	db.SetPrimarySpan(rootID)

	term := tuist.NewHeadlessTerminal(120, 60)
	fe := newWithTerminal(io.Discard, db, term)
	// Force a colour profile so we can assert the per-role SGR styling; the
	// screen tool strips ANSI, so a unit test is the only way to see it.
	fe.profile = termenv.ANSI
	fe.logs.Profile = termenv.ANSI
	fe.shell = stubShellHandler{}
	fe.FrontendOpts.Verbosity = dagui.ShowCompletedVerbosity

	setLog := func(id dagui.SpanID, text string) {
		logs := NewVterm(termenv.ANSI)
		logs.SetWidth(120)
		_, _ = logs.Write([]byte(text + "\n"))
		fe.logs.Logs[id] = logs
	}
	setLog(userID, "hello there")
	setLog(thinkID, "let me think")
	setLog(asstID, "here is my reply")
	setLog(toolID, `{"pattern": "*"}`)

	fe.recalculateViewLocked()

	frame := strings.Join(fe.tui.Frame(), "\n")
	plain := stripANSICodes(frame)

	// No span-tree pipe gutters on the transcript.
	if strings.Contains(plain, VertBoldBar) {
		t.Fatalf("conversation transcript still shows bold-pipe gutter (%q):\n%s", VertBoldBar, plain)
	}
	// No role-word labels.
	for _, word := range []string{"assistant", "user", "tool"} {
		if strings.Contains(plain, word) {
			t.Fatalf("conversation transcript still shows role label %q:\n%s", word, plain)
		}
	}
	// The user's prompt sits on a shaded background (ANSIBrightBlack bg = SGR
	// 100).
	if !containsStyledLine(frame, "hello there", "\x1b[100m") {
		t.Fatalf("user prompt is not rendered on a shaded background:\n%s", visibleEscapes(frame))
	}
	// Thinking is dim italic bright-black (SGR 90;3).
	if !containsStyledLine(frame, "let me think", "\x1b[90;3m") {
		t.Fatalf("thinking is not rendered dim italic:\n%s", visibleEscapes(frame))
	}
	// Tool calls keep a faint-dot cue.
	if !strings.Contains(plain, "• Find") {
		t.Fatalf("tool call missing faint-dot cue:\n%s", plain)
	}
}

func stripANSICodes(s string) string {
	return regexp.MustCompile("\x1b\\[[0-9;]*m").ReplaceAllString(s, "")
}

// TestUserPromptLeadingGutterShaded is a regression test for the user's prompt
// rendering its two-space leading gutter unshaded on the first line, so the
// shaded block started one gutter-width in while the continuation lines carried
// the gutter inside their background. The gutter -- and, when the row is
// focused, the "❯ " cue that stands in for it -- is now shaded on line 0 too, so
// the block lines up flush at the margin without an unshaded hole punched in it.
func TestUserPromptLeadingGutterShaded(t *testing.T) {
	rootID := prettyTestSpanID(1)
	userID := prettyTestSpanID(2)
	asstID := prettyTestSpanID(3)
	start := time.Unix(100, 0)

	// promptLine renders the transcript with focusID focused and returns the raw
	// (ANSI-preserving) line carrying the user's prompt.
	promptLine := func(t *testing.T, focusID dagui.SpanID) string {
		db := dagui.NewDB()
		db.ImportSnapshots([]dagui.SpanSnapshot{
			{ID: rootID, TraceID: prettyTestTraceID(), Name: "shell", StartTime: start, EndTime: start.Add(10 * time.Second), Final: true},
			{
				ID: userID, TraceID: prettyTestTraceID(), Name: "LLM prompt",
				Message: "received", LLMRole: "user", ParentID: rootID,
				StartTime: start.Add(time.Second), EndTime: start.Add(2 * time.Second), Final: true,
			},
			{
				ID: asstID, TraceID: prettyTestTraceID(), Name: "LLM response",
				Message: "received", LLMRole: "assistant", ParentID: rootID,
				StartTime: start.Add(3 * time.Second), EndTime: start.Add(4 * time.Second), Final: true,
			},
		})
		db.SetPrimarySpan(rootID)

		term := tuist.NewHeadlessTerminal(120, 60)
		fe := newWithTerminal(io.Discard, db, term)
		fe.profile = termenv.ANSI
		fe.logs.Profile = termenv.ANSI
		fe.shell = stubShellHandler{}
		fe.FrontendOpts.Verbosity = dagui.ShowCompletedVerbosity

		setLog := func(id dagui.SpanID, text string) {
			logs := NewVterm(termenv.ANSI)
			logs.SetWidth(120)
			// Write as markdown -- matching how live conversation messages stream --
			// so redraw omits the gutter prefix on the inline first line (the
			// terminal path prefixes every line, which would mask the bug).
			_, _ = logs.WriteMarkdown([]byte(text + "\n"))
			fe.logs.Logs[id] = logs
		}
		setLog(userID, "hello there")
		setLog(asstID, "here is my reply")

		fe.autoFocus = false
		fe.FocusedSpan = focusID
		fe.recalculateViewLocked()

		for _, line := range strings.Split(strings.Join(fe.tui.RenderLines(), "\n"), "\n") {
			if strings.Contains(stripANSICodes(line), "hello there") {
				return line
			}
		}
		t.Fatal("user prompt row not rendered")
		return ""
	}

	t.Run("unfocused", func(t *testing.T) {
		// Focus the assistant reply so the prompt renders with its plain gutter.
		// The line must open with the shaded-background SGR (ANSIBrightBlack bg =
		// SGR 100): the gutter is shaded rather than two plain spaces before it.
		line := promptLine(t, asstID)
		if !strings.HasPrefix(line, "\x1b[100m") {
			t.Fatalf("user prompt gutter is not shaded; line = %q", visibleEscapes(line))
		}
	})

	t.Run("focused", func(t *testing.T) {
		// The "❯ " cue replaces the gutter, so it must carry the same shaded
		// background (SGR 100) -- otherwise it punches an unshaded hole in the
		// block. Check the styling that precedes the cue, independent of SGR order.
		line := promptLine(t, userID)
		before, _, found := strings.Cut(line, LLMPrompt)
		if !found {
			t.Fatalf("focused user prompt missing its %q cue; line = %q", LLMPrompt, visibleEscapes(line))
		}
		if !strings.Contains(before, "100") {
			t.Fatalf("focused user prompt cue is not shaded; line = %q", visibleEscapes(line))
		}
	})
}

// TestFocusedAssistantMessageSinglePrompt is a regression test for a doubled
// "❯ " focus cue on a focused assistant message. The assistant's reply/thinking
// opens with a blank separator line and then renders its content carrying the
// focus cue; renderStep was emitting the cue twice -- once on that separator
// line (via the shared shell-mode gutter switch) and again on the content line
// -- stranding a lone "❯" on the blank line above the message. Only the content
// line should carry the cue.
func TestFocusedAssistantMessageSinglePrompt(t *testing.T) {
	db := dagui.NewDB()
	rootID := prettyTestSpanID(1)
	userID := prettyTestSpanID(2)
	asstID := prettyTestSpanID(3)
	start := time.Unix(100, 0)
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{ID: rootID, TraceID: prettyTestTraceID(), Name: "shell", StartTime: start, EndTime: start.Add(10 * time.Second), Final: true},
		{
			ID: userID, TraceID: prettyTestTraceID(), Name: "LLM prompt",
			Message: "received", LLMRole: "user", ParentID: rootID,
			StartTime: start.Add(time.Second), EndTime: start.Add(2 * time.Second), Final: true,
		},
		{
			ID: asstID, TraceID: prettyTestTraceID(), Name: "LLM response",
			Message: "received", LLMRole: "assistant", ParentID: rootID,
			StartTime: start.Add(3 * time.Second), EndTime: start.Add(4 * time.Second), Final: true,
		},
	})
	db.SetPrimarySpan(rootID)

	term := tuist.NewHeadlessTerminal(120, 60)
	fe := newWithTerminal(io.Discard, db, term)
	fe.profile = termenv.ANSI
	fe.logs.Profile = termenv.ANSI
	fe.shell = stubShellHandler{}
	fe.FrontendOpts.Verbosity = dagui.ShowCompletedVerbosity

	setLog := func(id dagui.SpanID, text string) {
		logs := NewVterm(termenv.ANSI)
		logs.SetWidth(120)
		_, _ = logs.WriteMarkdown([]byte(text + "\n"))
		fe.logs.Logs[id] = logs
	}
	setLog(userID, "hello there")
	setLog(asstID, "here is my reply")

	// Focus the assistant reply so it renders with its "❯ " cue; keep the user
	// prompt unfocused so its own gutter stays plain.
	fe.autoFocus = false
	fe.FocusedSpan = asstID
	fe.recalculateViewLocked()

	lines := strings.Split(strings.Join(fe.tui.RenderLines(), "\n"), "\n")
	cues := 0
	for _, l := range lines {
		cues += strings.Count(stripANSICodes(l), LLMPrompt)
	}
	if cues != 1 {
		t.Fatalf("expected exactly one %q focus cue for the focused assistant message, got %d:\n%s",
			LLMPrompt, cues, visibleEscapes(strings.Join(lines, "\n")))
	}
}

// TestUserPromptPaddedAndSeparatedFromTools verifies the live shell view sets
// the user's prompt apart: a shaded (ANSIBrightBlack) blank line above and
// below extends its block into a padded card, and a tool call that opens the
// turn -- which carries no leading blank of its own -- gets a plain separating
// blank so it doesn't sit flush beneath the card.
func TestUserPromptPaddedAndSeparatedFromTools(t *testing.T) {
	db := dagui.NewDB()
	rootID := prettyTestSpanID(1)
	userID := prettyTestSpanID(2)
	toolID := prettyTestSpanID(3)
	start := time.Unix(100, 0)
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{ID: rootID, TraceID: prettyTestTraceID(), Name: "shell", StartTime: start, EndTime: start.Add(10 * time.Second), Final: true},
		{
			ID: userID, TraceID: prettyTestTraceID(), Name: "LLM prompt",
			Message: "received", LLMRole: "user", ParentID: rootID,
			StartTime: start.Add(time.Second), EndTime: start.Add(2 * time.Second), Final: true,
		},
		{
			ID: toolID, TraceID: prettyTestTraceID(), Name: "Find",
			LLMRole: "assistant", LLMTool: "Find", ParentID: rootID,
			StartTime: start.Add(3 * time.Second), EndTime: start.Add(4 * time.Second), Final: true,
		},
	})
	db.SetPrimarySpan(rootID)

	term := tuist.NewHeadlessTerminal(120, 60)
	fe := newWithTerminal(io.Discard, db, term)
	fe.profile = termenv.ANSI
	fe.logs.Profile = termenv.ANSI
	fe.shell = stubShellHandler{}
	fe.FrontendOpts.Verbosity = dagui.ShowCompletedVerbosity
	// Keep the prompt unfocused.
	fe.autoFocus = false
	fe.FocusedSpan = toolID

	logs := NewVterm(termenv.ANSI)
	logs.SetWidth(120)
	_, _ = logs.WriteMarkdown([]byte("hello there\n"))
	fe.logs.Logs[userID] = logs

	fe.recalculateViewLocked()

	lines := fe.tui.Frame()
	isShaded := func(l string) bool {
		return strings.TrimSpace(stripANSICodes(l)) == "" && strings.Contains(l, "\x1b[100m")
	}
	contentIdx, toolIdx := -1, -1
	for i, l := range lines {
		p := stripANSICodes(l)
		if contentIdx == -1 && strings.Contains(p, "hello there") {
			contentIdx = i
		}
		if toolIdx == -1 && strings.Contains(p, "Find") {
			toolIdx = i
		}
	}
	if contentIdx <= 0 || toolIdx == -1 {
		t.Fatalf("missing rows (content=%d tool=%d):\n%s", contentIdx, toolIdx, visibleEscapes(strings.Join(lines, "\n")))
	}
	// Shaded blank line above and below the prompt content.
	if !isShaded(lines[contentIdx-1]) {
		t.Fatalf("expected a shaded blank line above the prompt; got %q", visibleEscapes(lines[contentIdx-1]))
	}
	if !isShaded(lines[contentIdx+1]) {
		t.Fatalf("expected a shaded blank line below the prompt; got %q", visibleEscapes(lines[contentIdx+1]))
	}
	// The tool call is separated from the card by a plain (unshaded) blank line.
	if strings.TrimSpace(stripANSICodes(lines[toolIdx-1])) != "" {
		t.Fatalf("expected a blank line before the tool call; got %q", visibleEscapes(lines[toolIdx-1]))
	}
	if isShaded(lines[toolIdx-1]) {
		t.Fatalf("separator before the tool should be plain, not shaded; got %q", visibleEscapes(lines[toolIdx-1]))
	}
}

func containsStyledLine(frame, text, styleSeq string) bool {
	for _, line := range strings.Split(frame, "\n") {
		if !strings.Contains(stripANSICodes(line), text) {
			continue
		}
		if strings.Contains(line, styleSeq) {
			return true
		}
	}
	return false
}

func visibleEscapes(frame string) string {
	return strings.ReplaceAll(frame, "\x1b", "\\x1b")
}

