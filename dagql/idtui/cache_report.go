package idtui

import (
	"fmt"
	"strings"
	"time"

	"github.com/dustin/go-humanize"

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
	if stats.Lookups() == 0 {
		return nil
	}

	lookups := stats.Lookups()
	rate := float64(stats.Hits) / float64(lookups) * 100
	line := fmt.Sprintf("♻️ Cache hits %d/%d (%.0f%%)", stats.Hits, lookups, rate)
	if impact := fe.cacheImpact; impact != nil && !zoomed {
		var savings []string
		if impact.Elapsed > 0 {
			savings = append(savings, fmt.Sprintf("~%s wall", humanDuration(impact.Elapsed)))
		}
		if impact.HasCPU && impact.CPU > 0 {
			savings = append(savings, fmt.Sprintf("~%s CPU", humanDuration(impact.CPU)))
		}
		if impact.HasMemory && impact.MemoryBytes > 0 {
			savings = append(savings, fmt.Sprintf(
				"~%s memory for %s",
				humanize.Bytes(uint64(impact.MemoryBytes)), humanDuration(impact.MemoryPeriod),
			))
		}
		if impact.HasNetworkRx && impact.NetworkRxBytes > 0 {
			savings = append(savings, fmt.Sprintf("~%s net rx", humanize.Bytes(uint64(impact.NetworkRxBytes))))
		}
		if impact.HasNetworkTx && impact.NetworkTxBytes > 0 {
			savings = append(savings, fmt.Sprintf("~%s net tx", humanize.Bytes(uint64(impact.NetworkTxBytes))))
		}
		if len(savings) > 0 {
			line += " · ⚡ Saved " + strings.Join(savings, " | ")
		}
	}
	return []string{line}
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
