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
// a submitted user message, with blank padding rows and a two-space indent.
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
		return 2 + len(p.attachments)
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
	ctx.Line(shade(""))
	for _, line := range lines {
		ctx.Line(shade(strings.Repeat(" ", indent) + line))
	}
	ctx.Line(shade(""))

	if result.Cursor != nil {
		ctx.SetCursor(result.Cursor.Row+1, min(result.Cursor.Col+indent, max(0, width-1)))
	}
}
