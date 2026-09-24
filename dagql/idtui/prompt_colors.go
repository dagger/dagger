package idtui

import (
	"fmt"
	"image/color"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/vito/tuist"
)

// promptBackground keeps the same negotiated color in both rendering systems.
// Its zero value leaves the terminal background untouched.
type promptBackground struct {
	cell color.Color
	term termenv.Color
}

// Keep integer RGB channels intact across both renderers. termenv.RGBColor's
// floating-point conversion can truncate a channel by one when emitting SGR.
type promptRGBColor color.RGBA

func (c promptRGBColor) Sequence(background bool) string {
	prefix := 38
	if background {
		prefix = 48
	}
	return fmt.Sprintf("%d;2;%d;%d;%d", prefix, c.R, c.G, c.B)
}

// blendPromptBackground follows Codex's composer fill: 12% white over a dark
// terminal background, or 4% black over a light one. The remaining colors stay
// in the user's ANSI palette; only this measured, theme-relative fill uses RGB
// (or the nearest 256-color shade).
func blendPromptBackground(bg color.Color, profile termenv.Profile) promptBackground {
	if bg == nil || (profile != termenv.TrueColor && profile != termenv.ANSI256) {
		return promptBackground{}
	}
	r, g, b, _ := bg.RGBA()
	r, g, b = r>>8, g>>8, b>>8
	top, alpha := uint32(255), uint32(12)
	if 299*r+587*g+114*b > 128000 {
		top, alpha = 0, 4
	}
	blend := func(channel uint32) uint8 {
		return uint8((channel*(100-alpha) + top*alpha) / 100)
	}
	fill := color.RGBA{R: blend(r), G: blend(g), B: blend(b), A: 255}
	result := promptBackground{cell: fill, term: promptRGBColor(fill)}
	if profile == termenv.ANSI256 {
		result.term = profile.FromColor(fill)
		result.cell = ansi.IndexedColor(result.term.(termenv.ANSI256Color))
	}
	return result
}

// promptColorTerminal queries only after the terminal's input reader is running.
// Tuist's existing reader decodes the response; there is no second stdin reader,
// blocking probe, or timeout to delay startup. Unsupported terminals simply
// never answer, leaving the background unset. Start also re-queries on resume.
type promptColorTerminal struct {
	tuist.Terminal
}

func (t *promptColorTerminal) Start(onInput func([]byte), onResize func()) error {
	if err := t.Terminal.Start(onInput, onResize); err != nil {
		return err
	}
	t.Terminal.WriteString(ansi.RequestBackgroundColor)
	return nil
}

// handlePromptBackground runs on Tuist's event loop, before input dispatch, so
// OSC replies cannot become editor text. It also accepts later color reports.
func (fe *frontendPretty) handlePromptBackground(_ tuist.Context, event uv.Event) bool {
	reply, ok := event.(uv.BackgroundColorEvent)
	if !ok {
		return false
	}
	if reply.Color == nil || fe.profile == termenv.Ascii {
		return true
	}
	background := blendPromptBackground(reply.Color, fe.promptColorProfile)
	if background == fe.promptBackground {
		return true
	}
	fe.promptBackground = background
	if fe.promptFrame != nil {
		fe.promptFrame.SetBackground(background.cell)
	}
	// Transcript rows and their logs cache role styling independently.
	for _, tree := range fe.spanTrees {
		tree.Update()
	}
	for _, logs := range fe.logsViews {
		logs.Update()
	}
	fe.Update()
	return true
}
