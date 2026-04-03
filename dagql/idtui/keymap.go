package idtui

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/muesli/termenv"
	"github.com/vito/tuist"

	"charm.land/lipgloss/v2"
)

// KeymapStyle is the default style for keymap text.
var KeymapStyle = lipgloss.NewStyle().
	Foreground(lipgloss.BrightBlack)

const keypressDuration = 500 * time.Millisecond

// KeymapBar is a component that renders a horizontal key binding bar.
type KeymapBar struct {
	tuist.Compo

	// Profile is the termenv color profile.
	Profile termenv.Profile

	// UsingCloudEngine shows the cloud icon prefix.
	UsingCloudEngine bool

	// Keys returns the current set of key bindings to display.
	Keys func(out *termenv.Output) []key.Binding

	// PressedKey is the key string that was most recently pressed.
	PressedKey string

	// PressedKeyAt is when the key was pressed.
	PressedKeyAt time.Time
}

func (kb *KeymapBar) Render(ctx tuist.Context) tuist.RenderResult {
	if kb.Keys == nil {
		return tuist.RenderResult{}
	}

	outBuf := new(strings.Builder)
	out := NewOutput(outBuf, termenv.WithProfile(kb.Profile))

	if kb.UsingCloudEngine {
		fmt.Fprint(out, lipgloss.NewStyle().
			Foreground(lipgloss.BrightMagenta).
			Render(CloudIcon+" cloud"))
		fmt.Fprint(out, KeymapStyle.Render(" "+VertBoldDash3+" "))
	}

	kb.renderKeys(out, KeymapStyle, kb.Keys(out))

	view := outBuf.String()
	if view == "" {
		return tuist.RenderResult{}
	}
	return tuist.RenderResult{
		Lines: []string{view},
	}
}

// RenderKeymap renders key bindings into a writer and returns the visible width.
func RenderKeymap(out io.Writer, style lipgloss.Style, keys []key.Binding, pressedKey string, pressedKeyAt time.Time) int {
	w := new(strings.Builder)
	var showedKey bool
	for _, k := range keys {
		mainKey := k.Keys()[0]
		var pressed bool
		if time.Since(pressedKeyAt) < keypressDuration {
			pressed = slices.Contains(k.Keys(), pressedKey)
		}
		if !k.Enabled() && !pressed {
			continue
		}
		keyStyle := style
		if pressed {
			keyStyle = keyStyle.Foreground(nil)
		}
		if showedKey {
			fmt.Fprint(w, style.Render(" "+DotTiny+" "))
		}
		fmt.Fprint(w, keyStyle.Bold(true).Render(mainKey))
		fmt.Fprint(w, keyStyle.Render(" "+k.Help().Desc))
		showedKey = true
	}
	res := w.String()
	fmt.Fprint(out, res)
	return lipgloss.Width(res)
}

func (kb *KeymapBar) renderKeys(out *termenv.Output, style lipgloss.Style, keys []key.Binding) {
	RenderKeymap(out, style, keys, kb.PressedKey, kb.PressedKeyAt)
}
