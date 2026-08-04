package core

import (
	"strings"
	"testing"
)

// TestAssembleLines covers log-line assembly from raw stdio segments: log
// records aren't guaranteed to be line-aligned, so a line that straddles two
// records takes the provenance of the record that started it.
func TestAssembleLines(t *testing.T) {
	for _, tc := range []struct {
		name     string
		segments []capturedLine
		want     []capturedLine
	}{
		{
			name: "whole lines keep their provenance",
			segments: []capturedLine{
				{text: "nested one\nnested two\n", direct: false},
				{text: "printed\n", direct: true},
			},
			want: []capturedLine{
				{text: "nested one", direct: false},
				{text: "nested two", direct: false},
				{text: "printed", direct: true},
			},
		},
		{
			name: "line split across records is assembled once",
			segments: []capturedLine{
				{text: "hello, ", direct: true},
				{text: "world\n", direct: true},
			},
			want: []capturedLine{{text: "hello, world", direct: true}},
		},
		{
			name: "straddling line takes the starting record's provenance",
			segments: []capturedLine{
				{text: "start", direct: true},
				{text: "-end\nnested\n", direct: false},
			},
			want: []capturedLine{
				{text: "start-end", direct: true},
				{text: "nested", direct: false},
			},
		},
		{
			name:     "trailing newlines don't contribute lines",
			segments: []capturedLine{{text: "only\n\n\n", direct: true}},
			want:     []capturedLine{{text: "only", direct: true}},
		},
		{
			name:     "unterminated final line is kept",
			segments: []capturedLine{{text: "no trailing newline", direct: false}},
			want:     []capturedLine{{text: "no trailing newline", direct: false}},
		},
		{
			name:     "empty input",
			segments: nil,
			want:     nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := assembleLines(tc.segments)
			if len(got) != len(tc.want) {
				t.Fatalf("assembleLines() = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("line %d = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestLimitIndirectLines locks in the tool-result abridging rule: whatever the
// tool printed itself survives in full — a sub-agent's report is the point of
// the call — while logs from nested work beneath it are cut down to a tail,
// with the dropped runs counted so the model knows to reach for ReadLogs.
func TestLimitIndirectLines(t *testing.T) {
	direct := func(texts ...string) []capturedLine {
		lines := make([]capturedLine, len(texts))
		for i, text := range texts {
			lines[i] = capturedLine{text: text, direct: true}
		}
		return lines
	}
	nested := func(texts ...string) []capturedLine {
		lines := make([]capturedLine, len(texts))
		for i, text := range texts {
			lines[i] = capturedLine{text: text}
		}
		return lines
	}

	t.Run("a long report survives noisy nested work", func(t *testing.T) {
		// The shape that motivated this: a sub-agent runs a build (lots of
		// nested output), then the tool prints its report. Tail-only
		// truncation evicted the report; now only the build output is cut.
		var lines []capturedLine
		for i := 1; i <= 50; i++ {
			lines = append(lines, capturedLine{text: "NESTED-" + itoa(i)})
		}
		for i := 1; i <= 12; i++ {
			lines = append(lines, capturedLine{text: "REPORT-" + itoa(i), direct: true})
		}

		got := limitIndirectLines("abc123", lines, 3, 1000)
		want := []string{
			"... 47 lines omitted (use ReadLogs(span: abc123) to read more) ...",
			"NESTED-48", "NESTED-49", "NESTED-50",
		}
		for i := 1; i <= 12; i++ {
			want = append(want, "REPORT-"+itoa(i))
		}
		requireLines(t, got, want)
	})

	t.Run("direct lines are never dropped even past the limit", func(t *testing.T) {
		got := limitIndirectLines("s", direct("a", "b", "c", "d", "e"), 2, 1000)
		requireLines(t, got, []string{"a", "b", "c", "d", "e"})
	})

	t.Run("interleaved runs each report their own count", func(t *testing.T) {
		var lines []capturedLine
		lines = append(lines, nested("n1", "n2", "n3")...)
		lines = append(lines, direct("report A")...)
		lines = append(lines, nested("n4", "n5")...)
		lines = append(lines, direct("report B")...)

		// limit 1 keeps only the last indirect line (n5).
		got := limitIndirectLines("s", lines, 1, 1000)
		requireLines(t, got, []string{
			"... 3 lines omitted (use ReadLogs(span: s) to read more) ...",
			"report A",
			"... 1 lines omitted (use ReadLogs(span: s) to read more) ...",
			"n5",
			"report B",
		})
	})

	t.Run("nothing is abridged when nested output fits", func(t *testing.T) {
		lines := append(nested("n1", "n2"), direct("done")...)
		got := limitIndirectLines("s", lines, 8, 1000)
		requireLines(t, got, []string{"n1", "n2", "done"})
	})

	t.Run("zero limit keeps everything", func(t *testing.T) {
		lines := append(nested("n1", "n2", "n3"), direct("done")...)
		got := limitIndirectLines("s", lines, 0, 1000)
		requireLines(t, got, []string{"n1", "n2", "n3", "done"})
	})

	t.Run("long lines are still character-capped", func(t *testing.T) {
		long := strings.Repeat("x", 20)
		got := limitIndirectLines("s", direct(long), 8, 10)
		requireLines(t, got, []string{strings.Repeat("x", 10) + "[... 10 chars truncated]"})
	})
}

func requireLines(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d lines:\n%s\nwant %d lines:\n%s",
			len(got), strings.Join(got, "\n"), len(want), strings.Join(want, "\n"))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// itoa formats small positive ints with a leading zero, so test fixtures sort
// and read like the log lines they stand in for.
func itoa(i int) string {
	if i < 10 {
		return "0" + string(rune('0'+i))
	}
	return string(rune('0'+i/10)) + string(rune('0'+i%10))
}
