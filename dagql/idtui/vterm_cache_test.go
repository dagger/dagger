package idtui

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/require"
	"github.com/vito/midterm"
)

func TestVtermLazySpillAndCache(t *testing.T) {
	cache := newTerminalCache()
	cache.maxCount = 3
	cache.maxCells = 10000
	text := strings.Repeat("all logs remain searchable\r\n", 1500)
	terms := make([]*Vterm, 30)
	for i := range terms {
		term := NewVterm(termenv.ANSI)
		t.Cleanup(term.Close)
		term.cache = cache
		term.SetWidth(80)
		_, err := io.WriteString(term, text)
		require.NoError(t, err)
		term.SetSearchHighlight("", -1)
		require.Nil(t, term.vt, "ingestion, width, and clearing search must remain lazy")
		require.Zero(t, term.terminalBuf.memory.Len())
		require.NotEmpty(t, term.terminalBuf.path)
		require.Zero(t, term.rawBuf.Len(), "plain terminal output is spooled once")
		require.Empty(t, term.rawBuf.path)
		terms[i] = term
	}
	for _, term := range terms {
		var raw bytes.Buffer
		require.NoError(t, term.PrintRaw(&raw))
		require.Equal(t, text, raw.String())
		require.Nil(t, term.vt, "raw output must not expand terminal cells")
		term.SetHeight(3)
		require.Contains(t, term.View(), "all logs remain searchable")
		require.Equal(t, 1, cache.entries.Len(), "oversized active terminal evicts other terminals")
	}
	require.Nil(t, terms[0].vt)
	count, _ := terms[0].Search("searchable", 0)
	require.Equal(t, 1500, count, "search includes the complete spooled log")
	require.Equal(t, 1500, len(terms[0].SearchMatchRows()))
	path := terms[0].terminalBuf.path
	terms[0].Close()
	_, err := os.Stat(path)
	require.ErrorIs(t, err, os.ErrNotExist)
}

// rawLog is what PrintRaw replays, before ANSI stripping.
func rawLog(term *Vterm) string {
	term.mu.Lock()
	defer term.mu.Unlock()
	return term.rawLocked().String()
}

// The raw log shares the terminal spool until a write's raw bytes diverge
// from what the terminal interprets.
func TestVtermRawLogSharesTerminalSpool(t *testing.T) {
	for _, diverge := range []struct {
		name  string
		write func(*Vterm) error
		raw   string
	}{
		{"diff", func(term *Vterm) error { _, err := term.WriteDiff([]byte("-old\n+new\n")); return err }, "-old\n+new\n"},
		{"markdown", func(term *Vterm) error { _, err := term.WriteMarkdown([]byte("**bold**\n")); return err }, "**bold**\n"},
		{"media", func(term *Vterm) error { term.WriteMedia(dagui.MediaRecord{Kind: "audio"}, "[audio]"); return nil }, "[audio]\n"},
	} {
		t.Run(diverge.name, func(t *testing.T) {
			term := NewVterm(termenv.ANSI)
			t.Cleanup(term.Close)
			text := strings.Repeat("\x1b[31mspilled\x1b[0m line\r\n", 3000)
			_, err := io.WriteString(term, text)
			require.NoError(t, err)
			require.NotEmpty(t, term.terminalBuf.path)
			require.Zero(t, term.rawBuf.Len())
			require.Empty(t, term.rawBuf.path, "no second spool for identical bytes")
			var raw bytes.Buffer
			require.NoError(t, term.PrintRaw(&raw))
			require.Equal(t, text, raw.String())

			require.NoError(t, diverge.write(term))
			_, err = io.WriteString(term, "after\n")
			require.NoError(t, err)
			raw.Reset()
			require.NoError(t, term.PrintRaw(&raw))
			require.Equal(t, text+diverge.raw+"after\n", raw.String())
		})
	}
}

func TestVtermCacheReplaysLogicalANSI(t *testing.T) {
	term := NewVterm(termenv.ANSI)
	t.Cleanup(term.Close)
	eager := midterm.NewAutoResizingTerminal()
	write := func(s string) {
		_, err := io.WriteString(term, s)
		require.NoError(t, err)
		_, err = io.WriteString(eager, s)
		require.NoError(t, err)
	}
	resize := func(width int) { term.SetWidth(width) }
	resize(12)
	write("long text wrapping at the original width\r\n")
	write("\x1b[31")
	write("mred\x1b[0m\roverwrite\r\n")
	term.SetHeight(3)
	term.ScrollToTop()
	before := term.View()
	term.mu.Lock()
	term.evictLocked()
	term.mu.Unlock()
	require.Equal(t, before, term.View())
	resize(24)
	write("\x1b[1A\x1b[2Kreplacement\r\nend")
	term.mu.Lock()
	term.evictLocked()
	term.mu.Unlock()
	actual := term.Term()
	require.Equal(t, eager.Content, actual.Content)
	var want, got bytes.Buffer
	for row := 0; row < eager.UsedHeight(); row++ {
		eager.RenderLineFgBg(&want, row, nil, nil)
		actual.RenderLineFgBg(&got, row, nil, nil)
	}
	require.Equal(t, want.String(), got.String())
}

func TestVtermCacheCountAndScroll(t *testing.T) {
	cache := newTerminalCache()
	cache.maxCount = 2
	var terms []*Vterm
	for range 6 {
		term := NewVterm(termenv.Ascii)
		t.Cleanup(term.Close)
		term.cache = cache
		_, err := io.WriteString(term, "one\r\ntwo\r\nthree\r\nfour")
		require.NoError(t, err)
		term.SetHeight(2)
		terms = append(terms, term)
	}
	require.Equal(t, 2, cache.entries.Len())
	require.Nil(t, terms[0].vt)
	terms[0].ScrollBy(-1)
	require.Equal(t, 1, terms[0].Offset)
	terms[0].ScrollToTop()
	require.Contains(t, terms[0].View(), "one")
	terms[1].View()
	terms[2].View()
	require.Nil(t, terms[0].vt)
	_, err := io.WriteString(terms[0], "\r\nfive")
	require.NoError(t, err)
	require.Contains(t, terms[0].View(), "one", "eviction preserves scrolled viewport")
}

func TestVtermSpoolErrorsAreVisible(t *testing.T) {
	term := NewVterm(termenv.Ascii)
	t.Cleanup(term.Close)
	_, err := io.WriteString(term, strings.Repeat("line\r\n", 10000))
	require.NoError(t, err)
	require.NoError(t, os.Remove(term.terminalBuf.path))
	require.Contains(t, term.View(), "log storage error")
	require.Error(t, term.PrintRaw(io.Discard))
	require.Error(t, term.Err())
}

func TestVtermSpoolWriteFailure(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir()+"/missing")
	term := NewVterm(termenv.Ascii)
	t.Cleanup(term.Close)
	_, err := io.WriteString(term, strings.Repeat("line\n", 10000))
	require.Error(t, err)
	require.Error(t, term.PrintRaw(io.Discard))
	require.Contains(t, term.View(), "log storage error")
	media := NewVterm(termenv.Ascii)
	t.Cleanup(media.Close)
	media.WriteMedia(dagui.MediaRecord{Kind: "audio"}, "[audio]")
	_, err = media.WriteMarkdown([]byte(strings.Repeat("markdown\n", 10000)))
	require.Error(t, err)
	require.Contains(t, media.View(), "log storage error")
}

func TestVtermCacheMediaAndMarkdown(t *testing.T) {
	cache := newTerminalCache()
	cache.maxCount = 1
	term := NewVterm(termenv.Ascii)
	t.Cleanup(term.Close)
	term.cache = cache
	term.SetWidth(40)
	_, err := term.WriteMarkdown([]byte("**before**\n"))
	require.NoError(t, err)
	_, err = term.WriteDiff([]byte("-old\n+new\n"))
	require.NoError(t, err)
	require.Nil(t, term.vt)
	term.WriteMedia(dagui.MediaRecord{Kind: "audio"}, "[audio]")
	require.Nil(t, term.vt, "media ingestion must not materialize preceding text")
	_, err = term.WriteMarkdown([]byte("**after**\n"))
	require.NoError(t, err)
	term.SetHeight(20)
	before := term.View()
	other := NewVterm(termenv.Ascii)
	t.Cleanup(other.Close)
	other.cache = cache
	other.View()
	require.Nil(t, term.vt)
	require.Nil(t, term.mediaRows)
	require.Equal(t, before, term.View())
	var raw bytes.Buffer
	require.NoError(t, term.PrintRaw(&raw))
	require.Equal(t, "**before**\n-old\n+new\n[audio]\n**after**\n", raw.String())
}

func TestVtermCacheConcurrentAccess(t *testing.T) {
	cache := newTerminalCache()
	cache.maxCount = 2
	var wg sync.WaitGroup
	for range 6 {
		term := NewVterm(termenv.Ascii)
		t.Cleanup(term.Close)
		term.cache = cache
		for worker := range 2 {
			wg.Go(func() {
				for i := range 20 {
					if worker == 0 {
						_, _ = fmt.Fprintf(term, "line %d\r\n", i)
					} else {
						term.SetHeight(3)
						term.View()
						term.Search("line", 0)
						term.SearchMatchRows()
						term.PrintRaw(io.Discard)
					}
				}
			})
		}
	}
	wg.Wait()
}

func TestPrettyLogsCollapsedSpansStayLazy(t *testing.T) {
	db := dagui.NewDB()
	root := prettyTestSpanID(1)
	start := time.Unix(100, 0)
	snapshots := []dagui.SpanSnapshot{{ID: root, TraceID: prettyTestTraceID(), Name: "root", StartTime: start, EndTime: start.Add(time.Second), Final: true}}
	for i := byte(2); i < 52; i++ {
		snapshots = append(snapshots, dagui.SpanSnapshot{ID: prettyTestSpanID(i), ParentID: root, TraceID: prettyTestTraceID(), Name: fmt.Sprintf("child %d", i), StartTime: start, EndTime: start.Add(time.Second), Final: true})
	}
	db.ImportSnapshots(snapshots)
	db.SetPrimarySpan(root)
	fe := NewWithDB(io.Discard, db)
	t.Cleanup(fe.logs.Close)
	fe.FrontendOpts.Verbosity = dagui.ShowCompletedVerbosity
	fe.FrontendOpts.GCThreshold = time.Hour
	fe.FrontendOpts.SpanExpanded = map[dagui.SpanID]bool{root: true}
	for _, snapshot := range snapshots[1:] {
		fe.FrontendOpts.SpanExpanded[snapshot.ID] = false
		_, err := io.WriteString(fe.logs.spanLogs(snapshot.ID), strings.Repeat("hidden output\r\n", 3000))
		require.NoError(t, err)
	}
	fe.logs.SetWidth(80)
	fe.recalculateViewLocked()
	_ = fe.tui.RenderLines()
	for _, logs := range fe.logs.Logs {
		require.Nil(t, logs.vt, "collapsed spans must not materialize while drawing the tree")
	}
	require.Zero(t, fe.logs.Cache.entries.Len())
}

// An active search refreshes on every frame. With more logs than the cache
// holds, that must not replay every log every frame.
func TestPrettyLogsSearchFramesStayOffTerminalCache(t *testing.T) {
	db := dagui.NewDB()
	root := prettyTestSpanID(1)
	start := time.Unix(100, 0)
	snapshots := []dagui.SpanSnapshot{{ID: root, TraceID: prettyTestTraceID(), Name: "root", StartTime: start, EndTime: start.Add(time.Second), Final: true}}
	for i := byte(2); i < 42; i++ {
		snapshots = append(snapshots, dagui.SpanSnapshot{ID: prettyTestSpanID(i), ParentID: root, TraceID: prettyTestTraceID(), Name: fmt.Sprintf("child %d", i), StartTime: start, EndTime: start.Add(time.Second), Final: true})
	}
	db.ImportSnapshots(snapshots)
	db.SetPrimarySpan(root)
	fe := NewWithDB(io.Discard, db)
	t.Cleanup(fe.logs.Close)
	fe.FrontendOpts.Verbosity = dagui.ShowCompletedVerbosity
	fe.FrontendOpts.GCThreshold = time.Hour
	fe.FrontendOpts.SpanExpanded = map[dagui.SpanID]bool{root: true}
	text := strings.Repeat(strings.Repeat("ordinary output\r\n", 99)+"needle\r\n", 20)
	for _, snapshot := range snapshots[1:] {
		fe.FrontendOpts.SpanExpanded[snapshot.ID] = false
		_, err := io.WriteString(fe.logs.spanLogs(snapshot.ID), text)
		require.NoError(t, err)
	}
	fe.logs.SetWidth(80)
	fe.recalculateViewLocked()
	fe.searchQuery = "needle"
	replays := func() int {
		total := 0
		for _, logs := range fe.logs.Logs {
			logs.mu.Lock()
			total += logs.replays
			logs.mu.Unlock()
		}
		return total
	}
	frame := func() {
		fe.Update() // the root renders, and refreshes search, on every frame
		_ = fe.tui.RenderLines()
	}
	frame()
	require.Equal(t, 40, replays(), "the first search frame reads each log once")
	require.Len(t, fe.searchMatches, 40*20, "search covers every log")
	require.LessOrEqual(t, fe.logs.Cache.entries.Len(), terminalCacheCount)
	for range 5 {
		frame()
	}
	require.Equal(t, 40, replays(), "unchanged logs are not replayed on later frames")
	require.Len(t, fe.searchMatches, 40*20)

	// New output is found on the next frame; only the changed log is read.
	var evicted *Vterm
	for _, logs := range fe.logs.Logs {
		if logs.vt == nil {
			evicted = logs
			break
		}
	}
	require.NotNil(t, evicted)
	_, err := io.WriteString(evicted, "another needle\r\n")
	require.NoError(t, err)
	frame()
	require.Equal(t, 41, replays())
	require.Len(t, fe.searchMatches, 40*20+1)
	require.Nil(t, evicted.vt, "searching an evicted log leaves it evicted")

	fe.clearSearch()
	for range 2 {
		frame()
	}
	require.Equal(t, 41, replays(), "clearing search does not materialize logs")
}

// Logs on screen together must not evict each other on every frame, even when
// they exceed the cell budget together.
func TestVtermCacheKeepsShownTerminals(t *testing.T) {
	cache := newTerminalCache()
	cache.maxCells = 1000
	var terms []*Vterm
	for range 3 {
		term := NewVterm(termenv.Ascii)
		t.Cleanup(term.Close)
		term.cache = cache
		term.SetHeight(3)
		_, err := io.WriteString(term, strings.Repeat("line of output\r\n", 100))
		require.NoError(t, err)
		terms = append(terms, term)
	}
	a, b, c := terms[0], terms[1], terms[2]
	cache.beginFrame()
	a.View()
	b.View()
	aReplays, bReplays := a.replays, b.replays
	for range 10 {
		cache.beginFrame()
		a.View()
		b.View()
	}
	require.Equal(t, aReplays, a.replays)
	require.Equal(t, bReplays, b.replays)
	// Once b leaves the screen, it is evicted to make room for others.
	cache.beginFrame()
	a.View()
	c.View()
	cReplays := c.replays
	for range 3 {
		cache.beginFrame()
		a.View()
		c.View()
	}
	require.Nil(t, b.vt)
	require.NotNil(t, a.vt)
	require.Equal(t, aReplays, a.replays)
	require.Equal(t, cReplays, c.replays)
}

func TestVtermStreamAndLayoutStayLazy(t *testing.T) {
	term := NewVterm(termenv.ANSI)
	t.Cleanup(term.Close)
	const text = "\x1b[31mfirst logical line\x1b[0m\nsecond"
	_, err := io.WriteString(term, text)
	require.NoError(t, err)
	for width := 1; width < 100; width++ {
		term.SetWidth(width)
		term.SetPrefix("> ")
	}
	require.Nil(t, term.vt)
	stored, err := term.terminalBuf.contents()
	require.NoError(t, err)
	require.Equal(t, text, stored, "the spool contains only stream bytes, without write/resize framing")
	term.SetWidth(8)
	term.SetHeight(2)
	logical := term.vt
	firstWrap := &term.textRows[0].wraps[0]
	for range 10 {
		_, err := io.WriteString(term, "!")
		require.NoError(t, err)
	}
	require.Same(t, logical, term.vt, "live writes incrementally update one logical terminal")
	require.True(t, term.layoutDirty, "writes do not eagerly lay out the stream")
	term.View()
	require.Same(t, firstWrap, &term.textRows[0].wraps[0], "unchanged logical rows reuse wrap metadata")
	require.Equal(t, "!!!!", ansi.Strip(term.LastLine()))
	var clipped bytes.Buffer
	term.Render(&clipped, 1, 1)
	require.Contains(t, clipped.String(), "\x1b[31m", "clipped continuation rows carry their own style")
}

func TestVtermMediaProgressBeforeWrapping(t *testing.T) {
	term := NewVterm(termenv.Ascii)
	t.Cleanup(term.Close)
	term.SetWidth(4)
	_, err := io.WriteString(term, "abcdefghij\r\x1b[2Kdone")
	require.NoError(t, err)
	term.WriteMedia(dagui.MediaRecord{Kind: "audio"}, "img")
	_, err = io.WriteString(term, "klmnopqrst\x1b[10Dnext\x1b[K")
	require.NoError(t, err)
	require.Equal(t, "done\nimg\nnext\n", mediaTestView(term))
	term.SetWidth(20)
	require.Equal(t, "done\nimg\nnext\n", mediaTestView(term))
}

// One search frame over 40 unchanged logs of 2000 lines: more logs than the
// terminal cache holds.
func BenchmarkVtermSearchFrame(b *testing.B) {
	cache := newTerminalCache()
	text := strings.Repeat(strings.Repeat("ordinary output\r\n", 99)+"needle\r\n", 20)
	terms := make([]*Vterm, 40)
	for i := range terms {
		terms[i] = NewVterm(termenv.Ascii)
		terms[i].cache = cache
		terms[i].SetWidth(80)
		_, _ = io.WriteString(terms[i], text)
		b.Cleanup(terms[i].Close)
	}
	b.ReportAllocs()
	for b.Loop() {
		cache.beginFrame()
		for _, term := range terms {
			term.SetSearchHighlight("needle", -1)
		}
		for _, term := range terms {
			if len(term.SearchMatchRows()) != 20 {
				b.Fatal("missing matches")
			}
		}
	}
}

func BenchmarkVtermHiddenLogs(b *testing.B) {
	text := []byte(strings.Repeat("ordinary log output\r\n", 2000))
	b.Run("lazy-spooled", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			term := NewVterm(termenv.Ascii)
			_, _ = term.Write(text)
			term.Close()
		}
	})
	b.Run("eager-midterm", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			term := midterm.NewAutoResizingTerminal()
			_, _ = term.Write(text)
		}
	})
}
