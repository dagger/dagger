package idtui

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/require"
	"github.com/vito/tuist"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"

	"github.com/dagger/dagger/dagql/dagui"
)

// timedLog is a log record's body and the time it was logged at.
type timedLog struct {
	body string
	at   time.Time
}

var zoomedLogsStart = time.Unix(100, 0)

// zoomedLogsFrontend is a shell session zoomed to a root span with three
// steps started at 1s, 3s and 5s, and the root's own logs as given, in the
// order given.
func zoomedLogsFrontend(t *testing.T, logs ...timedLog) *frontendPretty {
	t.Helper()
	t.Setenv("NO_COLOR", "1")
	start := zoomedLogsStart
	rootID := prettyTestSpanID(1)
	db := dagui.NewDB()
	snapshots := []dagui.SpanSnapshot{{
		ID: rootID, TraceID: prettyTestTraceID(), Name: "root",
		StartTime: start, EndTime: start.Add(time.Minute), Final: true,
	}}
	for i, name := range []string{"first-step", "second-step", "third-step"} {
		at := start.Add(time.Duration(2*i+1) * time.Second)
		snapshots = append(snapshots, dagui.SpanSnapshot{
			ID: prettyTestSpanID(byte(2 + i)), TraceID: prettyTestTraceID(), ParentID: rootID,
			Name: name, StartTime: at, EndTime: at.Add(time.Second), Final: true,
		})
	}
	db.ImportSnapshots(snapshots)
	db.SetPrimarySpan(rootID)

	fe := newWithTerminal(io.Discard, db, tuist.NewHeadlessTerminal(120, 60))
	fe.shell = stubShellHandler{}
	fe.FrontendOpts.Verbosity = dagui.ShowCompletedVerbosity
	fe.FrontendOpts.GCThreshold = time.Hour
	fe.ZoomedSpan = rootID
	for _, l := range logs {
		rec := frontendTestLogRecord(rootID.SpanID, otellog.StringValue(l.body))
		rec.SetTraceID(prettyTestTraceID().TraceID)
		rec.SetTimestamp(l.at)
		require.NoError(t, fe.logs.Export(context.Background(), []sdklog.Record{rec}))
	}
	fe.recalculateViewLocked()
	return fe
}

func zoomedLogsLines(fe *frontendPretty) []string {
	return strings.Split(ansi.Strip(strings.Join(fe.tui.RenderLines(), "\n")), "\n")
}

func after(d time.Duration) time.Time { return zoomedLogsStart.Add(d) }

// lineIndex is the index of the first of lines holding substr.
func lineIndex(t *testing.T, lines []string, substr string) int {
	t.Helper()
	for i, line := range lines {
		if strings.Contains(ansi.Strip(line), substr) {
			return i
		}
	}
	t.Fatalf("no line contains %q in:\n%s", substr, strings.Join(lines, "\n"))
	return -1
}

// requireOrder requires each text on a line below the one before.
func requireOrder(t *testing.T, lines []string, texts ...string) {
	t.Helper()
	prev := -1
	for _, text := range texts {
		at := lineIndex(t, lines, text)
		require.Greater(t, at, prev, "%q out of order:\n%s", text, strings.Join(lines, "\n"))
		prev = at
	}
}

// TestZoomedLogsInterleaveWithRows: in a shell session the zoomed span's own
// logs render between its rows, in the order they were logged, instead of
// piling up in a region beneath them all.
func TestZoomedLogsInterleaveWithRows(t *testing.T) {
	fe := zoomedLogsFrontend(t,
		timedLog{"log-before-all\n", after(500 * time.Millisecond)},
		timedLog{"log-after-first\n", after(2 * time.Second)},
		timedLog{"log-after-second\n", after(4 * time.Second)},
		timedLog{"log-after-third\n", after(6 * time.Second)},
	)
	requireOrder(t, zoomedLogsLines(fe),
		"log-before-all", "first-step",
		"log-after-first", "second-step",
		"log-after-second", "third-step",
		"log-after-third",
	)
}

// TestZoomedLogsSpacingBetweenReplies: a block of logs sits one blank line
// apart from the replies around it, as replies sit from each other.
func TestZoomedLogsSpacingBetweenReplies(t *testing.T) {
	fe := zoomedLogsFrontend(t, timedLog{"log-after-first\n", after(2 * time.Second)})
	for _, id := range []byte{2, 3, 4} {
		// Replies, which open with a blank line of their own; one with
		// nothing said isn't shown.
		fe.db.Spans.Map[prettyTestSpanID(id)].LLMRole = "assistant"
		_, _ = fe.logs.spanLogs(prettyTestSpanID(id)).WriteMarkdown([]byte("said"))
	}
	fe.recalculateViewLocked()
	lines := zoomedLogsLines(fe)
	requireOrder(t, lines, "first-step", "log-after-first", "second-step", "third-step")
	for i := 1; i < len(lines); i++ {
		require.False(t, strings.TrimSpace(lines[i-1]) == "" && strings.TrimSpace(lines[i]) == "",
			"two blank lines in a row:\n%s", strings.Join(lines, "\n"))
	}
}

// TestZoomedLogsSplitFollowsNewRows: a row arriving after logs that were
// written after it started splits them, the piece before it keeping its
// place.
func TestZoomedLogsSplitFollowsNewRows(t *testing.T) {
	fe := zoomedLogsFrontend(t,
		timedLog{"early-log\n", after(6 * time.Second)},
		timedLog{"late-log\n", after(8 * time.Second)},
	)
	requireOrder(t, zoomedLogsLines(fe), "third-step", "early-log", "late-log")

	// A fourth step starts between the two logs.
	at := after(7 * time.Second)
	fe.db.ImportSnapshots([]dagui.SpanSnapshot{{
		ID: prettyTestSpanID(9), TraceID: prettyTestTraceID(), ParentID: prettyTestSpanID(1),
		Name: "fourth-step", StartTime: at, EndTime: at.Add(time.Second), Final: true,
	}})
	fe.recalculateViewLocked()
	lines := zoomedLogsLines(fe)
	requireOrder(t, lines, "third-step", "early-log", "fourth-step", "late-log")
	require.Equal(t, 1, strings.Count(strings.Join(lines, "\n"), "early-log"), "each log line renders once")
}

// TestZoomedLogsKeepTerminalState: the pieces are rows of the one screen,
// so output rewriting a line logged before a row started shows the line as
// it ends up, in its place, rather than both versions.
func TestZoomedLogsKeepTerminalState(t *testing.T) {
	fe := zoomedLogsFrontend(t, timedLog{"progress 0%\n", after(500 * time.Millisecond)})
	requireOrder(t, zoomedLogsLines(fe), "progress 0%", "first-step")

	// Rewritten once the piece holding it has rendered.
	rec := frontendTestLogRecord(prettyTestSpanID(1).SpanID, otellog.StringValue("\x1b[1A\x1b[2Kprogress 100%\n"))
	rec.SetTraceID(prettyTestTraceID().TraceID)
	rec.SetTimestamp(after(2 * time.Second))
	require.NoError(t, fe.logs.Export(context.Background(), []sdklog.Record{rec}))
	fe.Update()
	lines := zoomedLogsLines(fe)
	text := strings.Join(lines, "\n")
	require.NotContains(t, text, "progress 0%")
	requireOrder(t, lines, "progress 100%", "first-step")
}

// TestZoomedLogsOutsideShellStayBelowRows: a viewport-clipped view crops
// its rows around the focused one, so it keeps the logs beneath them where
// they have room of their own.
func TestZoomedLogsOutsideShellStayBelowRows(t *testing.T) {
	fe := zoomedLogsFrontend(t, timedLog{"log-before-all\n", after(500 * time.Millisecond)})
	fe.shell = nil
	fe.recalculateViewLocked()
	lines := strings.Split(ansi.Strip(strings.Join(fe.tui.Frame(), "\n")), "\n")
	requireOrder(t, lines, "third-step", "log-before-all")
}

// TestVtermTouchedRow: appending touches only the rows from the cursor's
// down, colours included, while moving the cursor may touch any row.
func TestVtermTouchedRow(t *testing.T) {
	vt := NewVterm(termenv.Ascii)
	_, _ = vt.Write([]byte("a\nb\n"))
	vt.RenderRows(0, 1) // lay the screen out
	_, _ = vt.TakeTouchedRow()

	_, touched := vt.TakeTouchedRow()
	require.False(t, touched)

	_, _ = vt.Write([]byte("\x1b[33mWRN\x1b[0m c\n"))
	_, _ = vt.Write([]byte("\x1b]8;;https://dagger.io\x1b\\link\x1b]8;;\x1b\\\r\n"))
	row, touched := vt.TakeTouchedRow()
	require.True(t, touched)
	require.Equal(t, 2, row, "an appended line touches its own row on")

	_, _ = vt.Write([]byte("\x1b[1A\x1b[2Kd\n"))
	row, _ = vt.TakeTouchedRow()
	require.Equal(t, 0, row, "moving the cursor up may touch any row")
}

// TestVtermTimeRows pins the split points: each time splits at the first
// line logged at or after it, a line's own continuation is never split from
// it, and nothing marked means no split.
func TestVtermTimeRows(t *testing.T) {
	at := func(s int) time.Time { return time.Unix(int64(s), 0) }
	vt := NewVterm(termenv.Ascii)
	_, _, ok := vt.TimeRows(nil)
	require.False(t, ok, "an unmarked log can't be split")

	vt.MarkTime(at(10))
	_, _ = vt.Write([]byte("a\n"))
	vt.MarkTime(at(20))
	_, _ = vt.Write([]byte("b"))
	vt.MarkTime(at(30)) // mid-line: not a split point
	_, _ = vt.Write([]byte("cd\n"))
	vt.MarkTime(at(40))
	_, _ = vt.Write([]byte("e\n"))

	rows, used, ok := vt.TimeRows([]time.Time{at(5), at(15), at(25), {}, at(45)})
	require.True(t, ok)
	require.Equal(t, 3, used)
	require.Equal(t, []int{0, 1, 2, 2, 3}, rows)
	require.Equal(t, []string{"bcd"}, vt.RenderRows(1, 2))

	rows, _, _ = vt.TimeRows([]time.Time{at(35), at(15)})
	require.Equal(t, []int{2, 2}, rows, "rows never go back")

	// Laid out anew, narrower than a line, the marks follow their lines.
	vt.SetWidth(2)
	rows, used, _ = vt.TimeRows([]time.Time{at(15), at(35)})
	require.Equal(t, 1, rows[0])
	got := vt.RenderRows(rows[1], rows[1]+1)
	require.Len(t, got, 1, "%q", vt.RenderRows(0, used))
	require.Equal(t, "e", strings.TrimSpace(got[0]), "%q", vt.RenderRows(0, used))
}
