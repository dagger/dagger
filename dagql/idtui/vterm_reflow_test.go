package idtui

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/require"
	"github.com/vito/midterm"
)

// Width belongs to the viewer, not the producer. These assertions deliberately
// do not preserve the old terminal's resize history or physical-row edits.
func TestVtermReflowsExistingOutput(t *testing.T) {
	term := NewVterm(termenv.Ascii)
	t.Cleanup(term.Close)
	term.SetWidth(6)
	_, err := io.WriteString(term, "abcdefghijklmnop")
	require.NoError(t, err)
	term.SetHeight(10)
	require.Equal(t, []string{"abcdef", "ghijkl", "mnop"}, reflowViewLines(term))
	stored := term.terminalBuf.Len()
	term.SetWidth(20)
	require.Equal(t, []string{"abcdefghijklmnop"}, reflowViewLines(term))
	term.SetWidth(4)
	require.Equal(t, []string{"abcd", "efgh", "ijkl", "mnop"}, reflowViewLines(term))
	term.SetPrefix("> ")
	require.Equal(t, []string{"> ab", "> cd", "> ef", "> gh", "> ij", "> kl", "> mn", "> op"}, reflowViewLines(term))
	require.Equal(t, stored, term.terminalBuf.Len(), "resizing must not write to the content spool")
}

func TestVtermProgressEditsBeforeWrapping(t *testing.T) {
	for _, update := range []string{
		"\r\x1b[2Kdone",      // erase and replace a long progress line
		"\x1b[10Ddone\x1b[K", // horizontal cursor back to its logical start
		"\x1b[1Gdone\x1b[K",  // absolute column, independent of soft wraps
	} {
		t.Run(update, func(t *testing.T) {
			term := NewVterm(termenv.Ascii)
			t.Cleanup(term.Close)
			term.SetWidth(4)
			term.SetHeight(10)
			_, err := io.WriteString(term, "abcdefghij")
			require.NoError(t, err)
			require.Equal(t, []string{"abcd", "efgh", "ij"}, reflowViewLines(term))
			_, err = io.WriteString(term, update)
			require.NoError(t, err)
			require.Equal(t, []string{"done"}, reflowViewLines(term), "progress updates target the logical line, not its last wrapped row")
			term.SetWidth(20)
			require.Equal(t, []string{"done"}, reflowViewLines(term))
		})
	}
}

func TestVtermOutputIndependentOfResizeHistory(t *testing.T) {
	chunks := []string{"\x1b[3", "1mabcdefghij", "\r\x1b[2K", "done\x1b[0m\r\n", "a second long line"}
	makeTerm := func(resize bool) *Vterm {
		term := NewVterm(termenv.ANSI)
		t.Cleanup(term.Close)
		term.SetWidth(80)
		term.SetHeight(20)
		for i, chunk := range chunks {
			_, err := io.WriteString(term, chunk)
			require.NoError(t, err)
			if resize {
				term.SetWidth(3 + i)
				_ = term.View()
				term.mu.Lock()
				term.evictLocked()
				term.mu.Unlock()
			}
		}
		term.SetWidth(8)
		return term
	}
	fresh, resized := makeTerm(false), makeTerm(true)
	require.Equal(t, reflowViewLines(fresh), reflowViewLines(resized))
	require.Contains(t, resized.View(), "\x1b[31m", "colors survive logical-line interpretation and wrapping")
	var raw bytes.Buffer
	require.NoError(t, resized.PrintRaw(&raw))
	require.Equal(t, strings.Join(chunks, ""), raw.String())
}

func TestVtermReflowUsesDisplayCellWidths(t *testing.T) {
	term := NewVterm(termenv.Ascii)
	t.Cleanup(term.Close)
	term.SetWidth(4)
	_, err := io.WriteString(term, "a界e\u0301Z")
	require.NoError(t, err)
	term.SetHeight(10)
	require.Equal(t, []string{"a界e\u0301", "Z"}, reflowViewLines(term))
	term.SetWidth(8)
	require.Equal(t, []string{"a界e\u0301Z"}, reflowViewLines(term))
}

func TestVtermReflowPreservesColoredSpaceBars(t *testing.T) {
	term := NewVterm(termenv.ANSI)
	t.Cleanup(term.Close)
	term.SetWidth(4)
	_, err := io.WriteString(term, "\x1b[41m        \x1b[0m")
	require.NoError(t, err)
	term.SetHeight(10)
	require.Equal(t, 2, term.UsedHeight(), "background-colored spaces are visible log content")
	for _, line := range strings.Split(strings.TrimSuffix(term.View(), "\n"), "\n") {
		require.Contains(t, line, "\x1b[41m")
		require.Equal(t, "    ", ansi.Strip(line))
	}
}

func TestVtermReflowKeepsFollowingTail(t *testing.T) {
	term := NewVterm(termenv.Ascii)
	t.Cleanup(term.Close)
	term.SetWidth(4)
	_, err := io.WriteString(term, "abcdefghijklmnop")
	require.NoError(t, err)
	term.SetHeight(2)
	require.Equal(t, []string{"ijkl", "mnop"}, reflowViewLines(term))
	term.SetWidth(20)
	require.Equal(t, []string{"abcdefghijklmnop"}, reflowViewLines(term))
	_, err = io.WriteString(term, "qrstuvwx")
	require.NoError(t, err)
	term.SetWidth(4)
	require.Equal(t, []string{"qrst", "uvwx"}, reflowViewLines(term))
}

func TestVtermSearchCrossesSoftWrap(t *testing.T) {
	term := NewVterm(termenv.ANSI)
	t.Cleanup(term.Close)
	_, err := io.WriteString(term, "0123456789NEEDLE")
	require.NoError(t, err)
	term.SetWidth(4)
	term.SetHeight(4)
	count, row := term.Search("NEEDLE", 0)
	require.Equal(t, 1, count)
	require.Equal(t, 2, row, "search addresses the visual row where the match begins")
	term.SetWidth(20)
	count, row = term.Search("NEEDLE", 0)
	require.Equal(t, 1, count)
	require.Zero(t, row)
	term.mu.Lock()
	term.evictLocked()
	term.mu.Unlock()
	count, row = term.Search("NEEDLE", 0)
	require.Equal(t, 1, count)
	require.Zero(t, row)
}

func TestVtermLongLineLayoutIsIncremental(t *testing.T) {
	const width, height, chunk = 120, 20, 8 << 10
	text := strings.Repeat("x", 1<<20)
	term := NewVterm(termenv.Ascii)
	t.Cleanup(term.Close)
	term.SetWidth(width)
	term.SetHeight(height)
	frames := 0
	for i := 0; i < len(text); i += chunk {
		_, err := io.WriteString(term, text[i:i+chunk])
		require.NoError(t, err)
		tail := (i+chunk-1)%width + 1
		require.Equal(t, strings.Repeat("x", tail), reflowViewLines(term)[height-1])
		frames++
	}
	// Re-wrapping the whole line on every frame would be ~frames/2 times the
	// text. Only the appended tail, the unverified partial hash block and the
	// two wrapped rows before it may be wrapped again.
	require.LessOrEqual(t, term.wrapWork, len(text)+frames*(wrapHashBlock+2*width))
	require.Equal(t, (len(text)+width-1)/width, term.UsedHeight())
	term.ScrollToTop()
	lines := reflowViewLines(term)
	require.Len(t, lines, height)
	for _, line := range lines {
		require.Equal(t, strings.Repeat("x", width), line)
	}
}

// Incremental re-wrapping and per-slice rendering must match laying out the
// same stream from scratch, through edits anywhere on a multi-block line.
func TestVtermLongLineEditsMatchFreshLayout(t *testing.T) {
	inc, ref := NewVterm(termenv.ANSI), NewVterm(termenv.ANSI)
	t.Cleanup(inc.Close)
	t.Cleanup(ref.Close)
	for _, term := range []*Vterm{inc, ref} {
		term.SetWidth(37)
		term.SetHeight(1000)
	}
	render := func(term *Vterm) string {
		term.mu.Lock()
		defer term.mu.Unlock()
		term.materializeLocked()
		var buf bytes.Buffer
		term.renderLocked(&buf, 0, 1000)
		return buf.String()
	}
	for i, step := range []string{
		"\x1b[32m" + strings.Repeat("ab界", 1000),
		"\x1b[1;34m" + strings.Repeat("cd", 1250) + "\x1b[0m",
		"\x1b[2000DREPLACED in the middle",
		"\rstart",
		"\x1b[10000C" + strings.Repeat("y", 36) + "e\u0301",
		"\u0301\u0301" + strings.Repeat("z", 1500),
		"\x1b[41m    \x1b[0m",
		"\x1b[1500G\x1b[K" + "short",
		"\r\n" + strings.Repeat("next ", 600),
	} {
		_, err := io.WriteString(inc, step)
		require.NoError(t, err)
		_, err = io.WriteString(ref, step)
		require.NoError(t, err)
		got := render(inc)
		ref.mu.Lock()
		ref.evictLocked()
		ref.mu.Unlock()
		require.Equal(t, render(ref), got, "step %d", i)
		ref.mu.Lock()
		requireSameScreen(t, cutWholeLines(ref), got)
		ref.mu.Unlock()
		count, row := inc.Search("e", 3)
		refCount, refRow := ref.Search("e", 3)
		require.Equal(t, refCount, count)
		require.Equal(t, refRow, row)
		require.Equal(t, render(ref), render(inc), "highlighted step %d", i)
		ref.mu.Lock()
		requireSameScreen(t, cutWholeLines(ref), render(inc))
		ref.mu.Unlock()
		inc.Search("", -1)
		ref.Search("", -1)
	}
}

// A logical line without newlines (progress dots, minified JSON) grows by
// appends; live frames must lay out and render only what changed or is
// visible, not the whole line.
func BenchmarkVtermLongLine(b *testing.B) {
	const total, chunk = 1 << 20, 8 << 10
	for _, bench := range []struct {
		name string
		text string
	}{
		{"one-logical-line", strings.Repeat("x", total)},
		{"short-lines", strings.Repeat("0123456789abcd\r\n", total/16)},
	} {
		b.Run(bench.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				term := NewVterm(termenv.ANSI)
				term.SetWidth(120)
				term.SetHeight(20)
				for i := 0; i < len(bench.text); i += chunk {
					_, _ = io.WriteString(term, bench.text[i:min(i+chunk, len(bench.text))])
					_ = term.View()
				}
				for range 10 {
					term.ScrollBy(-20)
					_ = term.View()
				}
				term.Close()
			}
		})
	}
}

// cutWholeLines is the previous rendering: each wrapped row is an ANSI-aware
// cut of the whole rendered logical line, at the display columns of a wrap
// of the whole line.
func cutWholeLines(term *Vterm) string {
	var out strings.Builder
	width := max(1, term.Width-lipgloss.Width(term.Prefix))
	for row := range term.vt.UsedHeight() {
		var line strings.Builder
		term.vt.RenderLineFgBg(&line, row, nil, nil)
		start := 0
		for part := range strings.SplitSeq(ansi.Hardwrap(string(term.vt.Content[row][:textEnd(term.vt, row)]), width, true), "\n") {
			end := start + ansi.StringWidth(part)
			out.WriteString(term.Prefix + ansi.Cut(line.String(), start, end) + reset + "\n")
			start = end
		}
	}
	return out.String()
}

// requireSameScreen compares what two renderings display, cell by cell.
func requireSameScreen(t *testing.T, want, got string) {
	t.Helper()
	screen := func(s string) ([]string, [][]midterm.Format) {
		vt := midterm.NewAutoResizingTerminal()
		_, err := io.WriteString(vt, s)
		require.NoError(t, err)
		var lines []string
		var formats [][]midterm.Format
		for row := range vt.UsedHeight() {
			lines = append(lines, string(vt.Content[row]))
			var cells []midterm.Format
			for region := range vt.Format.Regions(row) {
				for range region.Size {
					cells = append(cells, region.F)
				}
			}
			formats = append(formats, cells)
		}
		return lines, formats
	}
	wantLines, wantFormats := screen(want)
	gotLines, gotFormats := screen(got)
	require.Equal(t, wantLines, gotLines)
	require.Equal(t, wantFormats, gotFormats)
}

func reflowViewLines(term *Vterm) []string {
	plain := strings.TrimSuffix(ansi.Strip(term.View()), "\n")
	lines := strings.Split(plain, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	return lines
}
