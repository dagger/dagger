package core

import (
	"fmt"
	"strings"
	"testing"
)

// TestGuardText covers the shared bounding helper behind guardTraceReport and
// guardToolResult: a per-line byte clamp on a rune boundary, then a total
// budget enforced by dropping whole lines out of the MIDDLE behind the
// guard's own marker.
func TestGuardText(t *testing.T) {
	guard := textGuard{
		maxBytes:   400,
		maxLineLen: 10,
		headBytes:  240,
		marker: func(lines, bytes int) string {
			return fmt.Sprintf("<%d lines, %d bytes dropped>", lines, bytes)
		},
	}

	// 60 lines of exactly 10 bytes: 659 bytes joined, well over the 400-byte
	// budget.
	var manyLines []string
	for i := 1; i <= 60; i++ {
		manyLines = append(manyLines, fmt.Sprintf("line-%02dxxx", i))
	}
	overBudget := strings.Join(manyLines, "\n")

	for _, tc := range []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "empty input",
			input: "",
			want:  "",
		},
		{
			name:  "within both limits passes through byte-identical",
			input: "one\ntwo\nthree\n",
			want:  "one\ntwo\nthree\n",
		},
		{
			name:  "a long line is clamped inline",
			input: "head\n" + strings.Repeat("x", 30) + "\ntail",
			want:  "head\n" + strings.Repeat("x", 10) + "[... 20 bytes truncated]\ntail",
		},
		{
			name:  "the clamp cuts on a rune boundary",
			input: strings.Repeat("┃", 5), // 3 bytes each, so 10 lands mid-rune
			want:  "┃┃┃[... 6 bytes truncated]",
		},
		{
			name: "over budget drops the middle behind the guard's marker",
			// head budget 240 keeps 21 lines (231 bytes), the 128-byte marker
			// reserve leaves 41 for the tail, which keeps 3 (33 bytes), so 36
			// lines (395 bytes) go.
			input: overBudget,
			want: strings.Join(append(append([]string{}, manyLines[:21]...),
				"<36 lines, 395 bytes dropped>",
				manyLines[57], manyLines[58], manyLines[59]), "\n"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := guardText(tc.input, guard)
			if got != tc.want {
				t.Fatalf("guardText() =\n%q\nwant\n%q", got, tc.want)
			}
		})
	}
}
