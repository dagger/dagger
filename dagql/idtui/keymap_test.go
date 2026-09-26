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
// per line when two columns don't fit, descriptions lined up past the widest
// key, and leaves out disabled keys like the bar does.
func TestRenderKeymapLinesAlignsDescriptions(t *testing.T) {
	lines := RenderKeymapLines(lipgloss.NewStyle(), []key.Binding{
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "nav mode")),
		key.NewBinding(key.WithKeys("ctrl+h"), key.WithHelp("ctrl+h", "toggle hud")),
		key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "go to error"), key.WithDisabled()),
	}, "", time.Time{}, 0)
	var plain []string
	for _, line := range lines {
		plain = append(plain, ansi.Strip(line))
	}
	want := []string{"esc     nav mode", "ctrl+h  toggle hud"}
	if strings.Join(plain, "\n") != strings.Join(want, "\n") {
		t.Fatalf("keymap lines = %q, want %q", plain, want)
	}
}

// TestRenderKeymapLinesTwoColumns: given room, the keymap bubble fills a left
// column top to bottom and continues in a right one, each column aligning its
// own keys; a label too long for two columns falls back to one.
func TestRenderKeymapLinesTwoColumns(t *testing.T) {
	keys := []key.Binding{
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "nav mode")),
		key.NewBinding(key.WithKeys("ctrl+h"), key.WithHelp("ctrl+h", "toggle hud")),
		key.NewBinding(key.WithKeys("!"), key.WithHelp("!", "run shell")),
		key.NewBinding(key.WithKeys("ctrl+t"), key.WithHelp("ctrl+t", "context")),
		key.NewBinding(key.WithKeys("ctrl+?"), key.WithHelp("ctrl+?", "toggle keymap")),
	}
	render := func(keys []key.Binding, width int) []string {
		var plain []string
		for _, line := range RenderKeymapLines(lipgloss.NewStyle(), keys, "", time.Time{}, width) {
			plain = append(plain, ansi.Strip(line))
		}
		return plain
	}
	want := []string{
		"esc     nav mode     ctrl+t  context",
		"ctrl+h  toggle hud   ctrl+?  toggle keymap",
		"!       run shell",
	}
	got := render(keys, 46)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("two-column keymap =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, line := range got {
		if w := ansi.StringWidth(line); w > 46 {
			t.Fatalf("two-column line is %d wide, past 46: %q", w, line)
		}
	}

	long := append(keys, key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "a description much too long to share")))
	if got := render(long, 46); len(got) != len(long) {
		t.Fatalf("a label too long for two columns must fall back to one:\n%s", strings.Join(got, "\n"))
	}
}
