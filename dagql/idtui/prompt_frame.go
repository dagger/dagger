package idtui

import (
	"strings"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/muesli/termenv"
	"github.com/vito/tuist"
)

// PromptFrame wraps the prompt TextInput in a full-width framed block: a
// horizontal rule above and below the input, which sits flush to the left edge
// with no prompt symbol or background chrome. The rules are faint so the input
// reads as an inset region without shouting.
//
// The frame renders the wrapped TextInput itself, translating its cursor down
// by one line to account for the top rule, so cursor positioning and key
// handling stay entirely owned by the TextInput.
type PromptFrame struct {
	tuist.Compo
	input      *tuist.TextInput
	profile    termenv.Profile
	keyHandler func(tuist.Context, uv.KeyPressEvent) bool
	// enabled gates the framed styling. When false the input is rendered bare
	// (no rules), matching plain shell mode.
	enabled bool
}

// NewPromptFrame creates a PromptFrame wrapping the given TextInput.
func NewPromptFrame(input *tuist.TextInput, profile termenv.Profile) *PromptFrame {
	return &PromptFrame{input: input, profile: profile}
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
		return 2
	}
	return 0
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

	result := p.RenderChildResult(ctx, p.input)

	if !p.enabled {
		ctx.Lines(result.Lines...)
		if result.Cursor != nil {
			ctx.SetCursor(result.Cursor.Row, result.Cursor.Col)
		}
		return
	}

	width := ctx.Width
	if width <= 0 {
		for _, line := range result.Lines {
			width = max(width, lipgloss.Width(line))
		}
	}

	out := NewOutput(new(strings.Builder), termenv.WithProfile(p.profile))
	// The rules read as faint bright-black dashes spanning the full width,
	// framing the flush input without any background.
	styleBar := func(bar string) string {
		return out.String(bar).
			Foreground(termenv.ANSIBrightBlack).
			Faint().
			String()
	}
	bar := styleBar(strings.Repeat(HorizBar, max(width, 0)))

	lines := make([]string, 0, len(result.Lines)+2)
	lines = append(lines, bar)
	lines = append(lines, result.Lines...)
	lines = append(lines, bar)
	ctx.Lines(lines...)

	// Offset the cursor by one row to account for the top rule.
	if result.Cursor != nil {
		ctx.SetCursor(result.Cursor.Row+1, result.Cursor.Col)
	}
}
