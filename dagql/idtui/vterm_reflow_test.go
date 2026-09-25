package idtui

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/require"
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

func reflowViewLines(term *Vterm) []string {
	plain := strings.TrimSuffix(ansi.Strip(term.View()), "\n")
	lines := strings.Split(plain, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	return lines
}
