package idtui

import (
	"bytes"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/muesli/termenv"
	"github.com/stretchr/testify/require"
)

// vtermScreenCase writes a log the way the frontend does: the width and
// prefix are set when the log is created, the bytes stream in, the terminal
// may be resized, and only then does something show it.
type vtermScreenCase struct {
	name   string
	width  int
	prefix string
	writes []string
	// resizes, by the index of the write each comes before
	widths   map[int]int
	prefixes map[int]string
}

var vtermScreenCases = []vtermScreenCase{
	{
		name:   "lines",
		writes: []string{"one\n", "two\n", "thr", "ee\n", "four"},
	},
	{
		name:   "colors and carriage returns",
		width:  40,
		writes: []string{"\x1b[31mred\x1b[0m plain\n", "progress 10%\r", "progress 50%\r", "progress 100%\n", "\x1b[1;32mdone\x1b[0m\n"},
	},
	{
		name:   "long line wraps at width",
		width:  20,
		prefix: "│ ",
		writes: []string{strings.Repeat("abcdefghij", 12) + "\n", "tail\n"},
	},
	{
		name:   "one huge line",
		writes: []string{`{"schema":"` + strings.Repeat("x", 100_000) + `"}`},
	},
	{
		// midterm doesn't reflow rows already written, so a resize keeps
		// the earlier layout and the carriage return lands on the second row
		name:   "resized after writing",
		width:  8,
		writes: []string{"abcdefgh12345678\r", "OK"},
		widths: map[int]int{1: 16},
	},
	{
		name:     "prefix and width change between writes",
		width:    30,
		writes:   []string{strings.Repeat("0123456789", 5) + "\n", "second\r", strings.Repeat("z", 40) + "\n", "end"},
		prefixes: map[int]string{1: "│ │ "},
		widths:   map[int]int{2: 12, 3: 50},
	},
	{
		name:     "prefix before any width",
		writes:   []string{"wide " + strings.Repeat("y", 60) + "\n", "x"},
		prefixes: map[int]string{1: "> "},
		widths:   map[int]int{1: 20},
	},
}

// vtermScreenState is everything a caller can read from a log.
type vtermScreenState struct {
	UsedHeight int
	View       string
	Print      string
	Raw        string
	Tail       []string
	TailTotal  int
	LastLine   string
	Offset     int
}

func readVtermScreen(t *testing.T, term *Vterm, height int) vtermScreenState {
	t.Helper()
	var st vtermScreenState
	st.UsedHeight = term.UsedHeight()
	term.SetHeight(height)
	st.Offset = term.Offset
	st.View = term.View()
	var print, raw bytes.Buffer
	require.NoError(t, term.Print(&print))
	require.NoError(t, term.PrintRaw(&raw))
	st.Print, st.Raw = print.String(), raw.String()
	st.Tail, st.TailTotal = term.PrintTail(2)
	st.LastLine = term.LastLine()
	return st
}

// TestVtermScreenBuiltOnDemand checks that a log whose screen is first built
// when something reads it shows exactly what it would have shown had every
// write gone to a screen as it arrived.
func TestVtermScreenBuiltOnDemand(t *testing.T) {
	for _, tc := range vtermScreenCases {
		t.Run(tc.name, func(t *testing.T) {
			newTerm := func() *Vterm {
				term := NewVterm(termenv.ANSI)
				if tc.width > 0 {
					term.SetWidth(tc.width)
				}
				term.SetPrefix(tc.prefix)
				return term
			}
			eager := newTerm()
			eager.UsedHeight() // builds the screen before any write
			require.NotNil(t, eager.vt)

			lazy := newTerm()
			for i, w := range tc.writes {
				for _, term := range []*Vterm{eager, lazy} {
					if prefix, ok := tc.prefixes[i]; ok {
						term.SetPrefix(prefix)
					}
					if width, ok := tc.widths[i]; ok {
						term.SetWidth(width)
					}
					_, err := term.Write([]byte(w))
					require.NoError(t, err)
				}
			}
			require.Nil(t, lazy.vt, "writing alone builds no screen")

			for _, height := range []int{1, 3, 1000} {
				require.Equal(t, readVtermScreen(t, eager, height), readVtermScreen(t, lazy, height), "height %d", height)
			}

			// Once built, the screen follows new writes like any other.
			for _, term := range []*Vterm{eager, lazy} {
				_, err := term.Write([]byte("\nmore\n"))
				require.NoError(t, err)
			}
			require.Equal(t, readVtermScreen(t, eager, 3), readVtermScreen(t, lazy, 3))
		})
	}
}

// TestVtermScreenBeforeMarkdown checks that a log of plain writes followed by
// Markdown still shows both: the Markdown never enters the screen, so the
// screen is built from the plain writes before the Markdown arrives.
func TestVtermScreenBeforeMarkdown(t *testing.T) {
	term := NewVterm(termenv.Ascii)
	term.SetWidth(40)
	_, err := term.Write([]byte("plain\n"))
	require.NoError(t, err)
	_, err = term.WriteMarkdown([]byte("**bold**"))
	require.NoError(t, err)
	term.SetHeight(term.UsedHeight())
	view := term.View()
	require.Contains(t, view, "plain")
	require.Contains(t, view, "bold")
	require.NotContains(t, view, "**bold**")
	require.Equal(t, 1, strings.Count(view, "plain"))
}

// BenchmarkVtermFootprint reports the heap a log retains per byte written to
// it, for logs shaped like a long test run's: most are short exec output, and
// some are a call's whole result on one line. "unshown" is the usual case: a
// run streams far more logs than anyone opens. "shown" renders every log once.
//
//	go test ./dagql/idtui -run '^$' -bench VtermFootprint -benchtime 1x
func BenchmarkVtermFootprint(b *testing.B) {
	const logs = 2000
	write := func(term *Vterm, i int) int {
		var n int
		if i%10 == 0 {
			// A call's result, logged on one line.
			line := fmt.Sprintf(`{"data":{"contents":%q}}`, strings.Repeat("x", 50_000+i))
			n, _ = term.Write([]byte(line))
			return n
		}
		for l := range 200 {
			m, _ := fmt.Fprintf(term, "\x1b[2m%04d\x1b[0m === RUN   TestSomething/case_%d_%d some output here\n", l, i, l)
			n += m
		}
		return n
	}
	for _, shown := range []bool{false, true} {
		name := "unshown"
		if shown {
			name = "shown"
		}
		b.Run(name, func(b *testing.B) {
			var perByte float64
			for b.Loop() {
				runtime.GC()
				var before, after runtime.MemStats
				runtime.ReadMemStats(&before)
				terms := make([]*Vterm, logs)
				var written int
				for i := range terms {
					terms[i] = NewVterm(termenv.ANSI)
					written += write(terms[i], i)
				}
				if shown {
					for _, term := range terms {
						term.SetHeight(10)
						_ = term.View()
					}
				}
				runtime.GC()
				runtime.ReadMemStats(&after)
				runtime.KeepAlive(terms)
				perByte = float64(int64(after.HeapAlloc)-int64(before.HeapAlloc)) / float64(written)
			}
			b.ReportMetric(perByte, "B/logbyte")
		})
	}
}
