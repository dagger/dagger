package idtui

import (
	"bytes"
	"fmt"
	"image/color"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/vito/tuist"

	"github.com/dagger/dagger/dagql/dagui"
)

// TestShellExitReprintsLiveTranscript: when an interactive shell (`dagger
// agent`) exits, the final render reprints the conversation exactly as the
// session showed it -- the cue-column indent, padded prompt cards, turn gaps,
// and tool calls nested under their reply -- instead of reflowing it into the
// CONVERSATION report, whose flush first lines and unpadded prompts made the
// transcript visibly jump on exit.
func TestShellExitReprintsLiveTranscript(t *testing.T) {
	db := dagui.NewDB()
	rootID := prettyTestSpanID(1)
	promptID := prettyTestSpanID(2)
	thinkID := prettyTestSpanID(3)
	replyID := prettyTestSpanID(4)
	toolID := prettyTestSpanID(5)
	prompt2ID := prettyTestSpanID(6)
	reply2ID := prettyTestSpanID(7)
	start := time.Unix(100, 0)
	span := func(id, parent dagui.SpanID, name, role string, at int) dagui.SpanSnapshot {
		return dagui.SpanSnapshot{
			ID: id, TraceID: prettyTestTraceID(), Name: name, Message: "received",
			LLMRole: role, ParentID: parent,
			StartTime: start.Add(time.Duration(at) * time.Second),
			EndTime:   start.Add(time.Duration(at+1) * time.Second),
			Final:     true,
		}
	}
	thinking := span(thinkID, rootID, "thinking", "assistant", 3)
	thinking.LLMThinking = true
	tool := span(toolID, replyID, "Find", "assistant", 6)
	tool.Message = ""
	tool.LLMTool = "Find"
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{ID: rootID, TraceID: prettyTestTraceID(), Name: "dagger agent", StartTime: start, EndTime: start.Add(20 * time.Second), Final: true},
		span(promptID, rootID, "LLM prompt", "user", 1),
		thinking,
		span(replyID, rootID, "LLM response", "assistant", 5),
		tool,
		span(prompt2ID, rootID, "LLM prompt", "user", 8),
		span(reply2ID, rootID, "LLM response", "assistant", 10),
	})
	db.SetPrimarySpan(rootID)

	const width = 60
	fe := newWithTerminal(io.Discard, db, tuist.NewHeadlessTerminal(width, 80))
	fe.profile = termenv.ANSI
	fe.logs.Profile = termenv.ANSI
	fe.promptBackground = blendPromptBackground(color.Black, termenv.TrueColor)
	fe.shell = stubShellHandler{}
	fe.ranShell = true
	fe.FrontendOpts.Verbosity = dagui.ShowCompletedVerbosity
	fe.autoFocus = false
	fe.setWindowSizeLocked(windowSize{Width: width, Height: 80})
	setLog := func(id dagui.SpanID, text string) {
		logs := NewVterm(termenv.ANSI)
		logs.SetWidth(width)
		_, _ = logs.WriteMarkdown([]byte(text + "\n"))
		fe.logs.Logs[id] = logs
	}
	setLog(promptID, "hey there - what's your favorite fruit?")
	setLog(thinkID, "just a casual question")
	setLog(replyID, "I don't eat, so I don't have a real favorite. If I had to pick one, I'd go with mango.\n\nWhat's yours?")
	setLog(prompt2ID, "tomato")
	// Taller than a third of the screen: the exit render must not cut the
	// reply down to a log tail window.
	var items []string
	for i := range 30 {
		items = append(items, fmt.Sprintf("- item %d", i))
	}
	setLog(reply2ID, "Tomato's a good pick.\n\n"+strings.Join(items, "\n"))

	fe.recalculateViewLocked()
	live := fe.tui.Frame()

	// stopShell clears the handler before the exit render.
	fe.shell = nil
	var buf bytes.Buffer
	if err := fe.FinalRender(&buf); err != nil {
		t.Fatal(err)
	}
	final := buf.String()

	liveText := strings.Join(live, "\n")
	if !strings.Contains(final, liveText) {
		t.Fatalf("exit render does not reprint the live transcript\nLIVE:\n%s\n\nFINAL:\n%s",
			visibleEscapes(liveText), visibleEscapes(final))
	}
	if strings.Contains(stripANSICodes(final), "CONVERSATION") {
		t.Fatalf("exit render fell back to the CONVERSATION report:\n%s", stripANSICodes(final))
	}
	// The exit render isn't clipped to the terminal like the live frame, so an
	// overlong line would wrap on screen.
	for _, line := range strings.Split(final, "\n") {
		if w := ansi.StringWidth(line); w > width {
			t.Fatalf("exit render line is %d cells wide, over the %d-cell terminal: %q", w, width, visibleEscapes(line))
		}
	}
	// Sanity: the live transcript carries the shell layout being preserved.
	plain := stripANSICodes(liveText)
	for _, want := range []string{"  hey there", "  I don't eat", "    • Find", "  tomato", "item 0", "item 29"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("live transcript missing %q:\n%s", want, plain)
		}
	}
	var pads int
	for _, line := range live {
		if strings.TrimSpace(stripANSICodes(line)) == "" && strings.Contains(line, "\x1b[48;2;") {
			pads++
		}
	}
	if pads != 4 {
		t.Fatalf("want each of the 2 prompt cards padded above and below, got %d shaded pad lines:\n%s",
			pads, visibleEscapes(liveText))
	}
}
