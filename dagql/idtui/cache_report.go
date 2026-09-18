package idtui

import (
	"fmt"
	"strings"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/muesli/termenv"

	"github.com/dagger/dagger/dagql/dagui"
)

// cacheReport renders exact cache-decision evidence for the report scope and,
// when available, estimates relative to a compatible historical cold run.
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
	if impact := fe.cacheImpact; impact != nil && !zoomed {
		lines = append(lines, "", reportHeadingLine(out, fe.agentStyle(), "ESTIMATED CACHE SAVINGS"))
		lines = append(lines, fmt.Sprintf("Compared with a colder run (%.0f%% cache hit rate)", impact.BaselineRate))
		if impact.Elapsed > 0 {
			lines = append(lines, fmt.Sprintf("Finished ~%s faster (%.0f%%)", humanDuration(impact.Elapsed), impact.Percent))
		}
		if impact.HasCPU && impact.CPU > 0 {
			lines = append(lines, fmt.Sprintf("Compute avoided: ~%s of CPU work", humanDuration(impact.CPU)))
		}
		if impact.HasNetwork && impact.NetworkBytes > 0 {
			lines = append(lines, fmt.Sprintf("Network transfer avoided: ~%s", humanize.Bytes(uint64(impact.NetworkBytes))))
		}
	}

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

func humanDuration(d time.Duration) string {
	if d < time.Second {
		return d.Round(10 * time.Millisecond).String()
	}
	d = d.Round(time.Second)
	if d >= time.Hour && d%time.Hour == 0 {
		return fmt.Sprintf("%dh", d/time.Hour)
	}
	if d >= time.Minute && d%time.Minute == 0 {
		return fmt.Sprintf("%dm", d/time.Minute)
	}
	return d.String()
}
