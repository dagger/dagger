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

	// Hidden reports whether the bar should render nothing. The shell hides
	// it: a hint above the prompt and the keymap HUD bubble stand in for it.
	Hidden func() bool

	// PressedKey is the key string that was most recently pressed.
	PressedKey string

	// PressedKeyAt is when the key was pressed.
	PressedKeyAt time.Time
}

func (kb *KeymapBar) Render(ctx tuist.Context) {
	if kb.Keys == nil || (kb.Hidden != nil && kb.Hidden()) {
		return
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
		return
	}
	ctx.Line("")
	ctx.Line(view)
}

// RenderKeymap renders key bindings into a writer and returns the visible width.
func RenderKeymap(out io.Writer, style lipgloss.Style, keys []key.Binding, pressedKey string, pressedKeyAt time.Time) int {
	w := new(strings.Builder)
	var showedKey bool
	for _, k := range keys {
		mainKey := k.Help().Key
		if mainKey == "" {
			mainKey = k.Keys()[0]
		}
		pressed := keyPressed(k, pressedKey, pressedKeyAt)
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

// keyPressed reports whether k was pressed recently enough to highlight.
func keyPressed(k key.Binding, pressedKey string, pressedKeyAt time.Time) bool {
	return time.Since(pressedKeyAt) < keypressDuration && slices.Contains(k.Keys(), pressedKey)
}

// keymapColumnGap separates the keymap bubble's columns.
const keymapColumnGap = "   "

// RenderKeymapLines renders key bindings as a table for the keymap bubble:
// the keys bold, padded to the widest in their column, and their descriptions
// in style beside them. When two columns fit within width, the bindings fill
// the left column top to bottom and continue in the right; otherwise (a long
// label, or width <= 0) they are listed one per line. Like RenderKeymap, it
// skips disabled bindings unless just pressed and lights up a pressed binding.
func RenderKeymapLines(style lipgloss.Style, keys []key.Binding, pressedKey string, pressedKeyAt time.Time, width int) []string {
	var entries []keymapEntry
	for _, k := range keys {
		mainKey := k.Help().Key
		if mainKey == "" && len(k.Keys()) > 0 {
			mainKey = k.Keys()[0]
		}
		pressed := keyPressed(k, pressedKey, pressedKeyAt)
		if !k.Enabled() && !pressed {
			continue
		}
		entries = append(entries, keymapEntry{mainKey, k.Help().Desc, pressed})
	}
	single, _ := keymapColumn(style, entries)
	if width <= 0 || len(entries) < 2 {
		return single
	}
	half := (len(entries) + 1) / 2
	left, leftWidth := keymapColumn(style, entries[:half])
	right, rightWidth := keymapColumn(style, entries[half:])
	if leftWidth+len(keymapColumnGap)+rightWidth > width {
		return single
	}
	lines := make([]string, len(left))
	for i, line := range left {
		if i < len(right) {
			line = padANSI(line, leftWidth) + keymapColumnGap + right[i]
		}
		lines[i] = line
	}
	return lines
}

type keymapEntry struct {
	key, desc string
	pressed   bool
}

// keymapColumn renders entries one per line with their descriptions aligned,
// returning the lines and the widest line's width.
func keymapColumn(style lipgloss.Style, entries []keymapEntry) ([]string, int) {
	keyWidth := 0
	for _, e := range entries {
		keyWidth = max(keyWidth, lipgloss.Width(e.key))
	}
	lines := make([]string, 0, len(entries))
	width := 0
	for _, e := range entries {
		descStyle := style
		if e.pressed {
			descStyle = descStyle.Foreground(nil)
		}
		pad := strings.Repeat(" ", keyWidth-lipgloss.Width(e.key))
		line := lipgloss.NewStyle().Bold(true).Render(e.key) + pad + "  " + descStyle.Render(e.desc)
		lines = append(lines, line)
		width = max(width, lipgloss.Width(line))
	}
	return lines, width
}

func (kb *KeymapBar) renderKeys(out *termenv.Output, style lipgloss.Style, keys []key.Binding) {
	RenderKeymap(out, style, keys, kb.PressedKey, kb.PressedKeyAt)
}
