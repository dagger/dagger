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
		// Clamp the line to the space between the borders before padding.
		// lipgloss's Width WRAPS rather than truncates, so an over-long line
		// comes back as a multi-row string — and tuist treats every ctx.Line
		// entry as exactly one terminal row, so that desynchronizes the frame's
		// line accounting: the diff renderer's relative cursor moves drift by
		// the number of extra rows, duplicating and clobbering lines all over
		// the screen, not just in this box. Sidebar content comes from producers
		// that may ignore the width they are handed (long host paths, unbounded
		// error text), so the box clamps rather than trusting them.
		// See TestNotificationBubbleOverlongContent.
		line = strings.Map(dropRowBreaks, tuist.ExpandTabs(line, 8))
		line = tuist.Truncate(line, innerWidth-1, "…")
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
		titleWidth = lipgloss.Width(n.section.Title) + 2 // spaces around title
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

	// Assemble everything between the two corners. The fill bars distribute the
	// leftover space, keeping the keymap right-aligned with a single trailing
	// bar before the closing corner when there's room. Crucially, the fill must
	// never exceed `remaining`: emitting extra bars makes the border wider than
	// the box, and the overlay compositor then truncates the overflow — dropping
	// the closing corner and leaving the box top looking shifted/broken (this is
	// the common case at 80/100/120-column terminals). See TestNotificationTopBorderWidth.
	var inner strings.Builder
	if titleWidth > 0 {
		inner.WriteString(bar(1))
		inner.WriteString(titleStr)
	}
	if keymapWidth > 0 {
		afterKeymap := 0
		if remaining > 0 {
			afterKeymap = 1 // single trailing bar before the corner, if it fits
		}
		beforeKeymap := remaining - afterKeymap // always >= 0
		inner.WriteString(bar(beforeKeymap))
		inner.WriteString(keymapStr)
		inner.WriteString(bar(afterKeymap))
	} else {
		inner.WriteString(bar(remaining))
	}

	// Safety clamp: when the title/keymap alone overflow innerWidth (a very
	// narrow box), truncate the inner content so the closing corner still lands
	// exactly at the box edge instead of being pushed off and truncated away.
	innerStr := inner.String()
	if tuist.VisibleWidth(innerStr) > innerWidth {
		innerStr = tuist.Truncate(innerStr, innerWidth, "")
	}

	return corner1 + innerStr + corner2
}

// dropRowBreaks removes the control characters that move the terminal cursor
// off the row being painted: vertical tab and form feed move down a row, and a
// bare carriage return jumps back to column 0 so the rest of the line overwrites
// what it already painted. All of them measure as zero columns, so truncating by
// visible width does not remove them. (Line feeds are handled by splitting the
// content into rows before this runs.)
func dropRowBreaks(r rune) rune {
	switch r {
	case '\r', '\v', '\f':
		return -1
	}
	return r
}

// notificationWidth returns the width for notification bubbles.
func notificationWidth(windowWidth int) int {
	return min(50, max(30, windowWidth/3))
}
