package idtui

import (
	"fmt"
	"strings"

	"github.com/muesli/termenv"

	"github.com/dagger/dagger/dagql/dagui"
)

// cacheReport renders exact cache-decision evidence for the report scope. It
// intentionally does not claim time or resource savings: those require a
// compatible historical miss profile, which is absent from a hit-only trace.
func (fe *frontendPretty) cacheReport(zoomed bool) []string {
	var scope *dagui.Span
	if zoomed && fe.rowsView != nil {
		scope = fe.rowsView.Zoomed
	}
	stats := fe.db.CacheStats(scope)
	if stats.Lookups() == 0 && stats.Uncached == 0 && stats.Unsupported == 0 {
		return nil
	}

	out := NewOutput(new(strings.Builder), termenv.WithProfile(fe.profile))
	lines := []string{reportHeadingLine(out, fe.agentStyle(), "CACHE")}
	if lookups := stats.Lookups(); lookups > 0 {
		rate := float64(stats.Hits) / float64(lookups) * 100
		lines = append(lines, fmt.Sprintf("%d / %d lookups hit (%.0f%%)", stats.Hits, lookups, rate))
	}

	var details []string
	if stats.Executed > 0 {
		details = append(details, fmt.Sprintf("%d executed", stats.Executed))
	}
	if stats.Joined > 0 {
		details = append(details, fmt.Sprintf("%d joined", stats.Joined))
	}
	if stats.Uncached > 0 {
		details = append(details, fmt.Sprintf("%d uncached", stats.Uncached))
	}
	if stats.Unsupported > 0 {
		details = append(details, fmt.Sprintf("%d unsupported", stats.Unsupported))
	}
	if len(details) > 0 {
		lines = append(lines, strings.Join(details, " · "))
	}
	return lines
}
