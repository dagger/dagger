package idtui

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func TestLineLevelDiff(t *testing.T) {
	old := `func hello() {
	fmt.Println("hello")
}`
	new := `func hello() {
	fmt.Println("hello world")
	fmt.Println("goodbye")
}`

	lines := lineLevelDiff(old, new)

	// Verify we get context, removed, and added lines.
	var ctx, rem, add int
	for _, l := range lines {
		switch l.Kind {
		case diffContext:
			ctx++
		case diffRemoved:
			rem++
		case diffAdded:
			add++
		}
	}

	if ctx == 0 {
		t.Error("expected context lines")
	}
	if rem == 0 {
		t.Error("expected removed lines")
	}
	if add == 0 {
		t.Error("expected added lines")
	}

	// The first line "func hello() {" should be context.
	if lines[0].Kind != diffContext {
		t.Errorf("expected first line to be context, got %v", lines[0].Kind)
	}
	if lines[0].Content != "func hello() {" {
		t.Errorf("unexpected first line content: %q", lines[0].Content)
	}
}

func TestPairForIntraline(t *testing.T) {
	lines := []diffLine{
		{OldNo: 1, NewNo: 1, Kind: diffContext, Content: "a"},
		{OldNo: 2, Kind: diffRemoved, Content: "old1"},
		{OldNo: 3, Kind: diffRemoved, Content: "old2"},
		{NewNo: 2, Kind: diffAdded, Content: "new1"},
		{NewNo: 3, Kind: diffAdded, Content: "new2"},
		{NewNo: 4, Kind: diffAdded, Content: "new3"},
		{OldNo: 4, NewNo: 5, Kind: diffContext, Content: "b"},
	}

	pairs := pairForIntraline(lines)

	// Context lines should have no pair.
	if pairs[0] != nil {
		t.Error("context line 0 should have nil pair")
	}
	if pairs[6] != nil {
		t.Error("context line 6 should have nil pair")
	}

	// removed[0] paired with added[0], removed[1] paired with added[1]
	if pairs[1] == nil || pairs[1].Content != "new1" {
		t.Error("removed line 1 should pair with added new1")
	}
	if pairs[2] == nil || pairs[2].Content != "new2" {
		t.Error("removed line 2 should pair with added new2")
	}
	if pairs[3] == nil || pairs[3].Content != "old1" {
		t.Error("added line 3 should pair with removed old1")
	}
	if pairs[4] == nil || pairs[4].Content != "old2" {
		t.Error("added line 4 should pair with removed old2")
	}

	// Third added line has no removed partner.
	if pairs[5] != nil {
		t.Error("added line 5 (new3) should have nil pair (no removed partner)")
	}
}

func TestRenderEditDiffWidth(t *testing.T) {
	old := `fmt.Println("hello")`
	new := `fmt.Println("hello world")`

	result := renderEditDiff(0, "test.go", old, new, 80)

	for i, line := range splitLines(result) {
		if line == "" {
			continue
		}
		w := ansi.StringWidth(line)
		if w > 80 {
			t.Errorf("line %d: visible width %d > 80: %q", i, w, line)
		}
	}
}

func TestRenderEditDiffTabWidth(t *testing.T) {
	// Simulate real Go source code with tab indentation, similar to what
	// an LLM edit tool call would produce.
	old := "\t\t\treturn ToValue(centered)\n\t\t})\n"
	new := "\t\t\treturn ToValue(centered)\n\t\t})\n\n" +
		"\t\t// String.reverse method: reverse -> String!\n" +
		"\t\tMethod(StringType, \"reverse\").\n" +
		"\t\t\tDoc(\"reverses the characters in the string\").\n" +
		"\t\t\tReturns(NonNull(StringType)).\n" +
		"\t\t\tImpl(func(ctx context.Context, self Value, args Args) (Value, error) {\n" +
		"\t\t\t\trunes := []rune(self.(StringValue).Val)\n" +
		"\t\t\t\tfor i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {\n" +
		"\t\t\t\t\trunes[i], runes[j] = runes[j], runes[i]\n" +
		"\t\t\t\t}\n" +
		"\t\t\t\treturn ToValue(string(runes))\n" +
		"\t\t\t})\n"

	for _, totalWidth := range []int{80, 120, 160, 200} {
		t.Run(fmt.Sprintf("width=%d", totalWidth), func(t *testing.T) {
			result := renderEditDiff(0, "test.go", old, new, totalWidth)
			for i, line := range splitLines(result) {
				if line == "" {
					continue
				}
				rendered := terminalRenderWidth(line)
				if rendered > totalWidth {
					t.Errorf("line %d: rendered width %d > %d (ansi.StringWidth=%d): %q",
						i, rendered, totalWidth, ansi.StringWidth(line), line)
				}
			}
		})
	}
}

func TestRenderEditDiffUnifiedFormat(t *testing.T) {
	old := "hello world\n"
	new := "hello there\ngoodbye\n"

	result := renderEditDiff(0, "test.txt", old, new, 80)

	// Should contain removed and added markers.
	if !strings.Contains(result, "- ") {
		t.Error("expected removed line marker '- '")
	}
	if !strings.Contains(result, "+ ") {
		t.Error("expected added line marker '+ '")
	}

	// Should be unified (single column), not side-by-side.
	// Each non-empty line should start with a line number gutter.
	for i, line := range splitLines(result) {
		if line == "" {
			continue
		}
		// Should have the gutter pattern: digits/spaces in first 10 chars.
		if len(line) < 12 {
			t.Errorf("line %d too short: %q", i, line)
		}
	}
}

// TestRenderUnifiedLineWideGutter guards against the gutter overflowing the
// terminal width once line numbers reach 5+ digits: the gutter width must be
// derived from the actual field width (numW), not a hard-coded constant, and
// content must be truncated to what's left over.
func TestRenderUnifiedLineWideGutter(t *testing.T) {
	out := NewOutput(new(strings.Builder), termenv.WithProfile(termenv.Ascii))

	// A 5-digit line number widens the gutter past the old hard-coded 12.
	dl := diffLine{
		OldNo:   12345,
		NewNo:   12345,
		Kind:    diffContext,
		Content: strings.Repeat("x", 200),
	}
	numW := len(strconv.Itoa(dl.OldNo)) // 5

	for _, width := range []int{20, 40, 80} {
		line := renderUnifiedLine(out, dl, nil, numW, width)
		if w := ansi.StringWidth(line); w > width {
			t.Errorf("width=%d: rendered width %d > %d: %q", width, w, width, line)
		}
		// The full line-number gutter must survive (never truncated away).
		if !strings.Contains(line, "12345 12345") {
			t.Errorf("width=%d: 5-digit gutter missing/truncated: %q", width, line)
		}
	}
}

// TestAddFileContextTrailingNewline verifies the after-context isn't shifted
// down by one (silently skipping a line) when oldText ends in a newline.
func TestAddFileContextTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	// Lines a..g, whole file terminated by a newline.
	if err := os.WriteFile(file, []byte("a\nb\nc\nd\ne\nf\ng\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// oldText covers lines c,d and ends with a trailing newline.
	oldText := "c\nd\n"
	lines := []diffLine{
		{OldNo: 1, NewNo: 1, Kind: diffContext, Content: "c"},
		{OldNo: 2, Kind: diffRemoved, Content: "d"},
		{NewNo: 2, Kind: diffAdded, Content: "D"},
	}

	got := addFileContext(file, oldText, lines)

	// The line immediately after the edit ("e") must appear as after-context,
	// not be skipped. It should be numbered 5 in both columns.
	var foundE bool
	for _, dl := range got {
		if dl.Kind == diffContext && dl.Content == "e" {
			foundE = true
			if dl.OldNo != 5 || dl.NewNo != 5 {
				t.Errorf("line 'e' has wrong numbers: OldNo=%d NewNo=%d (want 5/5)", dl.OldNo, dl.NewNo)
			}
		}
	}
	if !foundE {
		var contents []string
		for _, dl := range got {
			contents = append(contents, dl.Content)
		}
		t.Errorf("after-context skipped line 'e'; got lines: %q", contents)
	}
}

// terminalRenderWidth computes the actual visual width of a string as a
// terminal would render it, expanding tabs to the next tab stop (every 8
// columns) and skipping ANSI escape sequences.
func terminalRenderWidth(s string) int {
	col := 0
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' {
			// Skip ANSI escape sequence.
			j := i + 1
			for j < len(s) && (s[j] < 'A' || s[j] > 'Z') && (s[j] < 'a' || s[j] > 'z') {
				j++
			}
			if j < len(s) {
				j++ // include final letter
			}
			i = j
			continue
		}
		if s[i] == '\t' {
			col += 8 - (col % 8) // advance to next tab stop
			i++
			continue
		}
		// Decode UTF-8 rune and count as 1 column of width.
		_, size := utf8.DecodeRuneInString(s[i:])
		col++
		i += size
	}
	return col
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return split(s, '\n')
}

func split(s string, sep byte) []string {
	var parts []string
	for {
		i := indexByte(s, sep)
		if i < 0 {
			parts = append(parts, s)
			break
		}
		parts = append(parts, s[:i])
		s = s[i+1:]
	}
	return parts
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}
