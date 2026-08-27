package core

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// textGuard parameterises guardText: the total byte budget, the per-line byte
// clamp, how much of the budget the head keeps when the middle has to go, and
// the marker line that stands in for what went.
//
// It exists because two readers need the same bounding with different numbers
// and different advice: a rendered trace report (guardTraceReport, in
// trace_report.go) and every LLM tool result (guardToolResult, in mcp.go).
type textGuard struct {
	maxBytes   int
	maxLineLen int
	headBytes  int
	// marker renders the elision marker, given the count of lines and bytes
	// dropped from the middle. It is a whole line of its own, and counts
	// against maxBytes like any other.
	marker func(lines, bytes int) string
}

// guardText bounds text to a guard's limits: every line is clamped to
// maxLineLen bytes (on a rune boundary, marked inline), and the whole text to
// maxBytes, dropping the MIDDLE on line boundaries rather than the tail so
// both ends survive -- the head and the conclusion of a report, the first and
// last lines of a file, both carry signal.
//
// Text already within both limits is returned byte-identical, without
// allocating.
func guardText(text string, g textGuard) string {
	if len(text) <= g.maxBytes && !anyLineTooLong(text, g.maxLineLen) {
		return text
	}

	lines := strings.Split(text, "\n")
	total := 0
	for i, line := range lines {
		lines[i] = clampLineBytes(line, g.maxLineLen)
		total += len(lines[i]) + 1 // +1 for the newline that rejoins it
	}
	if total > 0 {
		total-- // the last line isn't followed by a newline
	}
	if total <= g.maxBytes {
		return strings.Join(lines, "\n")
	}

	// Truncate on line boundaries, keeping a generous head and tail. The
	// marker line itself counts against the budget, so reserve room for it.
	const markerReserve = 128
	headBudget := g.headBytes

	head, headBytes := 0, 0
	for head < len(lines) && headBytes+len(lines[head])+1 <= headBudget {
		headBytes += len(lines[head]) + 1
		head++
	}
	tailBudget := g.maxBytes - headBytes - markerReserve
	tail, tailBytes := len(lines), 0
	for tail > head && tailBytes+len(lines[tail-1])+1 <= tailBudget {
		tailBytes += len(lines[tail-1]) + 1
		tail--
	}

	droppedLines := tail - head
	droppedBytes := total - headBytes - tailBytes
	if droppedLines <= 0 {
		return strings.Join(lines, "\n")
	}

	out := make([]string, 0, head+1+(len(lines)-tail))
	out = append(out, lines[:head]...)
	out = append(out, g.marker(droppedLines, droppedBytes))
	out = append(out, lines[tail:]...)
	return strings.Join(out, "\n")
}

// anyLineTooLong reports whether text contains a line over the per-line byte
// clamp, without allocating a split.
func anyLineTooLong(text string, maxLineLen int) bool {
	for len(text) > 0 {
		line := text
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			line, text = text[:i], text[i+1:]
		} else {
			text = ""
		}
		if len(line) > maxLineLen {
			return true
		}
	}
	return false
}

// clampLineBytes truncates line to at most max bytes, cutting on a UTF-8
// rune boundary so a multi-byte rune is never split, and marks the clamp
// inline.
func clampLineBytes(line string, max int) string {
	if len(line) <= max {
		return line
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(line[cut]) {
		cut--
	}
	return line[:cut] + fmt.Sprintf("[... %d bytes truncated]", len(line)-cut)
}
