package idtui

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/vito/tuist"
)

func TestRenderKeymapUsesDisplayKey(t *testing.T) {
	binding := key.NewBinding(
		key.WithKeys(" ", "x"),
		key.WithHelp("x", "toggle"),
	)
	var out bytes.Buffer
	RenderKeymap(&out, lipgloss.NewStyle(), []key.Binding{binding}, "", time.Time{})
	if got := ansi.Strip(out.String()); !strings.Contains(got, "x toggle") {
		t.Fatalf("keymap rendered %q, want display key", got)
	}
}

func TestKeymapBarAlwaysHasLeadingBlankLine(t *testing.T) {
	binding := key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "confirm"))
	bar := &KeymapBar{
		Profile: termenv.Ascii,
		Keys: func(*termenv.Output) []key.Binding {
			return []key.Binding{binding}
		},
	}
	tui := tuist.New(tuist.NewHeadlessTerminal(80, 10))
	tui.AddChild(bar)
	lines := tui.RenderLines()
	if len(lines) != 2 || lines[0] != "" || !strings.Contains(ansi.Strip(lines[1]), "enter confirm") {
		t.Fatalf("keymap did not render with one leading blank line: %#v", lines)
	}
}

func TestKeymapBarCanSitSnugAgainstStatusBar(t *testing.T) {
	binding := key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "confirm"))
	bar := &KeymapBar{
		Profile: termenv.Ascii,
		Keys: func(*termenv.Output) []key.Binding {
			return []key.Binding{binding}
		},
		Snug: func() bool { return true },
	}
	tui := tuist.New(tuist.NewHeadlessTerminal(80, 10))
	tui.AddChild(bar)
	lines := tui.RenderLines()
	if len(lines) != 1 || !strings.Contains(ansi.Strip(lines[0]), "enter confirm") {
		t.Fatalf("snug keymap rendered a separating line: %#v", lines)
	}
}
