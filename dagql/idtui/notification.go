package idtui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/muesli/termenv"
	"github.com/vito/tuist"
)

// NotificationBubble renders a bordered notification box with a title
// embedded in the top border and optional keymap in the border.
//
//	╭─ Title ─── q quit ─╮
//	│ content here        │
//	│ more content        │
//	╰─────────────────────╯
type NotificationBubble struct {
	tuist.Compo

	fe      *frontendPretty
	section SidebarSection
}

var _ tuist.Component = (*NotificationBubble)(nil)

func newNotificationBubble(fe *frontendPretty, section SidebarSection) *NotificationBubble {
	return &NotificationBubble{
		fe:      fe,
		section: section,
	}
}

func (n *NotificationBubble) Render(ctx tuist.Context) {
	width := ctx.Width
	if width < 10 {
		width = 30
	}

	profile := n.fe.profile
	borderFg := termenv.ANSIBrightBlack

	// Compute inner width (subtract 2 for left+right border chars)
	innerWidth := width - 2

	// Get content
	content := n.section.Body(innerWidth - 2) // -2 for 1-char padding each side
	if content == "" {
		return
	}

	contentLines := strings.Split(strings.TrimRight(content, "\n"), "\n")

	// Top border: ╭─ Title ─── keymap ──╮
	ctx.Line(n.buildTopBorder(profile, borderFg, innerWidth))

	// Content lines with side borders and background
	out := NewOutput(new(strings.Builder), termenv.WithProfile(profile))
	leftBorder := out.String(VertBar).Foreground(borderFg).String()
	rightBorder := out.String(VertBar).Foreground(borderFg).String()
	bgStyle := lipgloss.NewStyle().
		Width(innerWidth)
	for _, line := range contentLines {
		// Apply background to the full inner width
		padded := bgStyle.Render(" " + line)
		ctx.Line(leftBorder + padded + rightBorder)
	}

	// Bottom border: ╰───────────────────╯
	bottomBorder := out.String(
		CornerBottomLeft + strings.Repeat(HorizBar, innerWidth) + CornerBottoRight,
	).Foreground(borderFg).String()
	ctx.Line(bottomBorder)
}

func (n *NotificationBubble) buildTopBorder(profile termenv.Profile, borderFg termenv.Color, innerWidth int) string {
	out := NewOutput(new(strings.Builder), termenv.WithProfile(profile))

	corner1 := out.String(CornerTopLeft).Foreground(borderFg).String()
	corner2 := out.String(CornerTopRight).Foreground(borderFg).String()
	bar := func(count int) string {
		if count <= 0 {
			return ""
		}
		return out.String(strings.Repeat(HorizBar, count)).Foreground(borderFg).String()
	}

	// Title portion
	titleStr := ""
	titleWidth := 0
	if n.section.Title != "" {
		titleStr = " " + out.String(n.section.Title).Bold().String() + " "
		titleWidth = len(n.section.Title) + 2 // spaces around title
	}

	// Keymap portion
	keymapStr := ""
	keymapWidth := 0
	if len(n.section.KeyMap) > 0 {
		kb := new(strings.Builder)
		keymapWidth = RenderKeymap(kb,
			KeymapStyle,
			n.section.KeyMap,
			n.fe.pressedKey, n.fe.pressedKeyAt)
		keymapStr = " " + kb.String() + " "
		keymapWidth += 2 // spaces around keymap
	}

	// Calculate fill bars
	usedWidth := titleWidth + keymapWidth
	if titleWidth > 0 {
		usedWidth += 1 // bar between ╭ and title
	}
	remaining := innerWidth - usedWidth
	if remaining < 0 {
		remaining = 0
	}

	var top strings.Builder
	top.WriteString(corner1)
	if titleWidth > 0 {
		top.WriteString(bar(1))
		top.WriteString(titleStr)
	}
	if keymapWidth > 0 {
		beforeKeymap := max(1, remaining-1)
		afterKeymap := remaining - beforeKeymap
		top.WriteString(bar(beforeKeymap))
		top.WriteString(keymapStr)
		top.WriteString(bar(afterKeymap))
	} else {
		top.WriteString(bar(remaining))
	}
	top.WriteString(corner2)

	return top.String()
}

// notificationWidth returns the width for notification bubbles.
func notificationWidth(windowWidth int) int {
	return min(50, max(30, windowWidth/3))
}
