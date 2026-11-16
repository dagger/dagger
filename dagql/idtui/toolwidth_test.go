package idtui

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/muesli/termenv"
	"github.com/vito/tuist"

	"github.com/dagger/dagger/dagql/dagui"
)

// gutteredText joins what a frame's guttered lines (logs, and rollups hung
// off a row's pipe) show after their gutter, whitespace dropped: the text
// they wrapped, wherever it wrapped.
func gutteredText(frame string) string {
	var text strings.Builder
	for line := range strings.SplitSeq(stripANSICodes(frame), "\n") {
		at := -1
		for _, gutter := range []string{VertDash3 + " ", VertBoldBar + " "} {
			if i := strings.LastIndex(line, gutter); i >= 0 && i+len(gutter) > at {
				at = i + len(gutter)
			}
		}
		if at >= 0 {
			text.WriteString(line[at:])
		}
	}
	return withoutSpace(text.String())
}

func withoutSpace(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}

// TestToolOutputFitsItsRow covers a tool call's long single-line output, and
// the logs of a test its CHECKS rollup nests, at depth 1 and 2: whether the
// row is collapsed or expanded, live and in the transcript a shell reprints
// on exit, every line fits the screen and none loses text. The live frame
// is clipped at the screen's edge, so a line laid out too wide loses its
// tail there; the transcript isn't, so the terminal would wrap it.
func TestToolOutputFitsItsRow(t *testing.T) {
	const width = 100
	for _, c := range []struct {
		name           string
		rollup, nested bool
	}{
		{"output", false, false},
		{"output under a rollup", true, false},
		{"nested output", false, true},
		{"nested output under a rollup", true, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			db := dagui.NewDB()
			rootID, promptID, replyID := prettyTestSpanID(1), prettyTestSpanID(2), prettyTestSpanID(3)
			toolID, checkID, testID := prettyTestSpanID(4), prettyTestSpanID(5), prettyTestSpanID(6)
			spawnID, subReplyID := prettyTestSpanID(7), prettyTestSpanID(8)
			start := time.Unix(100, 0)
			span := func(id, parent dagui.SpanID, name string) dagui.SpanSnapshot {
				return dagui.SpanSnapshot{
					ID: id, TraceID: prettyTestTraceID(), Name: name, ParentID: parent,
					StartTime: start, EndTime: start.Add(42 * time.Second), Final: true,
				}
			}
			toolCall := func(id, parent dagui.SpanID, server, tool string) dagui.SpanSnapshot {
				call := span(id, parent, server+"_"+tool)
				call.LLMRole, call.LLMTool, call.LLMToolServer = "assistant", tool, server
				call.Boundary, call.RollUpLogs, call.RollUpSpans = true, true, true
				return call
			}
			message := func(id, parent dagui.SpanID, role string) dagui.SpanSnapshot {
				msg := span(id, parent, "LLM "+role)
				msg.Message, msg.LLMRole = "received", role
				return msg
			}
			snaps := []dagui.SpanSnapshot{
				span(rootID, dagui.SpanID{}, "dagger agent"),
				message(promptID, rootID, "user"),
				message(replyID, rootID, "assistant"),
			}
			parent := replyID
			if c.nested {
				snaps = append(snaps,
					toolCall(spawnID, replyID, "Staff", "spawn"),
					message(subReplyID, spawnID, "assistant"))
				parent = subReplyID
			}
			check := toolCall(toolID, parent, "Editor", "check")
			check.LLMToolArgNames, check.LLMToolArgValues = []string{"include"}, []string{"**"}
			snaps = append(snaps, check)
			if c.rollup {
				goTest := span(checkID, toolID, "go:test")
				goTest.CheckName = "go:test"
				testCase := span(testID, checkID, "TestPartials")
				testCase.TestCaseName, testCase.TestStatus = "TestPartials", dagui.TestStatusFailure
				snaps = append(snaps, goTest, testCase)
			}
			db.ImportSnapshots(snaps)
			db.SetPrimarySpan(rootID)

			fe := newWithTerminal(io.Discard, db, tuist.NewHeadlessTerminal(width, 80))
			fe.profile = termenv.ANSI
			fe.logs.Profile = termenv.ANSI
			fe.shell = stubShellHandler{}
			fe.ranShell = true
			fe.FrontendOpts.Verbosity = dagui.ShowCompletedVerbosity
			fe.autoFocus = false
			fe.setWindowSizeLocked(windowSize{Width: width, Height: 80})
			write := func(id dagui.SpanID, text string) {
				_, _ = io.WriteString(fe.logs.spanLogs(id), text)
			}
			write(promptID, "Check it.")
			write(replyID, "Checking.")
			if c.nested {
				write(subReplyID, "On it.")
				fe.setExpanded(spawnID, true)
			}
			output := longToolArgs("OUTPUT ", " END")
			write(toolID, output+"\n")
			testLog := longToolArgs("TESTLOG ", " TESTEND")
			if c.rollup {
				write(testID, testLog+"\n")
			}

			fits := func(what, frame string, want ...string) {
				t.Helper()
				got := gutteredText(frame)
				for _, w := range want {
					if !strings.Contains(got, withoutSpace(w)) {
						t.Errorf("%s: lost text of %.10q…:\n%s", what, w, stripANSICodes(frame))
					}
				}
				for line := range strings.SplitSeq(frame, "\n") {
					plain := stripANSICodes(line)
					if !strings.Contains(plain, VertBoldBar) && !strings.Contains(plain, VertDash3) {
						continue
					}
					if w := lipgloss.Width(line); w > width {
						t.Errorf("%s: a %d-cell line on a %d-cell screen:\n%s", what, w, width, plain)
					}
				}
			}
			frame := func() string {
				fe.recalculateViewLocked()
				return strings.Join(fe.tui.Frame(), "\n")
			}

			// Collapsed, the CHECKS rollup nests the test's whole log.
			collapsed := []string{output}
			if c.rollup {
				collapsed = append(collapsed, testLog)
			}
			fits("collapsed", frame(), collapsed...)

			// Expanded, the check's own row tails its test's log.
			fe.setExpanded(toolID, true)
			fits("expanded", frame(), output)

			fe.setExpanded(toolID, false)
			fe.shell = nil
			var buf bytes.Buffer
			if err := fe.FinalRender(&buf); err != nil {
				t.Fatal(err)
			}
			fits("exit transcript", buf.String(), collapsed...)
		})
	}
}
