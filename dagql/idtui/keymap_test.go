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

func TestKeymapBarCanHide(t *testing.T) {
	binding := key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "confirm"))
	bar := &KeymapBar{
		Profile: termenv.Ascii,
		Keys: func(*termenv.Output) []key.Binding {
			return []key.Binding{binding}
		},
		Hidden: func() bool { return true },
	}
	tui := tuist.New(tuist.NewHeadlessTerminal(80, 10))
	tui.AddChild(bar)
	if lines := tui.RenderLines(); len(lines) != 0 {
		t.Fatalf("hidden keymap rendered lines: %#v", lines)
	}
}

// TestRenderKeymapLinesAlignsDescriptions: the keymap bubble lists one key
// per line, descriptions lined up past the widest key, and leaves out
// disabled keys like the bar does.
func TestRenderKeymapLinesAlignsDescriptions(t *testing.T) {
	lines := RenderKeymapLines(lipgloss.NewStyle(), []key.Binding{
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "nav mode")),
		key.NewBinding(key.WithKeys("ctrl+h"), key.WithHelp("ctrl+h", "toggle hud")),
		key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "go to error"), key.WithDisabled()),
	}, "", time.Time{})
	var plain []string
	for _, line := range lines {
		plain = append(plain, ansi.Strip(line))
	}
	want := []string{"esc     nav mode", "ctrl+h  toggle hud"}
	if strings.Join(plain, "\n") != strings.Join(want, "\n") {
		t.Fatalf("keymap lines = %q, want %q", plain, want)
	}
}
