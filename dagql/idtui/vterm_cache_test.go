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
		require.Zero(t, term.rawBuf.memory.Len())
		require.Zero(t, term.terminalBuf.memory.Len())
		require.NotEmpty(t, term.rawBuf.path)
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
	path := terms[0].rawBuf.path
	terms[0].Close()
	_, err := os.Stat(path)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestVtermCacheReplaysANSIAndResizeHistory(t *testing.T) {
	term := NewVterm(termenv.ANSI)
	t.Cleanup(term.Close)
	eager := midterm.NewAutoResizingTerminal()
	write := func(s string) {
		_, err := io.WriteString(term, s)
		require.NoError(t, err)
		_, err = io.WriteString(eager, s)
		require.NoError(t, err)
	}
	resize := func(width int) { term.SetWidth(width); eager.ResizeX(width) }
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
