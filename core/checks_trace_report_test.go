package core

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/dagql/dagui"
)

// TestTraceReportGuardPassesThroughSmallReports covers the common case: the
// measured real-world reports (59 B for a single check, ~2 KB scoped) must
// reach the caller byte-identical.
func TestTraceReportGuardPassesThroughSmallReports(t *testing.T) {
	for _, report := range []string{
		"",
		"CHECKS: 1 passed\n",
		"◼ check shellcheck:check\n┃ all good\n\nCHECKS: 1 passed\n\nRUN LOCALLY\n  dagger check\n",
		strings.Repeat("◼ a nested span with a unicode bullet\n", 100),
	} {
		if got := guardTraceReport(report); got != report {
			t.Fatalf("guardTraceReport modified an under-budget report:\ngot  %q\nwant %q", got, report)
		}
	}
}

// TestTraceReportGuardClampsLongLines covers the module-heavy case, where a
// single verbatim `schema(json: "…")` argument or `.contents: JSON!` result
// dwarfs the rest of the report.
func TestTraceReportGuardClampsLongLines(t *testing.T) {
	long := "schema(json: \"" + strings.Repeat("x", 5000) + "\")"
	report := "head\n" + long + "\ntail\n"

	got := guardTraceReport(report)
	lines := strings.Split(got, "\n")
	if len(lines) != 4 {
		t.Fatalf("expected line boundaries preserved, got %d lines: %q", len(lines), got)
	}
	if lines[0] != "head" || lines[2] != "tail" {
		t.Fatalf("expected surrounding lines untouched, got %q", got)
	}
	clamped := lines[1]
	if !strings.HasPrefix(clamped, long[:traceReportMaxLineLen]) {
		t.Fatalf("expected clamped line to keep its first %d bytes, got %q", traceReportMaxLineLen, clamped)
	}
	if !strings.Contains(clamped, "bytes truncated]") {
		t.Fatalf("expected an inline truncation marker, got %q", clamped)
	}
	if len(clamped) >= len(long) {
		t.Fatalf("expected clamped line to be shorter than the original (%d >= %d)", len(clamped), len(long))
	}
}

// TestTraceReportGuardClampsOnRuneBoundary makes sure the clamp never splits a
// multi-byte rune -- the report is full of box-drawing characters.
func TestTraceReportGuardClampsOnRuneBoundary(t *testing.T) {
	// "┃" is 3 bytes, so a run of them straddles the 2000-byte clamp.
	line := strings.Repeat("┃", traceReportMaxLineLen)
	got := clampLineBytes(line, traceReportMaxLineLen)
	// Everything before the marker must be valid UTF-8 and within the clamp.
	idx := strings.Index(got, "[... ")
	if idx < 0 {
		t.Fatalf("expected a truncation marker, got %q", got[:min(80, len(got))])
	}
	kept := got[:idx]
	if !utf8.ValidString(kept) {
		t.Fatalf("clamp split a rune: kept portion is not valid UTF-8")
	}
	if len(kept) > traceReportMaxLineLen {
		t.Fatalf("kept %d bytes, over the %d-byte clamp", len(kept), traceReportMaxLineLen)
	}
	if len(kept) < traceReportMaxLineLen-utf8.UTFMax {
		t.Fatalf("clamp gave up too much: kept %d of %d bytes", len(kept), traceReportMaxLineLen)
	}
}

// TestTraceReportGuardTruncatesMiddle covers the total-budget blowout: the
// span tree at the head and the summary sections at the tail both carry
// signal, so the middle is what goes.
func TestTraceReportGuardTruncatesMiddle(t *testing.T) {
	var b strings.Builder
	b.WriteString("◼ FIRST LINE OF THE SPAN TREE\n")
	for i := range 5000 {
		fmt.Fprintf(&b, "◼ span number %d doing some work\n", i)
	}
	b.WriteString("CHECKS: 42 passed\n")
	b.WriteString("RUN LOCALLY: dagger check\n")
	report := b.String()

	got := guardTraceReport(report)
	if len(got) > traceReportMaxBytes {
		t.Fatalf("guarded report is %d bytes, over the %d-byte budget", len(got), traceReportMaxBytes)
	}
	if !strings.HasPrefix(got, "◼ FIRST LINE OF THE SPAN TREE\n") {
		t.Fatalf("expected the head to be kept, got %q", got[:min(80, len(got))])
	}
	if !strings.HasSuffix(got, "CHECKS: 42 passed\nRUN LOCALLY: dagger check\n") {
		t.Fatalf("expected the tail to be kept, got %q", got[max(0, len(got)-80):])
	}
	if !strings.Contains(got, "omitted from the middle of this report") {
		t.Fatalf("expected a truncation marker, got %q", got[:min(200, len(got))])
	}

	// Line boundaries survive: every kept line is a whole line of the input.
	inputLines := map[string]bool{}
	for _, line := range strings.Split(report, "\n") {
		inputLines[line] = true
	}
	var markers int
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, "omitted from the middle") {
			markers++
			continue
		}
		if !inputLines[line] {
			t.Fatalf("kept line is not a whole input line: %q", line)
		}
	}
	if markers != 1 {
		t.Fatalf("expected exactly one truncation marker, got %d", markers)
	}

	// Both halves are generous: neither end is a token gesture.
	head, _, ok := strings.Cut(got, "... ")
	if !ok {
		t.Fatal("expected to find the marker")
	}
	if len(head) < traceReportMaxBytes/2 {
		t.Fatalf("head is only %d bytes of a %d-byte budget", len(head), traceReportMaxBytes)
	}
}

// TestExpandedSpansUnwrapsToFirstRealWork covers the narrow unwrap a scoped
// tool-call report needs. Force-expanding the WHOLE subtree (what this used to
// do) punches open every roll-up boundary in it -- module-internal glob
// matching, long CACHED call chains with verbatim arguments -- which measured
// 713 lines over the 16 KiB report budget for a single check.
//
// What actually has to be forced open is the path from the tool-call span
// (LLMTool + roll-ups, which TraceTree.IsExpanded would otherwise collapse to a
// bare status line) down to the tool's own work. Everything below that is left
// to the normal rules.
func TestExpandedSpansUnwrapsToFirstRealWork(t *testing.T) {
	const (
		toolID byte = iota + 1
		queryID
		profID
		workID
		nestedID
		deepID
	)
	start := time.Unix(100, 0)
	snap := func(id byte, name string, parent byte) dagui.SpanSnapshot {
		s := dagui.SpanSnapshot{
			ID:        traceTargetSpanID(id),
			TraceID:   dagui.TraceID{TraceID: trace.TraceID{1}},
			Name:      name,
			StartTime: start,
			EndTime:   start.Add(time.Second),
			Final:     true,
		}
		if parent != 0 {
			s.ParentID = traceTargetSpanID(parent)
		}
		return s
	}

	tool := snap(toolID, "check", 0)
	tool.LLMTool = "check"
	tool.Boundary = true
	tool.RollUpLogs = true
	tool.RollUpSpans = true

	// A pure API frame, then the module-function profiling twin (no logs, one
	// child): both are wrappers on the way to the work.
	query := snap(queryID, "POST /query", toolID)
	prof := snap(profID, "dagger-dev:Workspace.check", queryID)

	// The tool's own work: it rolls up its logs, which is exactly the boundary
	// the unwrap must punch through so its printed output survives.
	work := snap(workID, "Workspace.check", profID)
	work.RollUpLogs = true

	// Nested work below it: another roll-up, which must stay closed.
	nested := snap(nestedID, "Container.withExec", workID)
	nested.RollUpSpans = true
	deep := snap(deepID, "Directory.glob", nestedID)

	db := dagui.NewDB()
	db.ImportSnapshots([]dagui.SpanSnapshot{tool, query, prof, work, nested, deep})

	got := expandedSpans(db, traceTargetSpanID(toolID))
	for _, want := range []byte{toolID, queryID, profID, workID} {
		if !got[traceTargetSpanID(want)] {
			t.Errorf("span %d should be force-expanded, got %v", want, got)
		}
	}
	for _, unwanted := range []byte{nestedID, deepID} {
		if got[traceTargetSpanID(unwanted)] {
			t.Errorf("span %d must be left to the normal expansion rules, got %v", unwanted, got)
		}
	}

	// Without a scope there is no tool-call boundary to punch through.
	if unscoped := expandedSpans(db, dagui.SpanID{}); len(unscoped) != 0 {
		t.Errorf("an unscoped render must not force anything open, got %v", unscoped)
	}
}

// TestToolCallReportOptsHideTreeButReadTraceKeepsIt pins the one difference
// between the two LLM-facing report shapes: a tool result carries only what
// its call surfaced, while ReadTrace -- whose whole purpose is showing the
// shape of what ran -- keeps the span tree.
func TestToolCallReportOptsHideTreeButReadTraceKeepsIt(t *testing.T) {
	if !toolCallReportOpts().HideSpanTree {
		t.Error("a tool call's own report must not render the span tree")
	}
	for _, target := range []traceTarget{
		{Span: "cafef00d"},
		{Check: "ci:bootstrap"},
		{Test: "TestSomething"},
	} {
		if readTraceReportOpts(target).HideSpanTree {
			t.Errorf("ReadTrace(%+v) must keep the span tree", target)
		}
	}
}
