package idtui

import (
	"fmt"
	"strings"

	"github.com/muesli/termenv"
	"github.com/vito/tuist"
)

// StatusLineData carries structured token/cost/context data for the status line.
type StatusLineData struct {
	// Model is the active model identifier (e.g. "claude-opus-4-6").
	Model string
	// SubscriptionLabel is set when using an OAuth subscription (e.g. "sub").
	SubscriptionLabel string
	// InputTokens is cumulative input tokens across all turns.
	InputTokens int
	// OutputTokens is cumulative output tokens across all turns.
	OutputTokens int
	// CacheReads is cumulative cache read tokens.
	CacheReads int
	// CacheWrites is cumulative cache write tokens.
	CacheWrites int
	// TotalCost is the cumulative dollar cost across all models.
	TotalCost float64
	// ContextPercent is the current context window usage (0-100+).
	// Negative means unknown.
	ContextPercent float64
	// ContextWindow is the model's context window size in tokens.
	ContextWindow int
	// AutoCompact indicates whether auto-compaction is enabled.
	AutoCompact bool
}

// LLMCostFunc computes the dollar cost of a model's token usage. The CLI
// registers one (closing over its pricing tables) so the frontend can price the
// live metric rollup without depending on the pricing package.
type LLMCostFunc func(model string, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens int64) float64

// StatusLineLive carries the aggregate token/cost rollup across all models and
// sub-agents, recomputed from live metrics at render time so the status line
// stays current between turns instead of freezing on the last per-step push.
type StatusLineLive struct {
	InputTokens  int
	OutputTokens int
	CacheReads   int
	CacheWrites  int
	TotalCost    float64
}

// StatusLine renders a compact, single-line status bar showing LLM token
// usage, cost, context window utilisation and the active model name:
//
//	↑6.3k ↓30k R3.8M W144k $3.609 (sub) 34.1%/200k (auto)         claude-opus-4-6
type StatusLine struct {
	tuist.Compo

	profile termenv.Profile
	data    StatusLineData
	// liveStats, when set, is consulted on every render to source the token
	// rollup and cost from live metrics (all models + sub-agents). It returns
	// false before any metrics have arrived, falling back to data.
	liveStats func() (StatusLineLive, bool)
}

var _ tuist.Component = (*StatusLine)(nil)

func (sl *StatusLine) SetData(d StatusLineData) {
	sl.data = d
	sl.Update()
}

func (sl *StatusLine) Render(ctx tuist.Context) {
	d := sl.data
	if d.Model == "" {
		return
	}

	// Override the token rollup and cost with live metrics so they reflect the
	// latest turn (and any sub-agents) without waiting for the next per-step push.
	if sl.liveStats != nil {
		if live, ok := sl.liveStats(); ok {
			d.InputTokens = live.InputTokens
			d.OutputTokens = live.OutputTokens
			d.CacheReads = live.CacheReads
			d.CacheWrites = live.CacheWrites
			d.TotalCost = live.TotalCost
		}
	}

	width := max(ctx.Width, 20)

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

	// Cost, with optional subscription indicator.
	if d.TotalCost > 0 || d.SubscriptionLabel != "" {
		costStr := fmt.Sprintf("$%.3f", d.TotalCost)
		if d.SubscriptionLabel != "" {
			costStr += " (" + d.SubscriptionLabel + ")"
		}
		parts = append(parts, costStr)
	}

	// Context usage.
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
		// Colorise based on usage.
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
		// Truncate model name.
		avail := width - leftWidth - minPad
		if avail > 3 {
			right = right[:avail-3] + "..."
		}
		pad := strings.Repeat(" ", width-leftWidth-len(right))
		line = left + pad + right
	} else {
		line = left
	}

	// Apply dim styling to the whole line.
	dimLine := out.String(line).Foreground(termenv.ANSIBrightBlack).String()

	ctx.Lines(dimLine)
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
