package dagui

import "github.com/dagger/dagger/engine/telemetryattrs"

// CacheStats is the exact cache-decision tally for a trace or scoped subtree.
// It deliberately contains no estimated savings: a hit span records the work
// that did happen, not the counterfactual work it skipped.
type CacheStats struct {
	Hits        int
	Executed    int
	Joined      int
	Uncached    int
	Unsupported int

	RecipeHits     int
	DigestHits     int
	StructuralHits int
}

// Lookups is the number of invocations that consulted the cache. Uncached
// calls are excluded because their policy performs no lookup.
func (stats CacheStats) Lookups() int {
	return stats.Hits + stats.Executed + stats.Joined
}

// CacheStats returns cache decisions belonging to scope. A nil scope selects
// every received span in the workflow DB: remote and imported work can belong
// to the same workflow even when there is no canonical root span. A non-nil
// scope follows the ordinary parent tree.
func (db *DB) CacheStats(scope *Span) CacheStats {
	var stats CacheStats
	for _, span := range db.Spans.Order {
		if !span.Received || !cacheSpanInScope(span, scope) {
			continue
		}
		if span.CacheContract == "" {
			continue
		}
		if span.CacheContract != telemetryattrs.CacheContractV1 {
			stats.Unsupported++
			continue
		}
		switch span.CacheOutcome {
		case telemetryattrs.CacheOutcomeHit:
			stats.Hits++
			switch span.CacheHitRoute {
			case telemetryattrs.CacheHitRouteRecipe:
				stats.RecipeHits++
			case telemetryattrs.CacheHitRouteDigest:
				stats.DigestHits++
			case telemetryattrs.CacheHitRouteStructural:
				stats.StructuralHits++
			}
		case telemetryattrs.CacheOutcomeExecuted:
			stats.Executed++
		case telemetryattrs.CacheOutcomeJoined:
			stats.Joined++
		case telemetryattrs.CacheOutcomeUncached:
			stats.Uncached++
		}
	}
	return stats
}

// HasCacheReport reports whether the current trace contains cache evidence the
// final renderer can summarize. Unknown versions count too: silently hiding
// them would make a producer/consumer contract mismatch invisible.
func (db *DB) HasCacheReport() bool {
	stats := db.CacheStats(nil)
	return stats.Lookups() > 0 || stats.Uncached > 0 || stats.Unsupported > 0
}

func cacheSpanInScope(span, scope *Span) bool {
	if scope == nil {
		return true
	}
	for current := span; current != nil; current = current.ParentSpan {
		if current == scope {
			return true
		}
	}
	return false
}
