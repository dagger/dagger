package idtui

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/muesli/termenv"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/dagger/dagger/dagql/dagui"
)

// TestASCIIReporterScopedToolCallReport covers the shape core renders for an
// LLM tool result: the report is scoped to the tool call's span, every row is
// force-expanded (so output behind a roll-up/tool-call boundary survives), the
// tool's own output stays whole while nested work is abridged to a tail, and
// the whole-trace sections (CONVERSATION/SERVICES) stay out of it.
func TestASCIIReporterScopedToolCallReport(t *testing.T) {
	db := dagui.NewDB()
	rootID := prettyTestSpanID(1)
	loopID := prettyTestSpanID(2)
	promptID := prettyTestSpanID(3)
	callID := prettyTestSpanID(4)
	execID := prettyTestSpanID(5)
	displayID := prettyTestSpanID(6)
	start := time.Unix(100, 0)
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{
			ID:        rootID,
			TraceID:   prettyTestTraceID(),
			Name:      "shell",
			StartTime: start,
			EndTime:   start.Add(5 * time.Second),
			Final:     true,
		},
		{
			// The conversation runs beneath this span. It's passthrough, like
			// the real loop span.
			ID:          loopID,
			TraceID:     prettyTestTraceID(),
			Name:        "LLM.loop",
			ParentID:    rootID,
			Passthrough: true,
			StartTime:   start,
			EndTime:     start.Add(5 * time.Second),
			Final:       true,
		},
		{
			ID:        promptID,
			TraceID:   prettyTestTraceID(),
			Name:      "LLM prompt",
			ParentID:  loopID,
			Message:   "PROMPT-TEXT",
			LLMRole:   "user",
			StartTime: start,
			EndTime:   start.Add(time.Second),
			Final:     true,
		},
		{
			// The tool call's display span (displayPhases.StartToolCall):
			// every provider builds one, replay included, and core scopes the
			// tool result's report to it.
			ID:          displayID,
			TraceID:     prettyTestTraceID(),
			Name:        "report",
			ParentID:    loopID,
			Boundary:    true,
			RollUpLogs:  true,
			RollUpSpans: true,
			LLMTool:     "report",
			LLMRole:     "assistant",
			StartTime:   start.Add(time.Second),
			EndTime:     start.Add(4 * time.Second),
			Final:       true,
		},
		{
			ID:        callID,
			TraceID:   prettyTestTraceID(),
			Name:      "ReportAgent.report",
			ParentID:  displayID,
			StartTime: start.Add(time.Second),
			EndTime:   start.Add(4 * time.Second),
			Final:     true,
		},
		{
			ID:        execID,
			TraceID:   prettyTestTraceID(),
			Name:      "Container.withExec",
			ParentID:  callID,
			StartTime: start.Add(time.Second),
			EndTime:   start.Add(3 * time.Second),
			Final:     true,
		},
	})
	db.SetPrimarySpan(displayID)

	fe := NewASCIIReporterWithDB(io.Discard, db)
	fe.logs.Logs[callID] = testVterm(t, "LINE-01\nLINE-02\nLINE-03\n")
	fe.logs.Logs[execID] = testVterm(t, "NESTED-NOISE-01\nNESTED-NOISE-02\nNESTED-NOISE-03\nNESTED-NOISE-04\n")

	fe.SetReportRenderOpts(ReportRenderOpts{
		Verbosity:       dagui.ShowCompletedVerbosity,
		ExpandCompleted: true,
		ExpandSpans: map[dagui.SpanID]bool{
			displayID: true, callID: true, execID: true,
		},
		NestedLogLimit: 2,
		ScopedSubtree:  true,
	})

	var buf bytes.Buffer
	if err := fe.FinalRender(&buf); err != nil {
		t.Fatalf("FinalRender: %v", err)
	}
	got := buf.String()
	t.Logf("rendered report:\n%s", got)

	for _, want := range []string{"ReportAgent.report", "LINE-01", "LINE-03", "Container.withExec", "NESTED-NOISE-04"} {
		if !strings.Contains(got, want) {
			t.Errorf("report missing %q:\n%s", want, got)
		}
	}
	// Nested output is abridged to its tail.
	if strings.Contains(got, "NESTED-NOISE-01") {
		t.Errorf("nested output was not abridged:\n%s", got)
	}
	// Whole-trace framing stays out: the verdict belongs to the run that
	// contains the tool call, and the CONVERSATION promotion would replace the
	// subtree's rows with the enclosing agent's transcript.
	if strings.Contains(got, "TRACE") || strings.Contains(got, "PASSED") {
		t.Errorf("scoped report leaked the whole-trace verdict:\n%s", got)
	}
	if strings.Contains(got, "PROMPT-TEXT") {
		t.Errorf("scoped report leaked the enclosing conversation:\n%s", got)
	}
	// ...and the promotion must not have mutated the shared DB either: a
	// later whole-trace render of the same (cached) DB would inherit it.
	if db.Spans.Map[loopID].Passthrough != true || len(db.Spans.Map[loopID].RevealedSpans.Order) != 0 {
		t.Errorf("scoped render mutated the DB: revealed=%d", len(db.Spans.Map[loopID].RevealedSpans.Order))
	}
}

// twoToolCallChecksDB builds a session DB in the shape an agent produces: one
// root, two sibling tool-call spans, each running its own check. It's the
// minimal reproduction of the leak a trace-global CHECKS section causes -- the
// DB behind a scoped report holds the WHOLE session, not just the tool call
// being reported on.
func twoToolCallChecksDB(t *testing.T) (db *dagui.DB, callA, callB dagui.SpanID) {
	t.Helper()
	db = dagui.NewDB()
	rootID := prettyTestSpanID(1)
	callAID := prettyTestSpanID(2)
	goodID := prettyTestSpanID(3)
	callBID := prettyTestSpanID(4)
	badID := prettyTestSpanID(5)
	start := time.Unix(100, 0)
	end := start.Add(2 * time.Second)
	failed := sdktrace.Status{Code: codes.Error}
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{
			ID:        rootID,
			TraceID:   prettyTestTraceID(),
			Name:      "shell",
			StartTime: start,
			EndTime:   end,
			Final:     true,
		},
		{
			ID:        callAID,
			TraceID:   prettyTestTraceID(),
			Name:      "Workspace.check (call 1)",
			ParentID:  rootID,
			StartTime: start,
			EndTime:   end,
			Final:     true,
		},
		{
			ID:        goodID,
			TraceID:   prettyTestTraceID(),
			Name:      "demo:good",
			ParentID:  callAID,
			CheckName: "demo:good",
			StartTime: start,
			EndTime:   end,
			Final:     true,
		},
		{
			ID:        callBID,
			TraceID:   prettyTestTraceID(),
			Name:      "Workspace.check (call 2)",
			ParentID:  rootID,
			StartTime: start,
			EndTime:   end,
			Final:     true,
		},
		{
			ID:        badID,
			TraceID:   prettyTestTraceID(),
			Name:      "demo:bad",
			ParentID:  callBID,
			CheckName: "demo:bad",
			Status:    failed,
			StartTime: start,
			EndTime:   end,
			Final:     true,
		},
	})
	return db, callAID, callBID
}

// TestASCIIReporterScopedChecksExcludeOtherToolCalls covers the CHECKS section
// of a scoped report: DB.SurfacedChecks walks every span in the (session-wide,
// cached) DB, so without subtree filtering a `check` tool call renders the
// checks some EARLIER tool call ran -- an agent reading its own tool result
// would see work it never asked for.
func TestASCIIReporterScopedChecksExcludeOtherToolCalls(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	db, _, callB := twoToolCallChecksDB(t)
	db.SetPrimarySpan(callB)

	fe := NewASCIIReporterWithDB(io.Discard, db)
	fe.SetReportRenderOpts(ReportRenderOpts{
		Verbosity:       dagui.ShowCompletedVerbosity,
		ExpandCompleted: true,
		ScopedSubtree:   true,
	})

	var buf bytes.Buffer
	if err := fe.FinalRender(&buf); err != nil {
		t.Fatalf("FinalRender: %v", err)
	}
	got := buf.String()
	t.Logf("rendered report:\n%s", got)

	// This tool call's own check is still summarized...
	if !strings.Contains(got, "demo:bad") {
		t.Errorf("scoped report dropped its own check:\n%s", got)
	}
	// ...but the previous tool call's is not.
	if strings.Contains(got, "demo:good") {
		t.Errorf("scoped report leaked another tool call's check:\n%s", got)
	}

	// The unscoped render of the same DB still covers the whole trace.
	db.SetPrimarySpan(prettyTestSpanID(1)) // back to the session root
	fe2 := NewASCIIReporterWithDB(io.Discard, db)
	fe2.SetReportRenderOpts(ReportRenderOpts{
		Verbosity:       dagui.ShowCompletedVerbosity,
		ExpandCompleted: true,
	})
	var wholeBuf bytes.Buffer
	if err := fe2.FinalRender(&wholeBuf); err != nil {
		t.Fatalf("FinalRender (unscoped): %v", err)
	}
	whole := wholeBuf.String()
	if !strings.Contains(whole, "demo:good") || !strings.Contains(whole, "demo:bad") {
		t.Errorf("unscoped report lost a check:\n%s", whole)
	}
}

// TestASCIIReporterScopedReportWithoutChecksKeepsOwnOutput covers the other
// half: when nothing in the scoped subtree is a check there must be no CHECKS
// section at all, and -- crucially -- the tool call's own rows and output must
// survive. A trace-global checks section claims the report's row set (the
// progress tree only renders as a fallback), so a leaked check from an earlier
// call used to swallow this call's own printed output entirely.
func TestASCIIReporterScopedReportWithoutChecksKeepsOwnOutput(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	db, _, _ := twoToolCallChecksDB(t)

	// A third tool call that matched no checks: it ran a module function that
	// printed a warning, and nothing else.
	rootID := prettyTestSpanID(1)
	callCID := prettyTestSpanID(6)
	fnID := prettyTestSpanID(7)
	start := time.Unix(100, 0)
	end := start.Add(time.Second)
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{
			ID:        callCID,
			TraceID:   prettyTestTraceID(),
			Name:      "Workspace.check (call 3)",
			ParentID:  rootID,
			StartTime: start,
			EndTime:   end,
			Final:     true,
		},
		{
			ID:        fnID,
			TraceID:   prettyTestTraceID(),
			Name:      "Workspace.check",
			ParentID:  callCID,
			StartTime: start,
			EndTime:   end,
			Final:     true,
		},
	})
	db.SetPrimarySpan(callCID)

	fe := NewASCIIReporterWithDB(io.Discard, db)
	fe.logs.Logs[fnID] = testVterm(t, "WARNING: no checks matched. Available checks: demo:good, demo:bad\n")
	fe.SetReportRenderOpts(ReportRenderOpts{
		Verbosity:       dagui.ShowCompletedVerbosity,
		ExpandCompleted: true,
		ExpandSpans:     map[dagui.SpanID]bool{callCID: true, fnID: true},
		ScopedSubtree:   true,
	})

	var buf bytes.Buffer
	if err := fe.FinalRender(&buf); err != nil {
		t.Fatalf("FinalRender: %v", err)
	}
	got := buf.String()
	t.Logf("rendered report:\n%s", got)

	// The tool call's own row and its own output survive.
	if !strings.Contains(got, "WARNING: no checks matched") {
		t.Errorf("scoped report lost the tool call's own output:\n%s", got)
	}
	// No checks ran in scope, so there is no CHECKS section and no rows from
	// the other tool calls' checks.
	if strings.Contains(got, "CHECKS") {
		t.Errorf("scoped report rendered a CHECKS section for checks it didn't run:\n%s", got)
	}
	for _, leaked := range []string{"demo:good", "demo:bad"} {
		// The warning line legitimately names them; check the check rows'
		// status vocabulary instead.
		if strings.Contains(got, leaked+" ") && strings.Contains(got, "ERROR") {
			t.Errorf("scoped report leaked check %q:\n%s", leaked, got)
		}
	}
}

// TestReportRenderOptsRerunSuggestion covers the per-render plumbing core uses
// to swap the report's rerun vocabulary: a headless reader (an LLM tool result)
// has tools rather than a `dagger` CLI, so the section must carry the caller's
// lines and none of the CLI's. Interactive rendering, which leaves the hook
// nil, keeps the default `dagger check` body (see frontend_pretty_test.go).
func TestReportRenderOptsRerunSuggestion(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	fe := NewASCIIReporterWithDB(io.Discard, rerunReportDB(t))
	fe.SetReportRenderOpts(ReportRenderOpts{
		Verbosity:       dagui.ShowCompletedVerbosity,
		ExpandCompleted: true,
		RerunSuggestion: func(names []string) (string, []string) {
			body := make([]string, 0, len(names))
			for _, name := range names {
				body = append(body, `ReadTrace(check: "`+name+`")`)
			}
			return "SEE FULL TRACE", body
		},
	})

	var buf bytes.Buffer
	if err := fe.FinalRender(&buf); err != nil {
		t.Fatalf("FinalRender: %v", err)
	}
	got := buf.String()
	t.Logf("rendered report:\n%s", got)
	if !strings.Contains(got, "SEE FULL TRACE") ||
		!strings.Contains(got, `ReadTrace(check: "ci:bootstrap")`) {
		t.Fatalf("report missing the injected suggestion:\n%s", got)
	}
	if strings.Contains(got, `dagger check "ci:bootstrap"`) || strings.Contains(got, "RUN LOCALLY") {
		t.Fatalf("report still suggests the CLI command:\n%s", got)
	}
}

// TestASCIIReporterScopedChecksUnderToolBoundary is the render-level half of
// zoom-relative surfacing. core's tool-call display span
// (displayPhases.StartToolCall) is a Boundary, and MCP.Call adopts its context,
// so EVERY check a tool runs sits under a Boundary -- which the whole-trace
// containment walk treats as containment. The result was a tool result with no
// CHECKS section at all, falling back to the raw span tree. Rolled up relative
// to the tool call itself, that boundary sits AT the root and is irrelevant.
//
// Note the Boundary attribute is what makes this a reproduction: replay-driven
// integration tests never create the display span, so the bug is invisible to
// them.
func TestASCIIReporterScopedChecksUnderToolBoundary(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	db := dagui.NewDB()
	rootID := prettyTestSpanID(1)
	toolID := prettyTestSpanID(2)
	checkID := prettyTestSpanID(3)
	noiseID := prettyTestSpanID(4)
	start := time.Unix(100, 0)
	end := start.Add(3 * time.Second)
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{
			ID:        rootID,
			TraceID:   prettyTestTraceID(),
			Name:      "shell",
			StartTime: start,
			EndTime:   end,
			Final:     true,
		},
		{
			// Mirrors StartToolCall: boundary + roll-ups + tool name.
			ID:          toolID,
			TraceID:     prettyTestTraceID(),
			Name:        "check",
			ParentID:    rootID,
			Boundary:    true,
			RollUpLogs:  true,
			RollUpSpans: true,
			LLMTool:     "check",
			LLMRole:     "assistant",
			StartTime:   start,
			EndTime:     end,
			Final:       true,
		},
		{
			ID:        checkID,
			TraceID:   prettyTestTraceID(),
			Name:      "shellcheck:check",
			ParentID:  toolID,
			CheckName: "shellcheck:check",
			StartTime: start,
			EndTime:   end,
			Final:     true,
		},
		{
			ID:        noiseID,
			TraceID:   prettyTestTraceID(),
			Name:      "Container.withExec",
			ParentID:  checkID,
			StartTime: start,
			EndTime:   end,
			Final:     true,
		},
	})

	render := func(primary dagui.SpanID, scoped bool) string {
		t.Helper()
		db.SetPrimarySpan(primary)
		fe := NewASCIIReporterWithDB(io.Discard, db)
		fe.SetReportRenderOpts(ReportRenderOpts{
			Verbosity:       dagui.ShowCompletedVerbosity,
			ExpandCompleted: true,
			ScopedSubtree:   scoped,
		})
		var buf bytes.Buffer
		if err := fe.FinalRender(&buf); err != nil {
			t.Fatalf("FinalRender: %v", err)
		}
		return buf.String()
	}

	// Unzoomed at the DB root, the boundary contains the check: no CHECKS
	// section -- the whole-trace behavior is unchanged.
	before := render(rootID, false)
	if strings.Contains(before, "CHECKS") {
		t.Fatalf("expected the boundary to contain the check at the DB root:\n%s", before)
	}

	// Zoomed to the tool call, the check is what ran inside it.
	got := render(toolID, true)
	t.Logf("rendered report:\n%s", got)
	if !strings.Contains(got, "CHECKS") || !strings.Contains(got, "shellcheck:check") {
		t.Fatalf("scoped report has no CHECKS section for the check it ran:\n%s", got)
	}
	// The compact CHECKS summary replaces the raw tree, so the module-internal
	// work the check did stays out of the tool result.
	if strings.Contains(got, "Container.withExec") {
		t.Errorf("scoped report fell back to the raw tree:\n%s", got)
	}
}

func testVterm(t *testing.T, content string) *Vterm {
	t.Helper()
	vt := NewVterm(termenv.Ascii)
	vt.SetWidth(80)
	if _, err := vt.Write([]byte(content)); err != nil {
		t.Fatalf("write vterm: %v", err)
	}
	return vt
}
