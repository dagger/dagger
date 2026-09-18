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
		lines = append(lines, fmt.Sprintf("Cache hit rate: %.0f%% (%d/%d)", rate, stats.Hits, lookups))
	}
	lines = append(lines, fmt.Sprintf("Mutualized jobs: %d", stats.Joined))

	var details []string
	// Executed is intentionally not rendered: it is the complement of hits and
	// mutualized jobs, and exposing the engine term made the report harder to
	// understand without adding useful information.
	if stats.Uncached > 0 {
		details = append(details, fmt.Sprintf("Cache bypassed: %d", stats.Uncached))
	}
	if stats.Unsupported > 0 {
		details = append(details, fmt.Sprintf("Unsupported records: %d", stats.Unsupported))
	}
	if len(details) > 0 {
		lines = append(lines, strings.Join(details, " · "))
	}
	return lines
}
