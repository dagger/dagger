package idtui

import (
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

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
	if !(short > tall) {
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
	if lines := fe.renderLiveGlobalTests(tuist.Context{Width: 80}); len(lines) == 0 {
		t.Fatal("live global tests did not render")
	}
	// Claims are per-render-pass state; the real frontend resets them before each
	// pass. Reset here so the final render isn't suppressed by the live render
	// having claimed these same orphan cases.
	fe.claims = newRenderClaims()
	fe.finalRender = true
	lines := fe.renderFinalGlobalTests(tuist.Context{Width: 80})
	testsLine, ok := findPrettyTestLine(lines, "TESTS")
	if !ok {
		t.Fatalf("final global tests did not include TESTS line:\n%s", strings.Join(lines, "\n"))
	}
	if testsLine != "TESTS" {
		t.Fatalf("global final TESTS line = %q, want no indentation", testsLine)
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
			status:   sdktrace.Status{Code: codes.Error, Description: `call function "phpstan": exit code: 1 [traceparent:abc123-def456]`},
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
	if !(bootstrapIdx < nestedIdx && nestedIdx < goLintIdx && nestedIdx < helmLintIdx) {
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
	fe.ciMeta = &ciContext{commit: "abc123", prNumber: "4", isNativeCI: true}

	lines := fe.renderRerunSection(nil)
	joined := strings.Join(lines, "\n")
	// Only the outermost check is re-runnable; the sub-check rolls up to it.
	if strings.Contains(joined, "go:lint") {
		t.Fatalf("RE-RUN listed a sub-check:\n%s", joined)
	}
	if !strings.Contains(joined, `dagger cloud rerun --commit abc123 --check "ci:bootstrap"`) {
		t.Fatalf("missing cloud rerun line:\n%s", joined)
	}
	if !strings.Contains(joined, `dagger check "ci:bootstrap"`) {
		t.Fatalf("missing local check line:\n%s", joined)
	}
	// CI re-run leads; the local check follows.
	if rerunIdx, checkIdx := indexOfLine(lines, "cloud rerun"), indexOfLine(lines, "dagger check"); !(rerunIdx >= 0 && rerunIdx < checkIdx) {
		t.Fatalf("expected cloud rerun before local check (rerun=%d check=%d):\n%s", rerunIdx, checkIdx, joined)
	}
}

func TestRerunSectionLocalOnlyWithoutNativeCI(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	fe := NewWithDB(io.Discard, rerunReportDB(t))
	fe.recalculateViewLocked()
	// No ciMeta (live/local run): only a local check, no cloud rerun.

	lines := fe.renderRerunSection(nil)
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, "cloud rerun") {
		t.Fatalf("did not expect a cloud rerun line without native CI:\n%s", joined)
	}
	if !strings.Contains(joined, `dagger check "ci:bootstrap"`) {
		t.Fatalf("missing local check line:\n%s", joined)
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
	if lines := fe.renderLiveGlobalTests(tuist.Context{Width: 80}); len(lines) != 0 {
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
	if lines := fe.renderLiveGlobalTests(tuist.Context{Width: 80}); len(lines) != 0 {
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
