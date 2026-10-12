package idtui

import (
	"encoding/binary"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/muesli/termenv"
	"github.com/stretchr/testify/require"
	"github.com/vito/tuist"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/dagql/dagui"
)

func perfSpanID(n uint64) dagui.SpanID {
	var id trace.SpanID
	binary.BigEndian.PutUint64(id[:], n+1)
	return dagui.SpanID{SpanID: id}
}

// newLargeTraceFrontend builds a live (interactive) frontend over a trace of
// roughly tops*fanout^depth completed spans hanging off a running root: a
// handful of top-level rows, each hiding a big collapsed subtree. With agent
// set, each top-level row is a conversation turn instead, the shape of a long
// `dagger agent` session: a user prompt and an assistant reply, with a tool
// call under the reply hiding the subtree, the conversation promoted onto the
// root. It returns the frontend and the IDs of the deepest spans, under which
// streamed spans can land.
func newLargeTraceFrontend(tb testing.TB, tops, fanout, depth int, agent bool) (*frontendPretty, []dagui.SpanID, *uint64) {
	tb.Helper()
	db := dagui.NewDB()
	traceID := dagui.TraceID{TraceID: trace.TraceID{1}}
	start := time.Unix(100, 0)
	var next uint64
	newID := func() dagui.SpanID {
		id := perfSpanID(next)
		next++
		return id
	}
	rootID := newID()
	snaps := []dagui.SpanSnapshot{{
		ID: rootID, TraceID: traceID, Name: "root", StartTime: start,
	}}
	var leaves []dagui.SpanID
	var grow func(parent dagui.SpanID, level int)
	grow = func(parent dagui.SpanID, level int) {
		for range fanout {
			id := newID()
			snaps = append(snaps, dagui.SpanSnapshot{
				ID: id, TraceID: traceID, ParentID: parent, Name: "step",
				StartTime: start, EndTime: start.Add(time.Second),
			})
			if level+1 < depth {
				grow(id, level+1)
			} else {
				leaves = append(leaves, id)
			}
		}
	}
	for range tops {
		id := newID()
		if agent {
			snaps = append(snaps, dagui.SpanSnapshot{
				ID: id, TraceID: traceID, ParentID: rootID, Name: "prompt",
				LLMRole: "user", Message: "do the thing",
				StartTime: start, EndTime: start.Add(time.Second),
			})
			replyID := newID()
			snaps = append(snaps, dagui.SpanSnapshot{
				ID: replyID, TraceID: traceID, ParentID: rootID, Name: "reply",
				LLMRole: "assistant", Message: "on it",
				StartTime: start, EndTime: start.Add(time.Second),
			})
			id = newID()
			snaps = append(snaps, dagui.SpanSnapshot{
				ID: id, TraceID: traceID, ParentID: replyID, Name: "tool",
				LLMRole: "assistant", LLMTool: "run",
				StartTime: start, EndTime: start.Add(time.Second),
			})
		} else {
			snaps = append(snaps, dagui.SpanSnapshot{
				ID: id, TraceID: traceID, ParentID: rootID, Name: "top",
				StartTime: start, EndTime: start.Add(time.Second),
			})
		}
		grow(id, 0)
	}
	db.ImportSnapshots(snaps)
	db.SetPrimarySpan(rootID)

	fe := newWithTerminal(io.Discard, db, tuist.NewHeadlessTerminal(120, 40))
	fe.FrontendOpts.Verbosity = dagui.ShowCompletedVerbosity
	fe.FrontendOpts.GCThreshold = time.Hour
	fe.recalculateViewLocked()
	fe.tui.Frame()
	return fe, leaves, &next
}

// BenchmarkStreamingFrame measures live frames while spans stream into a
// large trace: every frameGap a batch lands (marking the view dirty, as
// ExportSpans does) and the TUI renders, rebuilding the view. ns/op is the
// render-loop time per frame (the gaps aren't timed).
//
// The zoomed shape zooms to the root, as SetPrimary does for every CLI
// command; agent is a `dagger agent` session, its conversation promoted onto
// the root; unzoomed lists every span as a top-level candidate.
func BenchmarkStreamingFrame(b *testing.B) {
	for _, shape := range []string{"zoomed", "agent", "unzoomed"} {
		b.Run(shape, func(b *testing.B) {
			benchmarkStreamingFrame(b, shape)
		})
	}
}

func benchmarkStreamingFrame(b *testing.B, shape string) {
	const frameGap = 25 * time.Millisecond
	fe, leaves, next := newLargeTraceFrontend(b, 50, 10, 4, shape == "agent") // ~555k spans
	if shape == "zoomed" {
		fe.ZoomedSpan = perfSpanID(0)
		fe.recalculateViewLocked()
	}
	traceID := dagui.TraceID{TraceID: trace.TraceID{1}}
	start := time.Unix(100, 0)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		time.Sleep(frameGap)
		b.StartTimer()
		id := perfSpanID(*next)
		*next++
		fe.db.ImportSnapshots([]dagui.SpanSnapshot{{
			ID: id, TraceID: traceID, ParentID: leaves[i%len(leaves)], Name: "streamed",
			StartTime: start, EndTime: start.Add(time.Second),
		}})
		fe.viewDirty = true
		fe.Update()
		fe.tui.Frame()
	}
}

const summaryPerfIndent = 2

// newTestSummaryFixture builds a test view of failing cases (each with
// logLines(i) lines of logs), two skipped cases with logs and a passing suite,
// plus the live TestView rendering its summary at width 80.
func newTestSummaryFixture(failing int, logLines func(i int) int) (*TestView, *dagui.TestView, *int) {
	logsBySpan := map[dagui.SpanID]*Vterm{}
	var roots []*dagui.TestNode
	addCase := func(i int, category dagui.TestCategory, lines int) {
		id := perfSpanID(uint64(i))
		name := fmt.Sprintf("case-%d", i)
		if lines > 0 {
			logs := NewVterm(termenv.Ascii)
			// Match the width the summary sizes logs to, so SetWidth is a no-op.
			logs.SetWidth(80 - (summaryPerfIndent + 8))
			for l := range lines {
				fmt.Fprintf(logs, "%s log line %d\n", name, l)
			}
			logsBySpan[id] = logs
		}
		roots = append(roots, &dagui.TestNode{
			ID:           dagui.TestNodeID(name),
			Kind:         dagui.TestNodeCase,
			Name:         name,
			Span:         &dagui.Span{SpanSnapshot: dagui.SpanSnapshot{ID: id, Name: name}},
			SelfCategory: category,
			Category:     category,
		})
	}
	for i := range failing {
		addCase(i, dagui.TestCategoryFailing, logLines(i))
	}
	addCase(failing, dagui.TestCategorySkipped, 2)
	addCase(failing+1, dagui.TestCategorySkipped, 1)
	roots = append(roots, &dagui.TestNode{
		ID:       "passing",
		Kind:     dagui.TestNodeSuite,
		Name:     "passing suite",
		FullName: "passing suite",
		Category: dagui.TestCategoryPassing,
		Counts:   dagui.TestCounts{Passing: 5},
	})
	view := &dagui.TestView{
		Roots:  roots,
		Counts: dagui.TestCounts{Failing: failing, Skipped: 2, Passing: 5},
	}
	requests := new(int)
	tv := &TestView{
		SummaryIndent:   summaryPerfIndent,
		SummaryLogLines: 8,
		Logs:            logsBySpan,
		RequestLogs:     func(dagui.SpanID) { *requests++ },
	}
	return tv, view, requests
}

// eagerTestSummaryLines is renderTestSummaryLines as it was before it learned
// to skip logs that can't be shown: every entry's logs are rendered, then the
// same layout decides what fits.
func eagerTestSummaryLines(tv *TestView, out TermOutput, view *dagui.TestView, width, height int) []string {
	if tv.testSummaryFinal() {
		width = 0
	}
	header := tv.renderTestSummaryHeader(out, strings.Repeat(" ", max(tv.SummaryIndent, 0)), width)
	entries := collectTestSummaryEntries(view)
	var blocks []testSummaryBlock
	for _, group := range [][]testSummaryEntry{entries.failing, entries.skipped, entries.running} {
		for _, entry := range group {
			blocks = append(blocks, testSummaryBlock{
				name: tv.renderTestSummaryEntryName(out, entry, width),
				logs: tv.renderTestSummaryLogs(out, entry, width),
			})
		}
	}
	var passing []string
	for _, entry := range entries.passing {
		passing = append(passing, tv.renderTestSummaryPassingSuite(out, entry, width))
	}
	counts := renderTestSummaryCounts(out, view.Counts, tv.SummaryIndent, width)
	full := tv.withSummaryNote(assembleTestSummary(header, blocks, passing, counts))
	if height <= 0 || len(full) <= height {
		return full
	}
	if height == 1 {
		return []string{tv.renderTestSummaryOneLine(out, view.Counts, width)}
	}
	compact := tv.renderTestSummaryCountsCompact(out, view.Counts, width)
	return tv.condenseTestSummary(out, header, blocks, passing, compact, height)
}

// TestTestSummarySkipsUnshownLogs checks that the live summary renders the
// same lines as rendering every entry's logs would, at every height, while
// only touching the logs of entries it can show.
func TestTestSummarySkipsUnshownLogs(t *testing.T) {
	render := func(f func(*TestView, TermOutput, *dagui.TestView, int, int) []string, tv *TestView, view *dagui.TestView, height int) string {
		var buf strings.Builder
		out := NewOutput(&buf, termenv.WithProfile(termenv.Ascii))
		return strings.Join(f(tv, out, view, 80, height), "\n")
	}
	for _, failing := range []int{0, 1, 3, 7} {
		tv, view, _ := newTestSummaryFixture(failing, func(i int) int { return []int{3, 0, 12, 1}[i%4] })
		require.Contains(t, render((*TestView).renderTestSummaryLines, tv, view, 0), "log line")
		for height := 0; height <= 60; height++ {
			require.Equal(t,
				render(eagerTestSummaryLines, tv, view, height),
				render((*TestView).renderTestSummaryLines, tv, view, height),
				"failing=%d height=%d", failing, height)
		}
	}

	// 12 entries in 30 rows: room for the names plus a couple of entries'
	// logs.
	tv, view, requests := newTestSummaryFixture(10, func(int) int { return 20 })
	render((*TestView).renderTestSummaryLines, tv, view, 30)
	require.LessOrEqual(t, *requests, 3, "only shown entries' logs should be rendered")
	*requests = 0
	render((*TestView).renderTestSummaryLines, tv, view, 0)
	require.Equal(t, 12, *requests, "an unbounded summary renders every entry's logs")

	// Too many entries for even their names: no logs at all.
	tv, view, requests = newTestSummaryFixture(500, func(int) int { return 20 })
	render((*TestView).renderTestSummaryLines, tv, view, 30)
	require.Zero(t, *requests)
}

func TestVtermPrintTailMatchesPrint(t *testing.T) {
	for _, content := range []string{
		"",
		"one\n",
		"one\ntwo",
		"one\n\n\ntwo\n\n\n",
		strings.Repeat("a line that wraps around the narrow terminal\n", 50),
	} {
		term := NewVterm(termenv.Ascii)
		term.SetWidth(12)
		_, _ = term.Write([]byte(content))
		var buf strings.Builder
		require.NoError(t, term.Print(&buf))
		want := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
		for _, n := range []int{1, 2, 3, 8, 1000} {
			got, total := term.PrintTail(n)
			require.Equal(t, len(want), total, "content=%q", content)
			require.Equal(t, want[len(want)-min(n, len(want)):], got, "content=%q n=%d", content, n)
		}
	}
}

// BenchmarkTestSummaryLive renders a live, height-bounded test summary over
// many failing tests with long logs, as a TestView does every frame. eager
// renders every entry's logs first, as the summary used to.
func BenchmarkTestSummaryLive(b *testing.B) {
	tv, view, _ := newTestSummaryFixture(300, func(int) int { return 2000 })
	var buf strings.Builder
	out := NewOutput(&buf, termenv.WithProfile(termenv.Ascii))
	b.Run("lazy", func(b *testing.B) {
		for b.Loop() {
			tv.renderTestSummaryLines(out, view, 80, 30)
		}
	})
	b.Run("eager", func(b *testing.B) {
		for b.Loop() {
			eagerTestSummaryLines(tv, out, view, 80, 30)
		}
	})
}

// BenchmarkVtermTail compares tailing a long log through Print with
// PrintTail, as the live summary does for each shown entry.
func BenchmarkVtermTail(b *testing.B) {
	term := NewVterm(termenv.Ascii)
	term.SetWidth(70)
	for l := range 20000 {
		fmt.Fprintf(term, "log line %d\n", l)
	}
	b.Run("Print", func(b *testing.B) {
		for b.Loop() {
			var buf strings.Builder
			_ = term.Print(&buf)
			lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
			_ = lines[len(lines)-8:]
		}
	})
	b.Run("PrintTail", func(b *testing.B) {
		for b.Loop() {
			term.PrintTail(8)
		}
	})
}
