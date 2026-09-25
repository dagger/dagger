package idtui

import (
	"fmt"
	"image/color"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/cellbuf"
	"github.com/muesli/termenv"
	"github.com/vito/tuist"
)

// PromptFrame wraps the prompt TextInput in the same full-width shaded card as
// a submitted user message, separated from prior output by a blank line, with
// blank padding rows and a two-space indent. While the input has keyboard
// focus, the first line's indent carries the focus cue ("❯ ") instead -- the
// same cue a focused transcript row shows -- so it's obvious where typing goes.
// Cursor positioning and key handling stay owned by the TextInput; the frame
// reserves the horizontal padding before rendering and translates its cursor.
type PromptFrame struct {
	tuist.Compo
	input      *tuist.TextInput
	profile    termenv.Profile
	keyHandler func(tuist.Context, uv.KeyPressEvent) bool
	// enabled gates the shaded styling. When false the input is rendered bare,
	// matching plain shell mode.
	enabled     bool
	background  color.Color
	attachments []string
	// isFocused reports whether a component owns the keyboard (see
	// SetFocusSource).
	isFocused func(tuist.Component) bool
}

// SetFocusSource sets how the frame asks whether its input is focused,
// normally the owning TUI's IsFocused. The input re-renders when its focus
// changes, and that marks this frame dirty too, so the cue follows focus
// without anything else reporting it. Without a source the cue never shows.
func (p *PromptFrame) SetFocusSource(isFocused func(tuist.Component) bool) {
	p.isFocused = isFocused
	p.Update()
}

// SetAttachments shows payload-free labels, not clipboard bytes, in the draft.
func (p *PromptFrame) SetAttachments(images []PromptImage, pasting bool) {
	var labels []string
	for i, image := range images {
		labels = append(labels, fmt.Sprintf("[image %d: %s, %d KiB]", i+1, image.MIMEType, (len(image.Data)+1023)/1024))
	}
	if pasting {
		labels = append(labels, "Reading clipboard image…")
	}
	if slices.Equal(labels, p.attachments) {
		return
	}
	p.attachments = labels
	p.Update()
}

// NewPromptFrame creates a PromptFrame wrapping the given TextInput.
func NewPromptFrame(input *tuist.TextInput, profile termenv.Profile) *PromptFrame {
	return &PromptFrame{input: input, profile: profile}
}

// SetBackground sets the theme-relative fill. Nil leaves the terminal's default
// background untouched when color detection is unavailable.
func (p *PromptFrame) SetBackground(background color.Color) {
	p.background = background
	p.Update()
}

// SetKeyHandler sets the handler for keys that bubble out of the wrapped input.
func (p *PromptFrame) SetKeyHandler(handler func(tuist.Context, uv.KeyPressEvent) bool) {
	p.keyHandler = handler
}

// HandleKeyPress implements tuist.Interactive. The focused TextInput receives
// each key first, so this only delegates keys that the editor did not consume.
func (p *PromptFrame) HandleKeyPress(ctx tuist.Context, ev uv.KeyPressEvent) bool {
	if p.keyHandler == nil {
		return false
	}
	return p.keyHandler(ctx, ev)
}

// ChromeHeight is the number of lines the frame adds around the text input.
func (p *PromptFrame) ChromeHeight() int {
	if p.enabled {
		return 3 + len(p.attachments)
	}
	return len(p.attachments)
}

// SetEnabled toggles the framed styling on or off.
func (p *PromptFrame) SetEnabled(enabled bool) {
	if p.enabled == enabled {
		return
	}
	p.enabled = enabled
	p.Update()
}

func (p *PromptFrame) Render(ctx tuist.Context) {
	if p.input == nil {
		return
	}

	childCtx := ctx
	indent := 0
	if p.enabled {
		indent = 2
		if ctx.Width > 0 {
			indent = min(indent, ctx.Width-1)
			// Leave room on the right too, including the cursor at end of line.
			childCtx = ctx.Resize(max(1, ctx.Width-indent-2), ctx.Height)
		}
	}
	result := p.RenderChildResult(childCtx, p.input)
	lines := append([]string(nil), result.Lines...)
	out := NewOutput(new(strings.Builder), termenv.WithProfile(p.profile))
	for _, label := range p.attachments {
		if childCtx.Width > 0 {
			label = ansi.Truncate(label, childCtx.Width, "…")
		}
		lines = append(lines, out.String(label).Foreground(termenv.ANSICyan).String())
	}

	if !p.enabled {
		ctx.Lines(lines...)
		if result.Cursor != nil {
			ctx.SetCursor(result.Cursor.Row, result.Cursor.Col)
		}
		return
	}

	width := ctx.Width
	if width <= 0 {
		for _, line := range lines {
			width = max(width, lipgloss.Width(line)+2*indent)
		}
	}

	// Apply the background to cells rather than wrapping the ANSI string:
	// embedded style resets in highlights, hints, and attachment labels must
	// not punch holes in the card. Preserve their foreground and attributes.
	shade := func(line string) string {
		line = padANSI(ansi.Truncate(line, width, ""), width)
		if p.profile == termenv.Ascii {
			return ansi.Strip(line)
		}
		if p.background == nil {
			return line
		}
		buf := cellbuf.NewBuffer(width, 1)
		cellbuf.SetContent(buf, line)
		for x := range width {
			if cell := buf.Cell(x, 0); cell != nil && cell.Width > 0 {
				cell = cell.Clone()
				cell.Style.Bg = p.background
				buf.SetCell(x, 0, cell)
			}
		}
		_, line = cellbuf.RenderLine(buf, 0)
		return line
	}
	ctx.Line("") // separate the draft from the transcript without extending its fill
	ctx.Line(shade(""))
	gutter := strings.Repeat(" ", indent)
	focused := p.isFocused != nil && p.isFocused(p.input)
	for i, line := range lines {
		prefix := gutter
		if i == 0 && focused && indent > 0 {
			// Same width as the indent it replaces, so wrapping and the cursor
			// column are unaffected.
			prefix = out.String(LLMPrompt).Bold().String() + gutter[1:]
		}
		ctx.Line(shade(prefix + line))
	}
	ctx.Line(shade(""))

	if result.Cursor != nil {
		ctx.SetCursor(result.Cursor.Row+2, min(result.Cursor.Col+indent, max(0, width-1)))
	}
}
