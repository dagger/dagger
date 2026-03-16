package idtui

import (
	"fmt"
	"strings"

	"github.com/muesli/termenv"
	"github.com/vito/tuist"
)

// StatusLine renders a compact, single-line status bar showing LLM token
// usage, cost, context window utilisation and the active model name:
//
//	↑6.3k ↓30k R3.8M W144k $3.609 (sub) 34.1%/200k (auto)         claude-opus-4-6
type StatusLine struct {
	tuist.Compo

	profile termenv.Profile
	data    StatusLineData
}

var _ tuist.Component = (*StatusLine)(nil)

func (sl *StatusLine) SetData(d StatusLineData) {
	sl.data = d
}

func (sl *StatusLine) Render(ctx tuist.Context) tuist.RenderResult {
	d := sl.data
	if d.Model == "" {
		return tuist.RenderResult{}
	}

	width := ctx.Width
	if width < 20 {
		width = 20
	}

	out := NewOutput(new(strings.Builder), termenv.WithProfile(sl.profile))

	// -- left side: token stats + cost + context --------------------------
	var parts []string

	if d.InputTokens > 0 {
		parts = append(parts, "↑"+formatTokenCount(d.InputTokens))
	}
	if d.OutputTokens > 0 {
		parts = append(parts, "↓"+formatTokenCount(d.OutputTokens))
	}
	if d.CacheReads > 0 {
		parts = append(parts, "R"+formatTokenCount(d.CacheReads))
	}
	if d.CacheWrites > 0 {
		parts = append(parts, "W"+formatTokenCount(d.CacheWrites))
	}

	// Cost, with optional subscription indicator
	if d.TotalCost > 0 || d.SubscriptionLabel != "" {
		costStr := fmt.Sprintf("$%.3f", d.TotalCost)
		if d.SubscriptionLabel != "" {
			costStr += " (" + d.SubscriptionLabel + ")"
		}
		parts = append(parts, costStr)
	}

	// Context usage
	if d.ContextWindow > 0 {
		autoTag := ""
		if d.AutoCompact {
			autoTag = " (auto)"
		}
		var ctxPart string
		if d.ContextPercent >= 0 {
			ctxPart = fmt.Sprintf("%.1f%%/%s%s",
				d.ContextPercent,
				formatTokenCount(d.ContextWindow),
				autoTag)
		} else {
			ctxPart = fmt.Sprintf("?/%s%s",
				formatTokenCount(d.ContextWindow),
				autoTag)
		}
		// Colorise based on usage
		switch {
		case d.ContextPercent > 90:
			ctxPart = out.String(ctxPart).Foreground(termenv.ANSIRed).String()
		case d.ContextPercent > 70:
			ctxPart = out.String(ctxPart).Foreground(termenv.ANSIYellow).String()
		}
		parts = append(parts, ctxPart)
	}

	left := strings.Join(parts, " ")
	leftWidth := visibleLen(left)

	// -- right side: model name -------------------------------------------
	right := d.Model
	rightWidth := len(right)

	// Assemble the line with padding between left and right.
	const minPad = 2
	totalNeeded := leftWidth + minPad + rightWidth
	var line string
	if totalNeeded <= width {
		pad := strings.Repeat(" ", width-leftWidth-rightWidth)
		line = left + pad + right
	} else if leftWidth+minPad+3 < width {
		// Truncate model name
		avail := width - leftWidth - minPad
		if avail > 3 {
			right = right[:avail-3] + "..."
		}
		pad := strings.Repeat(" ", width-leftWidth-len(right))
		line = left + pad + right
	} else {
		line = left
	}

	// Apply dim styling to the whole line
	dimLine := out.String(line).Foreground(termenv.ANSIBrightBlack).String()

	return tuist.RenderResult{
		Lines: []string{dimLine},
	}
}

// formatTokenCount formats a token count in a compact human-readable form.
func formatTokenCount(count int) string {
	switch {
	case count < 1000:
		return fmt.Sprintf("%d", count)
	case count < 10_000:
		return fmt.Sprintf("%.1fk", float64(count)/1000)
	case count < 1_000_000:
		return fmt.Sprintf("%dk", count/1000)
	case count < 10_000_000:
		return fmt.Sprintf("%.1fM", float64(count)/1_000_000)
	default:
		return fmt.Sprintf("%dM", count/1_000_000)
	}
}

// visibleLen returns the visible width of a string, stripping ANSI codes.
func visibleLen(s string) int {
	// Strip ANSI escape sequences for width calculation
	clean := strings.Builder{}
	inEscape := false
	for i := 0; i < len(s); i++ {
		if s[i] == '\x1b' {
			inEscape = true
			continue
		}
		if inEscape {
			if (s[i] >= 'A' && s[i] <= 'Z') || (s[i] >= 'a' && s[i] <= 'z') {
				inEscape = false
			}
			continue
		}
		clean.WriteByte(s[i])
	}
	return clean.Len()
}
