package idtui

import (
	"io"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/bubbles/key"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/muesli/termenv"
	"github.com/vito/tuist"
)

// TestNotificationTopBorderWidth guards against a width miscalculation in the
// notification bubble's top border. When the title + keymap nearly fill the
// box, the fill-bar math used to emit one bar too many, so the assembled top
// border was wider than the box. The overlay compositor then truncated the
// overflow, dropping the closing "╮" corner and making the box top look
// shifted/broken (visible at common 80/100/120-column widths). The top border
// must always be exactly the box width and keep both corners.
func TestNotificationTopBorderWidth(t *testing.T) {
	term := tuist.NewHeadlessTerminal(120, 20)
	fe := newWithTerminal(io.Discard, dagui.NewDB(), term)
	fe.profile = termenv.ANSI

	n := newNotificationBubble(fe, SidebarSection{
		Title: "Changes",
		KeyMap: []key.Binding{
			key.NewBinding(key.WithKeys("ctrl+s"), key.WithHelp("ctrl+s", "save")),
			key.NewBinding(key.WithKeys("ctrl+u"), key.WithHelp("ctrl+u", "reset")),
		},
	})

	// notificationWidth ranges roughly 30..50; exercise inner widths that span
	// "keymap overflows", "keymap exactly fills", and "keymap fits with room".
	for innerWidth := 20; innerWidth <= 60; innerWidth++ {
		top := n.buildTopBorder(fe.profile, termenv.ANSIBrightBlack, innerWidth)

		if w := tuist.VisibleWidth(top); w != innerWidth+2 {
			t.Errorf("innerWidth=%d: top border visible width = %d, want %d: %q",
				innerWidth, w, innerWidth+2, stripANSICodes(top))
		}

		plain := []rune(stripANSICodes(top))
		if len(plain) == 0 || plain[0] != []rune(CornerTopLeft)[0] {
			t.Errorf("innerWidth=%d: top border missing left corner: %q", innerWidth, string(plain))
		}
		if len(plain) == 0 || plain[len(plain)-1] != []rune(CornerTopRight)[0] {
			t.Errorf("innerWidth=%d: top border missing right corner: %q", innerWidth, string(plain))
		}
	}
}

// fixedWidthHost renders a single child at an exact width, standing in for the
// overlay that hosts notification bubbles in the real frontend (which sizes
// them with tuist.SizeAbs(notificationWidth(...))).
type fixedWidthHost struct {
	tuist.Compo

	width int
	child tuist.Component
}

func (h *fixedWidthHost) Render(ctx tuist.Context) {
	h.RenderChild(ctx.Resize(h.width, ctx.Height), h.child)
}

// renderComponentLines renders c at an exact width and returns the raw lines it
// emitted via ctx.Line — i.e. exactly the slice entries the framework treats as
// one terminal row each.
func renderComponentLines(t *testing.T, c tuist.Component, width int) []string {
	t.Helper()
	tui := tuist.New(tuist.NewHeadlessTerminal(width, 40))
	tui.AddChild(&fixedWidthHost{width: width, child: c})
	return tui.RenderLines()
}

// TestNotificationBubbleOverlongContent guards the invariant tuist relies on:
// every string handed to ctx.Line is exactly ONE terminal row — no embedded
// newlines, and no more visible columns than the box is wide.
//
// The bubble pads content with lipgloss.NewStyle().Width(innerWidth).Render(),
// and lipgloss Width does not truncate: it calls Wrap (see charm.land/
// lipgloss/v2 style.go "Word wrap" -> lipgloss.Wrap -> ansi.Wrap), which
// word-wraps and hard-breaks words longer than the width. So an over-long
// content line — e.g. a filesystem path with no whitespace, which sidebar
// producers such as updateReferencesPreview emit without honouring the width
// they are given — turns one ctx.Line entry into a multi-row string, and the
// framework's line accounting (and the overlay compositor) corrupt the screen.
func TestNotificationBubbleOverlongContent(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	const bubbleWidth = 40

	// A realistic "References" entry: long, and with no whitespace to wrap on.
	longPath := "sdk/typescript/runtime/node_modules/typescript/lib/lib.es2015.symbol.wellknown.d.ts"

	term := tuist.NewHeadlessTerminal(120, 40)
	fe := newWithTerminal(io.Discard, dagui.NewDB(), term)

	var gotWidth int
	bubble := newNotificationBubble(fe, SidebarSection{
		Title: "References",
		ContentFunc: func(width int) string {
			// Deliberately ignore width, exactly like updateReferencesPreview.
			gotWidth = width
			return longPath
		},
	})

	lines := renderComponentLines(t, bubble, bubbleWidth)
	if gotWidth <= 0 {
		t.Fatalf("ContentFunc was not called with a usable width (got %d)", gotWidth)
	}

	for i, line := range lines {
		if strings.Contains(line, "\n") {
			t.Errorf("line %d contains embedded newline(s): tuist treats each "+
				"ctx.Line entry as exactly one row, so this desynchronizes line "+
				"accounting.\nrows within the entry: %q",
				i, strings.Split(stripANSICodes(line), "\n"))
		}
		if w := tuist.VisibleWidth(line); w > bubbleWidth {
			t.Errorf("line %d visible width = %d, want <= %d: %q",
				i, w, bubbleWidth, stripANSICodes(line))
		}
	}

	// The box must be exactly what it looks like: top border, one row per
	// content line, bottom border — and the number of slice entries must match
	// the number of physical rows the terminal will actually show.
	physicalRows := 0
	for _, line := range lines {
		physicalRows += strings.Count(line, "\n") + 1
	}
	if physicalRows != len(lines) {
		t.Errorf("emitted %d ctx.Line entries but they occupy %d terminal rows",
			len(lines), physicalRows)
	}
	if want := 3; len(lines) != want {
		t.Errorf("emitted %d lines, want %d (top border, 1 content row, bottom border)",
			len(lines), want)
	}
}

// TestNotificationLipglossWidthWraps documents the upstream behavior the bubble
// depends on: charm.land/lipgloss/v2 Style.Width(n) pads short input but WRAPS
// long input (hard-breaking unbreakable words), it never truncates.
func TestNotificationLipglossWidthWraps(t *testing.T) {
	style := lipgloss.NewStyle().Width(10)

	if got := style.Render("hi"); got != "hi        " {
		t.Errorf("short input: Render(%q) = %q, want padding to width 10", "hi", got)
	}

	for _, input := range []string{
		"one two three four five",       // wraps on spaces
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaa", // no spaces: hard-wrapped
		"a/very/long/path/with/no/spaces/at/all.txt",
	} {
		got := style.Render(input)
		if !strings.Contains(got, "\n") {
			t.Errorf("Render(%q) = %q; expected wrapping (newlines), which is "+
				"the behavior notification.go must defend against", input, got)
		}
	}
}
