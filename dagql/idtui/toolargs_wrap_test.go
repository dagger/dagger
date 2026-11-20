package idtui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/require"
	"github.com/vito/tuist"
)

// longToolArgs is a tool call's arguments on one long line, with no two
// windows alike, so a character lost anywhere shows.
func longToolArgs(prefix, suffix string) string {
	var sb strings.Builder
	sb.WriteString(prefix)
	for i := 0; sb.Len() < 480; i++ {
		sb.WriteByte(byte('a' + i%26))
		if i%26 == 25 {
			sb.WriteByte(byte('0' + (i/26)%10))
		}
	}
	sb.WriteString(suffix)
	return sb.String()
}

// wrappedLogText joins what a frame's log lines show after their gutter: the
// log's text, as wrapped. No log line is wider than width.
func wrappedLogText(t *testing.T, frame string, width int) string {
	t.Helper()
	var text strings.Builder
	for line := range strings.SplitSeq(stripANSICodes(frame), "\n") {
		at := -1
		for _, gutter := range []string{VertDash3 + " ", VertBoldBar + " "} {
			if i := strings.LastIndex(line, gutter); i >= 0 && i+len(gutter) > at {
				at = i + len(gutter)
			}
		}
		if at >= 0 {
			require.LessOrEqual(t, lipgloss.Width(line), width, "log line wider than the screen:\n%s", frame)
			text.WriteString(strings.TrimRight(line[at:], " "))
		}
	}
	return text.String()
}

// TestToolArgsWrapToTheirRow covers a tool call's raw arguments, shown at
// verbosity 2: written before anything shows them, they wrap at the width
// their row leaves them, not the screen's, losing nothing at each wrap --
// collapsed, expanded, zoomed in and back out, nested in a Timeout, and in
// the final report.
func TestToolArgsWrapToTheirRow(t *testing.T) {
	const width = 100
	l := newLiveToolCalls(t, termenv.Ascii)
	l.fe.FrontendOpts.Verbosity = toolArgsVerbosity
	runArgs := longToolArgs(`{"command":"`, `"}`)
	timeoutArgs := longToolArgs(`{"tool":"Run","duration":"5m","arguments":{"command":"`, `"}}`)
	nestedArgs := longToolArgs(`{"command":"`, `!"}`)

	l.toolCall(liveTimeout, liveReply, "Timeout")
	l.stream(liveTimeout, timeoutArgs, len(timeoutArgs), nil)
	l.closeArgs(liveTimeout)
	l.toolCall(liveNested, liveTimeout, "Run")
	l.stream(liveNested, nestedArgs, len(nestedArgs), nil)
	l.closeArgs(liveNested)
	const liveRun = liveWork
	l.toolCall(liveRun, liveReply, "Run")
	l.stream(liveRun, runArgs, len(runArgs), nil)
	l.closeArgs(liveRun)

	shows := func(what string, args ...string) {
		t.Helper()
		l.fe.viewDirty = true
		frame := l.frame()
		got := wrappedLogText(t, frame, width)
		for _, a := range args {
			require.Contains(t, got, a, "%s: arguments lost text:\n%s", what, stripANSICodes(frame))
		}
	}
	shows("collapsed", timeoutArgs, runArgs)

	l.fe.setExpanded(prettyTestSpanID(liveTimeout), true)
	l.fe.setExpanded(prettyTestSpanID(liveRun), true)
	shows("expanded", timeoutArgs, nestedArgs, runArgs)

	// Zoomed in, the Timeout is the title; the call it wraps is its row.
	l.fe.ZoomedSpan = prettyTestSpanID(liveTimeout)
	shows("zoomed", nestedArgs)

	l.fe.ZoomedSpan = l.fe.db.PrimarySpan
	shows("zoomed back out", timeoutArgs, nestedArgs, runArgs)

	r := newRenderer(l.fe.db, 0, l.fe.FrontendOpts, true)
	report := strings.Join(l.fe.conversationReport(tuist.Context{Width: width}, r, false), "\n")
	got := wrappedLogText(t, report, width)
	for _, a := range []string{timeoutArgs, runArgs} {
		require.Contains(t, got, a, "report: arguments lost text:\n%s", stripANSICodes(report))
	}
}
