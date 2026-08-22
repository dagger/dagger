package idtui

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/x/ansi"
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
