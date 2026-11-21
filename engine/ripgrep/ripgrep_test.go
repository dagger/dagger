package ripgrep

import (
	"bufio"
	"fmt"
	"io"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// rgOutput is `rg --json` output for the given matches: path, line number
// and text of each, with one submatch per occurrence of sub.
func rgOutput(sub string, matches ...[3]string) string {
	var b strings.Builder
	b.WriteString(`{"type":"begin","data":{"path":{"text":"x"}}}` + "\n")
	offset := 0
	for _, m := range matches {
		path, line, text := m[0], m[1], m[2]
		fmt.Fprintf(&b, `{"type":"match","data":{"path":{"text":%q},"lines":{"text":%q},"line_number":%s,"absolute_offset":%d,"submatches":[`, path, text, line, offset)
		first := true
		for i := 0; sub != "" && i+len(sub) <= len(text); {
			j := strings.Index(text[i:], sub)
			if j < 0 {
				break
			}
			if !first {
				b.WriteString(",")
			}
			first = false
			fmt.Fprintf(&b, `{"match":{"text":%q},"start":%d,"end":%d}`, sub, i+j, i+j+len(sub))
			i += j + len(sub)
		}
		b.WriteString("]}}\n")
		offset += len(text)
	}
	b.WriteString(`{"type":"end","data":{"path":{"text":"x"},"binary_offset":null,"stats":{"elapsed":{"secs":0,"nanos":1,"human":"0s"},"searches":1}}}` + "\n")
	b.WriteString(`{"data":{"elapsed_total":{"human":"0s","nanos":1,"secs":0}},"type":"summary"}` + "\n")
	return b.String()
}

func parseAll(t *testing.T, out string, opts Options) ([]Result, bool, error) {
	t.Helper()
	var results []Result
	limitReached, err := ParseJSON(strings.NewReader(out), opts, func(r Result) error {
		results = append(results, r)
		return nil
	})
	return results, limitReached, err
}

func TestParseJSON(t *testing.T) {
	out := rgOutput("o",
		[3]string{"a.txt", "1", "hello world\n"},
		[3]string{"dir/b.txt", "7", "foo\n"},
	)
	results, limitReached, err := parseAll(t, out, Options{Limits: DefaultLimits()})
	require.NoError(t, err)
	require.False(t, limitReached)
	require.Equal(t, []Result{
		{Path: "a.txt", LineNumber: 1, Lines: "hello world\n", Submatches: []Submatch{
			{Text: "o", Start: 4, End: 5},
			{Text: "o", Start: 7, End: 8},
		}},
		{Path: "dir/b.txt", LineNumber: 7, AbsoluteOffset: 12, Lines: "foo\n", Submatches: []Submatch{
			{Text: "o", Start: 1, End: 2},
			{Text: "o", Start: 2, End: 3},
		}},
	}, results)
}

func TestParseJSONSkipsNonUTF8(t *testing.T) {
	out := `{"type":"match","data":{"path":{"bytes":"/w=="},"lines":{"text":"x\n"},"line_number":1,"absolute_offset":0,"submatches":[]}}
{"type":"match","data":{"path":{"text":"a"},"lines":{"bytes":"/w=="},"line_number":1,"absolute_offset":0,"submatches":[]}}
{"type":"match","data":{"path":{"text":"b"},"lines":{"text":"ok\n"},"line_number":2,"absolute_offset":0,"submatches":[]}}
`
	var skipped []string
	results, _, err := parseAll(t, out, Options{Limits: DefaultLimits(), OnSkip: func(reason string) {
		skipped = append(skipped, reason)
	}})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "b", results[0].Path)
	require.Len(t, skipped, 2)
}

func TestParseJSONLongLines(t *testing.T) {
	long := strings.Repeat("a", 100) + "é" + strings.Repeat("a", 100) + "\n"
	out := rgOutput("a", [3]string{"min.js", "1", long})
	limits := Limits{MaxLineBytes: 101, MaxSubmatches: 1000}
	results, _, err := parseAll(t, out, Options{Limits: limits})
	require.NoError(t, err)
	require.Len(t, results, 1)
	r := results[0]
	// Cut before the é rather than through it, the newline kept.
	require.Equal(t, strings.Repeat("a", 100)+"[... omitted 102 bytes of long line]\n", r.Lines)
	// Only the submatches within the kept text.
	require.Len(t, r.Submatches, 100)
	require.Equal(t, 100, r.Submatches[99].End)

	t.Run("submatches capped", func(t *testing.T) {
		results, _, err := parseAll(t, out, Options{Limits: Limits{MaxSubmatches: 3}})
		require.NoError(t, err)
		require.Equal(t, long, results[0].Lines)
		require.Equal(t, []Submatch{
			{Text: "a", Start: 0, End: 1},
			{Text: "a", Start: 1, End: 2},
			{Text: "a", Start: 2, End: 3},
		}, results[0].Submatches)
	})
}

func manyLines(n int) string {
	matches := make([][3]string, n)
	for i := range matches {
		matches[i] = [3]string{"f.txt", fmt.Sprint(i + 1), fmt.Sprintf("line %d\n", i)}
	}
	return rgOutput("", matches...)
}

func TestParseJSONMaxResults(t *testing.T) {
	out := manyLines(11)
	limits := Limits{MaxResults: 10}

	t.Run("no limit fails past the budget", func(t *testing.T) {
		_, _, err := parseAll(t, out, Options{Limits: limits})
		require.Error(t, err)
		require.True(t, IsBudgetError(err))
		require.Contains(t, err.Error(), "search matched more than 10 lines")
		require.Contains(t, err.Error(), "set a limit")
	})

	t.Run("exactly the budget is fine", func(t *testing.T) {
		results, limitReached, err := parseAll(t, manyLines(10), Options{Limits: limits})
		require.NoError(t, err)
		require.False(t, limitReached)
		require.Len(t, results, 10)
	})

	t.Run("a limit truncates instead", func(t *testing.T) {
		limit := 3
		results, limitReached, err := parseAll(t, out, Options{Limits: limits, Limit: &limit})
		require.NoError(t, err)
		require.True(t, limitReached)
		require.Len(t, results, 3)
	})

	t.Run("a limit replaces the budget", func(t *testing.T) {
		limit := 100
		results, limitReached, err := parseAll(t, out, Options{Limits: limits, Limit: &limit})
		require.NoError(t, err)
		require.False(t, limitReached)
		require.Len(t, results, 11)
	})
}

func TestParseJSONMaxBytes(t *testing.T) {
	out := manyLines(100)
	limit := 1000
	_, _, err := parseAll(t, out, Options{Limits: Limits{MaxBytes: 1 << 10}, Limit: &limit})
	require.Error(t, err)
	require.True(t, IsBudgetError(err))
	require.Contains(t, err.Error(), "search results exceed")
}

func TestParseFiles(t *testing.T) {
	out := "a\nb\n\nc\n"
	collect := func(opts Options) ([]string, bool, error) {
		var paths []string
		limitReached, err := ParseFiles(strings.NewReader(out), opts, func(p string) error {
			paths = append(paths, p)
			return nil
		})
		return paths, limitReached, err
	}
	paths, limitReached, err := collect(Options{Limits: DefaultLimits()})
	require.NoError(t, err)
	require.False(t, limitReached)
	require.Equal(t, []string{"a", "b", "c"}, paths)

	_, _, err = collect(Options{Limits: Limits{MaxResults: 2}})
	require.ErrorContains(t, err, "search matched more than 2 files")

	limit := 2
	paths, limitReached, err = collect(Options{Limits: Limits{MaxResults: 1}, Limit: &limit})
	require.NoError(t, err)
	require.True(t, limitReached)
	require.Equal(t, []string{"a", "b"}, paths)
}

func TestScanLines(t *testing.T) {
	long := strings.Repeat("x", 200<<10) // longer than the reader's buffer
	in := "short\n" + long + "\nlast"
	var lines []string
	require.NoError(t, ScanLines(strings.NewReader(in), 10, func(l string) error {
		lines = append(lines, l)
		return nil
	}))
	require.Equal(t, []string{
		"short",
		"xxxxxxxxxx" + fmt.Sprintf("[... omitted %d bytes of long line]", len(long)-10),
		"last",
	}, lines)
}

// dotLineReader streams the `rg --json` output of searching a single line of
// n bytes for ".": one message with a submatch per byte, ~40 times the line,
// generated as it is read rather than held.
func dotLineReader(n int) io.Reader {
	pr, pw := io.Pipe()
	go func() {
		w := bufio.NewWriter(pw)
		fmt.Fprintf(w, `{"type":"match","data":{"path":{"text":"min.js"},"lines":{"text":"`)
		for range n {
			w.WriteByte('a')
		}
		fmt.Fprintf(w, `\n"},"line_number":1,"absolute_offset":0,"submatches":[`)
		for i := range n {
			if i > 0 {
				w.WriteByte(',')
			}
			fmt.Fprintf(w, `{"match":{"text":"a"},"start":%d,"end":%d}`, i, i+1)
		}
		w.WriteString("]}}\n")
		pw.CloseWithError(w.Flush())
	}()
	return pr
}

// peakHeapReader samples the live heap (after a GC) every `every` bytes
// read.
type peakHeapReader struct {
	r     io.Reader
	every int
	read  int
	next  int
	peak  uint64
}

func (p *peakHeapReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.read += n
	if p.read >= p.next {
		p.next = p.read + p.every
		p.sample()
	}
	return n, err
}

func (p *peakHeapReader) sample() {
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	p.peak = max(p.peak, ms.HeapAlloc)
}

// One 1 MiB line searched for "." is a single ~42 MiB message of a million
// submatches. Decoding it whole held ~130 MiB live (the decoder's buffer and
// the submatches); streamed, the parse holds ~3 MiB, little more than the
// line itself.
func TestParseJSONLongLineMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("streams a 42 MiB message")
	}
	const lineBytes = 1 << 20
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	base := ms.HeapAlloc

	pr := &peakHeapReader{r: dotLineReader(lineBytes), every: 1 << 20}
	var results []Result
	_, err := ParseJSON(pr, Options{Limits: DefaultLimits()}, func(r Result) error {
		results = append(results, r)
		return nil
	})
	require.NoError(t, err)
	pr.sample()

	require.Len(t, results, 1)
	require.Len(t, results[0].Submatches, DefaultMaxSubmatches)
	require.Less(t, len(results[0].Lines), DefaultMaxLineBytes+100)
	growth := int64(pr.peak) - int64(base)
	t.Logf("peak live heap growth: %d KiB", growth>>10)
	require.Less(t, growth, int64(16<<20), "parsing held %d MiB live", growth>>20)
}
