package core

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
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
