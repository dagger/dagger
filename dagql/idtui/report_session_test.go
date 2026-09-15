package idtui

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/dagui"
)

// reportScopeFixture builds a two-tool-call trace:
//
//	run
//	├─ ToolA        (LLM tool call)
//	│  ├─ sentinel-work-a
//	│  └─ case-a    (test case)
//	└─ ToolB        (LLM tool call)
//	   └─ sentinel-work-b
//
// It is the shape every scoped-report question is about: what does a report
// scoped to ToolA show, and can anything of ToolB's leak into it (or vice
// versa)?
func reportScopeFixture() (db *dagui.DB, toolA, toolB dagui.SpanID) {
	db = dagui.NewDB()
	rootID := prettyTestSpanID(1)
	toolA = prettyTestSpanID(2)
	workA := prettyTestSpanID(3)
	caseA := prettyTestSpanID(4)
	toolB = prettyTestSpanID(5)
	workB := prettyTestSpanID(6)
	start := time.Unix(100, 0)
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{
			ID: rootID, TraceID: prettyTestTraceID(), Name: "run",
			StartTime: start, EndTime: start.Add(10 * time.Second), Final: true,
		},
		{
			ID: toolA, TraceID: prettyTestTraceID(), Name: "ToolA", LLMTool: "toolA",
			ParentID:  rootID,
			StartTime: start.Add(time.Second), EndTime: start.Add(4 * time.Second), Final: true,
		},
		{
			ID: workA, TraceID: prettyTestTraceID(), Name: "sentinel-work-a",
			ParentID:  toolA,
			StartTime: start.Add(time.Second), EndTime: start.Add(2 * time.Second), Final: true,
		},
		{
			ID: caseA, TraceID: prettyTestTraceID(), Name: "case-a",
			ParentID:  toolA,
			StartTime: start.Add(2 * time.Second), EndTime: start.Add(3 * time.Second),
			TestCaseName: "case-a", TestStatus: dagui.TestStatusSuccess, Final: true,
		},
		{
			ID: toolB, TraceID: prettyTestTraceID(), Name: "ToolB", LLMTool: "toolB",
			ParentID:  rootID,
			StartTime: start.Add(5 * time.Second), EndTime: start.Add(8 * time.Second), Final: true,
		},
		{
			ID: workB, TraceID: prettyTestTraceID(), Name: "sentinel-work-b",
			ParentID:  toolB,
			StartTime: start.Add(6 * time.Second), EndTime: start.Add(7 * time.Second), Final: true,
		},
	})
	db.SetPrimarySpan(rootID)
	return db, toolA, toolB
}

// scopedReportOpts mirrors what core's tool-call path passes: a subtree-scoped
// report rooted at one span. hideTree distinguishes the tool-result render
// (surfaced sections only) from the ReadTrace render (tree included).
func scopedReportOpts(root dagui.SpanID, hideTree bool) ReportRenderOpts {
	return ReportRenderOpts{
		Verbosity:       dagui.ShowCompletedVerbosity,
		ExpandCompleted: true,
		ScopedSubtree:   true,
		Root:            root,
		HideSpanTree:    hideTree,
		AgentStyle:      true,
	}
}

func renderReport(t *testing.T, session *ReportSession, opts ReportRenderOpts) string {
	t.Helper()
	var buf bytes.Buffer
	if err := session.Render(&buf, opts); err != nil {
		t.Fatalf("render report: %v", err)
	}
	return buf.String()
}

// TestToolCallReportHidesSpanTree covers the tool-result render shape: only
// what the call SURFACED (here, its TESTS roll-up), never the span tree. The
// ReadTrace-style render over the same trace still shows the tree.
func TestToolCallReportHidesSpanTree(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	db, toolA, _ := reportScopeFixture()
	session := NewReportSession(db)

	surfaced := renderReport(t, session, scopedReportOpts(toolA, true))
	if strings.Contains(surfaced, "sentinel-work-a") {
		t.Fatalf("surfaced-only report rendered a span-tree row:\n%s", surfaced)
	}
	if !strings.Contains(surfaced, "TESTS") || !strings.Contains(surfaced, "1 passed") {
		t.Fatalf("surfaced-only report dropped the TESTS section:\n%s", surfaced)
	}

	withTree := renderReport(t, session, scopedReportOpts(toolA, false))
	if !strings.Contains(withTree, "sentinel-work-a") {
		t.Fatalf("ReadTrace-style report lost the span tree:\n%s", withTree)
	}
}

// TestScopedReportExcludesForeignTests is the TESTS half of strict scoping: a
// report about a tool call that ran no tests must not close with the TESTS
// section of a DIFFERENT tool call in the same trace.
func TestScopedReportExcludesForeignTests(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	db, _, toolB := reportScopeFixture()
	session := NewReportSession(db)

	report := renderReport(t, session, scopedReportOpts(toolB, true))
	if strings.Contains(report, "case-a") || strings.Contains(report, "TESTS") {
		t.Fatalf("report for a test-free tool call surfaced another call's tests:\n%s", report)
	}
}

// TestConsecutiveScopedRendersDoNotLeak is the regression for the old
// per-session reporter: two renders with different roots over the SAME session
// must not show each other's content, in either order.
func TestConsecutiveScopedRendersDoNotLeak(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	db, toolA, toolB := reportScopeFixture()
	session := NewReportSession(db)

	first := renderReport(t, session, scopedReportOpts(toolA, false))
	if !strings.Contains(first, "sentinel-work-a") {
		t.Fatalf("report for ToolA missing its own work:\n%s", first)
	}
	if strings.Contains(first, "sentinel-work-b") {
		t.Fatalf("report for ToolA included ToolB's work:\n%s", first)
	}

	second := renderReport(t, session, scopedReportOpts(toolB, false))
	if !strings.Contains(second, "sentinel-work-b") {
		t.Fatalf("report for ToolB missing its own work:\n%s", second)
	}
	if strings.Contains(second, "sentinel-work-a") || strings.Contains(second, "TESTS") {
		t.Fatalf("report for ToolB leaked ToolA's content:\n%s", second)
	}

	// ...and back again: the first render's scope isn't sticky either.
	third := renderReport(t, session, scopedReportOpts(toolA, false))
	if !strings.Contains(third, "sentinel-work-a") || strings.Contains(third, "sentinel-work-b") {
		t.Fatalf("re-render of ToolA differs from the first:\n%s", third)
	}
}
