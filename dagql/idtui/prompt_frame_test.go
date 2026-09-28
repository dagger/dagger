package idtui

import (
	"context"
	"image/color"
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/cellbuf"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/require"
	"github.com/vito/tuist"

	"github.com/dagger/dagger/dagql/dagui"
)

// Capture the frame's translated cursor as well as its rendered lines.
type promptFrameCapture struct {
	tuist.Compo
	frame  *PromptFrame
	result tuist.RenderResult
}

func (c *promptFrameCapture) Render(ctx tuist.Context) {
	c.result = c.RenderChildResult(ctx, c.frame)
	ctx.Lines(c.result.Lines...)
	if c.result.Cursor != nil {
		ctx.SetCursor(c.result.Cursor.Row, c.result.Cursor.Col)
	}
}

func renderPromptFrame(frame *PromptFrame, width int) tuist.RenderResult {
	capture := &promptFrameCapture{frame: frame}
	tui := tuist.New(tuist.NewHeadlessTerminal(width, 20))
	tui.AddChild(capture)
	tui.SetFocus(frame.input)
	tui.RenderOnce()
	return capture.result
}

func requirePromptBackground(t *testing.T, lines []string, width int) {
	t.Helper()
	require.NotEmpty(t, lines)
	require.Equal(t, "", lines[0], "unshaded separator above the prompt")
	lines = lines[1:]
	joined := strings.Join(lines, "\n")
	require.NotContains(t, joined, HorizBar)
	require.Contains(t, joined, "\x1b[48;2;30;30;30m")
	buf := cellbuf.NewBuffer(width, len(lines))
	cellbuf.SetContent(buf, joined)
	bg := buf.Cell(0, 0).Style.Bg
	require.NotNil(t, bg)
	for y, line := range lines {
		require.Equal(t, width, ansi.StringWidth(line), "row %d", y)
		for x := range width {
			cell := buf.Cell(x, y)
			require.NotNil(t, cell)
			if cell.Width > 0 { // wide-cell continuations inherit the leading cell
				require.Equal(t, bg, cell.Style.Bg, "background at row %d col %d", y, x)
			}
		}
	}
}

func TestPromptFrameRendersShadedInput(t *testing.T) {
	const width = 40
	input := tuist.NewTextInput("")
	input.SetValue("hello there\nsecond line")
	frame := NewPromptFrame(input, termenv.ANSI)
	frame.SetEnabled(true)
	frame.SetBackground(blendPromptBackground(color.Black, termenv.TrueColor).cell)
	result := renderPromptFrame(frame, width)

	require.Len(t, result.Lines, 5)
	requirePromptBackground(t, result.Lines, width)
	require.Equal(t, strings.Repeat(" ", width), ansi.Strip(result.Lines[1]))
	require.Equal(t, strings.Repeat(" ", width), ansi.Strip(result.Lines[4]))
	require.Equal(t, "  hello there", strings.TrimRight(ansi.Strip(result.Lines[2]), " "))
	require.Equal(t, "  second line", strings.TrimRight(ansi.Strip(result.Lines[3]), " "))
	require.Equal(t, &tuist.CursorPos{Row: 3, Col: 13}, result.Cursor)
	require.Equal(t, 3, frame.ChromeHeight())
}

// TestPromptFrameSoftBorder: with a border color, the card's padding rows
// double as its edges -- thin scan-line rules along the top of the first and the
// bottom of the last, in the soft border color over the card's own fill -- so
// the card is outlined without growing.
func TestPromptFrameSoftBorder(t *testing.T) {
	const width = 40
	shade := blendPromptBackground(color.Black, termenv.TrueColor)
	input := tuist.NewTextInput("")
	input.SetValue("hello there")
	frame := NewPromptFrame(input, termenv.ANSI)
	frame.SetEnabled(true)
	frame.SetBackground(shade.cell)
	frame.SetBorder(shade.border)
	result := renderPromptFrame(frame, width)

	require.Len(t, result.Lines, 4)
	requirePromptBackground(t, result.Lines, width)
	require.Equal(t, 3, frame.ChromeHeight())
	for _, edge := range []struct {
		row   int
		glyph string
	}{{1, promptTopEdge}, {3, promptBottomEdge}} {
		require.Equal(t, strings.Repeat(edge.glyph, width), ansi.Strip(result.Lines[edge.row]))
		buf := cellbuf.NewBuffer(width, 1)
		cellbuf.SetContent(buf, result.Lines[edge.row])
		for x := range width {
			require.Equal(t, shade.border, buf.Cell(x, 0).Style.Fg, "edge row %d col %d", edge.row, x)
		}
	}
	require.Equal(t, "  hello there", strings.TrimRight(ansi.Strip(result.Lines[2]), " "))
}

// TestPromptFrameOpensOverFocusedTab: the card's bottom edge leaves a gap over
// the focused agent's tab on the line beneath, so the tab reads as hanging off
// the card; the gap keeps the card's fill.
func TestPromptFrameOpensOverFocusedTab(t *testing.T) {
	const width = 40
	shade := blendPromptBackground(color.Black, termenv.TrueColor)
	input := tuist.NewTextInput("")
	frame := NewPromptFrame(input, termenv.ANSI)
	frame.SetEnabled(true)
	frame.SetBackground(shade.cell)
	frame.SetBorder(shade.border)
	frame.SetTabSource(func(int) (int, int, bool) { return 11, 24, true })
	result := renderPromptFrame(frame, width)

	requirePromptBackground(t, result.Lines, width)
	bottom := result.Lines[len(result.Lines)-1]
	require.Equal(t,
		strings.Repeat(promptBottomEdge, 11)+strings.Repeat(" ", 13)+strings.Repeat(promptBottomEdge, width-24),
		ansi.Strip(bottom))
	require.Equal(t, strings.Repeat(promptTopEdge, width), ansi.Strip(result.Lines[1]))
}

// TestPromptFrameFocusCue verifies the focused input swaps its first line's
// two-space indent for the "❯ " focus cue -- same width, so wrapping and the
// cursor column don't move -- and reverts to the indent when focus leaves. The
// frame asks the TUI during Render, so this also relies on a focus change
// re-rendering the (otherwise cached) frame.
func TestPromptFrameFocusCue(t *testing.T) {
	newFrame := func(width int) (*PromptFrame, *promptFrameCapture, *tuist.TUI) {
		input := tuist.NewTextInput("")
		input.SetValue("hello there\nsecond line")
		frame := NewPromptFrame(input, termenv.ANSI)
		frame.SetEnabled(true)
		frame.SetBackground(blendPromptBackground(color.Black, termenv.TrueColor).cell)
		capture := &promptFrameCapture{frame: frame}
		tui := tuist.New(tuist.NewHeadlessTerminal(width, 20))
		frame.SetFocusSource(tui.IsFocused)
		tui.AddChild(capture)
		tui.SetFocus(input)
		tui.RenderOnce()
		return frame, capture, tui
	}

	const width = 40
	frame, capture, tui := newFrame(width)
	result := capture.result
	require.Len(t, result.Lines, 5)
	requirePromptBackground(t, result.Lines, width)
	require.Equal(t, LLMPrompt+" hello there", strings.TrimRight(ansi.Strip(result.Lines[2]), " "))
	require.Equal(t, "  second line", strings.TrimRight(ansi.Strip(result.Lines[3]), " "),
		"continuation lines keep the plain indent")
	require.Equal(t, &tuist.CursorPos{Row: 3, Col: 13}, result.Cursor)

	tui.SetFocus(nil)
	tui.RenderOnce()
	require.Equal(t, "  hello there", strings.TrimRight(ansi.Strip(capture.result.Lines[2]), " "),
		"the cue must leave with focus")

	tui.SetFocus(frame.input)
	tui.RenderOnce()
	require.Equal(t, LLMPrompt+" hello there", strings.TrimRight(ansi.Strip(capture.result.Lines[2]), " "),
		"the cue must return with focus")

	// Too narrow for the full indent: the cue takes the one cell there is.
	_, capture, _ = newFrame(2)
	require.True(t, strings.HasPrefix(ansi.Strip(capture.result.Lines[2]), LLMPrompt), ansi.Strip(capture.result.Lines[2]))
}

// TestPromptFocusCueFollowsInputFocus drives the frontend: the draft shows the
// focus cue while the input owns the keyboard, drops it when focus moves to
// transcript navigation, and gets it back on returning to the input.
func TestPromptFocusCueFollowsInputFocus(t *testing.T) {
	fe := newWithTerminalProfile(io.Discard, dagui.NewDB(), tuist.NewHeadlessTerminal(60, 20), termenv.ANSI)
	fe.setupTUI()
	fe.startShell(context.Background(), &imagePromptHandler{mode: true})
	fe.textInput.SetValue("draft")
	draftLine := func() string {
		t.Helper()
		fe.tui.Step()
		for _, line := range fe.tui.Frame() {
			if plain := ansi.Strip(line); strings.Contains(plain, "draft") {
				return strings.TrimRight(plain, " ")
			}
		}
		t.Fatalf("draft not rendered:\n%s", strings.Join(fe.tui.Frame(), "\n"))
		return ""
	}

	// stubShellHandler's own prompt ("⋈ ") follows the frame's gutter.
	require.Equal(t, LLMPrompt+" ⋈ draft", draftLine(), "input focused on shell start")

	fe.focusNavigationTarget()
	require.Equal(t, "  ⋈ draft", draftLine(), "navigating the transcript")

	fe.enterInsertMode()
	require.Equal(t, LLMPrompt+" ⋈ draft", draftLine(), "back in the input")
}

func TestPromptFrameWrapAndResize(t *testing.T) {
	input := tuist.NewTextInput("")
	input.SetValue("abcdefghijklmnop")
	frame := NewPromptFrame(input, termenv.ANSI)
	frame.SetEnabled(true)
	frame.SetBackground(blendPromptBackground(color.Black, termenv.TrueColor).cell)
	capture := &promptFrameCapture{frame: frame}
	term := tuist.NewHeadlessTerminal(12, 20)
	tui := tuist.New(term)
	tui.AddChild(capture)
	tui.SetFocus(input)
	tui.RenderOnce()

	// Reserve two cells on each side before wrapping, not after rendering.
	require.Len(t, capture.result.Lines, 5)
	require.Equal(t, "  abcdefgh  ", ansi.Strip(capture.result.Lines[2]))
	require.Equal(t, "  ijklmnop  ", ansi.Strip(capture.result.Lines[3]))
	require.Equal(t, &tuist.CursorPos{Row: 3, Col: 10}, capture.result.Cursor)
	requirePromptBackground(t, capture.result.Lines, 12)

	term.Resize(24, 20)
	tui.RenderOnce()
	require.Len(t, capture.result.Lines, 4)
	require.Equal(t, &tuist.CursorPos{Row: 2, Col: 18}, capture.result.Cursor)
	requirePromptBackground(t, capture.result.Lines, 24)
}

func TestPromptFrameStyledContentAndAttachments(t *testing.T) {
	input := tuist.NewTextInput("")
	input.SetValue("hello")
	input.Highlight = func(string) []tuist.StyleSpan {
		return []tuist.StyleSpan{{Start: 0, End: 2, Style: func(s string) string {
			return termenv.String(s).Foreground(termenv.ANSICyan).String()
		}}}
	}
	input.Suggestion = "hello world"
	input.SuggestionStyle = func(s string) string { return termenv.String(s).Faint().String() }
	frame := NewPromptFrame(input, termenv.ANSI)
	frame.SetEnabled(true)
	frame.SetBackground(blendPromptBackground(color.Black, termenv.TrueColor).cell)
	frame.SetAttachments([]PromptImage{{MIMEType: "image/png", Data: []byte("secret")}}, false)
	result := renderPromptFrame(frame, 40)

	require.Len(t, result.Lines, 5)
	require.Equal(t, 4, frame.ChromeHeight())
	require.Contains(t, ansi.Strip(result.Lines[2]), "  hello world")
	require.Contains(t, ansi.Strip(result.Lines[3]), "  [image 1: image/png, 1 KiB]")
	require.NotContains(t, strings.Join(result.Lines, "\n"), "secret")
	requirePromptBackground(t, result.Lines, 40)
	require.Equal(t, &tuist.CursorPos{Row: 2, Col: 7}, result.Cursor)
}

func TestPromptFrameDisabledRendersBare(t *testing.T) {
	input := tuist.NewTextInput("⋈ ")
	input.SetValue("ls -la")
	frame := NewPromptFrame(input, termenv.ANSI)
	result := renderPromptFrame(frame, 40)

	require.Equal(t, []string{"⋈ ls -la"}, result.Lines)
	require.Equal(t, &tuist.CursorPos{Row: 0, Col: 8}, result.Cursor)
	require.Zero(t, frame.ChromeHeight())
}

func TestPromptFrameEmptyAndUnicodeInput(t *testing.T) {
	for _, value := range []string{"", "界🙂"} {
		input := tuist.NewTextInput("")
		input.SetValue(value)
		frame := NewPromptFrame(input, termenv.ANSI)
		frame.SetEnabled(true)
		frame.SetBackground(blendPromptBackground(color.Black, termenv.TrueColor).cell)
		result := renderPromptFrame(frame, 16)
		require.Len(t, result.Lines, 4)
		requirePromptBackground(t, result.Lines, 16)
		require.Equal(t, "  "+value+strings.Repeat(" ", 14-ansi.StringWidth(value)), ansi.Strip(result.Lines[2]))
		require.Equal(t, &tuist.CursorPos{Row: 2, Col: 2 + ansi.StringWidth(value)}, result.Cursor)
	}
}

func TestPromptFrameNoColorAndNarrowWidths(t *testing.T) {
	for _, width := range []int{1, 2, 3, 4, 12} {
		input := tuist.NewTextInput("")
		input.SetValue("abcdef")
		frame := NewPromptFrame(input, termenv.Ascii)
		frame.SetEnabled(true)
		frame.SetBackground(blendPromptBackground(color.Black, termenv.TrueColor).cell)
		frame.SetAttachments(nil, true)
		result := renderPromptFrame(frame, width)
		require.Equal(t, "", result.Lines[0])
		for _, line := range result.Lines[1:] {
			require.NotContains(t, line, "\x1b")
			require.Equal(t, width, ansi.StringWidth(line))
		}
		require.NotNil(t, result.Cursor)
		require.Less(t, result.Cursor.Col, width)
		require.GreaterOrEqual(t, result.Cursor.Col, 0)
	}
}
