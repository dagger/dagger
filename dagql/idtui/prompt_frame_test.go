package idtui

import (
	"image/color"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/cellbuf"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/require"
	"github.com/vito/tuist"
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

	require.Len(t, result.Lines, 4)
	requirePromptBackground(t, result.Lines, width)
	require.Equal(t, strings.Repeat(" ", width), ansi.Strip(result.Lines[0]))
	require.Equal(t, strings.Repeat(" ", width), ansi.Strip(result.Lines[3]))
	require.Equal(t, "  hello there", strings.TrimRight(ansi.Strip(result.Lines[1]), " "))
	require.Equal(t, "  second line", strings.TrimRight(ansi.Strip(result.Lines[2]), " "))
	require.Equal(t, &tuist.CursorPos{Row: 2, Col: 13}, result.Cursor)
	require.Equal(t, 2, frame.ChromeHeight())
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
	require.Len(t, capture.result.Lines, 4)
	require.Equal(t, "  abcdefgh  ", ansi.Strip(capture.result.Lines[1]))
	require.Equal(t, "  ijklmnop  ", ansi.Strip(capture.result.Lines[2]))
	require.Equal(t, &tuist.CursorPos{Row: 2, Col: 10}, capture.result.Cursor)
	requirePromptBackground(t, capture.result.Lines, 12)

	term.Resize(24, 20)
	tui.RenderOnce()
	require.Len(t, capture.result.Lines, 3)
	require.Equal(t, &tuist.CursorPos{Row: 1, Col: 18}, capture.result.Cursor)
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

	require.Len(t, result.Lines, 4)
	require.Equal(t, 3, frame.ChromeHeight())
	require.Contains(t, ansi.Strip(result.Lines[1]), "  hello world")
	require.Contains(t, ansi.Strip(result.Lines[2]), "  [image 1: image/png, 1 KiB]")
	require.NotContains(t, strings.Join(result.Lines, "\n"), "secret")
	requirePromptBackground(t, result.Lines, 40)
	require.Equal(t, &tuist.CursorPos{Row: 1, Col: 7}, result.Cursor)
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
		require.Len(t, result.Lines, 3)
		requirePromptBackground(t, result.Lines, 16)
		require.Equal(t, "  "+value+strings.Repeat(" ", 14-ansi.StringWidth(value)), ansi.Strip(result.Lines[1]))
		require.Equal(t, &tuist.CursorPos{Row: 1, Col: 2 + ansi.StringWidth(value)}, result.Cursor)
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
		for _, line := range result.Lines {
			require.NotContains(t, line, "\x1b")
			require.Equal(t, width, ansi.StringWidth(line))
		}
		require.NotNil(t, result.Cursor)
		require.Less(t, result.Cursor.Col, width)
		require.GreaterOrEqual(t, result.Cursor.Col, 0)
	}
}
