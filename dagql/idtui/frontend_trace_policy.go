package idtui

import "os"

// traceRenderPolicy controls how the final `dagger trace --full` report is
// rendered for a given zoom target. The decisions are deliberately data, not
// branches scattered through the renderer, so the tiering scheme is easy to
// tweak (and to A/B via DAGGER_TRACE_RENDER) as we settle on what's least
// redundant without being confusing.
//
// The two knobs that matter (per the design): descendant-vs-non-descendant
// logs (showOwnDescendantLogs) and logs-vs-subtests+logs (showSubtests). The
// other two select the extra sections.
type traceRenderPolicy struct {
	// showSubtests lists subtests beneath the zoom target, each with their own
	// inline logs (the existing check-inline-tests behavior).
	showSubtests bool
	// showOwnDescendantLogs dumps the zoom target's own rolled-up descendant
	// logs (the raw-subtree treatment). Suppressed when showSubtests already
	// carries the logs, to avoid showing them twice.
	showOwnDescendantLogs bool
	// showRootCause surfaces the zoom target's root-cause origin span(s) and
	// their logs, derived from ErrorOrigins.
	showRootCause bool
	// showSuggestions prints a "next steps" block of drill-in commands.
	showSuggestions bool
}

type zoomKind int

const (
	zoomRoot zoomKind = iota // no flag / whole trace
	zoomCheck
	zoomTest
	zoomSpan
)

func policyForZoom(k zoomKind) traceRenderPolicy {
	switch k {
	case zoomRoot:
		return traceRenderPolicy{
			showSubtests:    true,
			showSuggestions: true,
		}
	case zoomCheck:
		return traceRenderPolicy{
			showSubtests:    true,
			showRootCause:   true,
			showSuggestions: true,
		}
	case zoomTest, zoomSpan:
		return traceRenderPolicy{
			showOwnDescendantLogs: true,
		}
	}
	return traceRenderPolicy{showOwnDescendantLogs: true}
}

// zoomKind classifies the current zoom target so renderPolicy can pick a tier.
// Must be called after recalculateViewLocked (promoteChecksLocked may rewrite
// fe.ZoomedSpan).
func (fe *frontendPretty) zoomKind() zoomKind {
	id := fe.ZoomedSpan
	if !id.IsValid() || id == fe.db.PrimarySpan {
		return zoomRoot
	}
	span, ok := fe.db.Spans.Map[id]
	if !ok {
		return zoomSpan
	}
	if span.CheckName != "" {
		return zoomCheck
	}
	if tv := fe.db.TestView(); tv != nil && tv.BySpan[id] != nil {
		return zoomTest
	}
	return zoomSpan
}

// renderPolicy resolves the policy for the current zoom, honoring the
// DAGGER_TRACE_RENDER override so we can render any preset against a fixed
// zoom while comparing approaches.
func (fe *frontendPretty) renderPolicy() traceRenderPolicy {
	k := fe.zoomKind()
	pol := policyForZoom(k)
	switch os.Getenv("DAGGER_TRACE_RENDER") {
	case "root":
		return policyForZoom(zoomRoot)
	case "check":
		return policyForZoom(zoomCheck)
	case "test":
		return policyForZoom(zoomTest)
	case "span":
		return policyForZoom(zoomSpan)
	}
	// A plain trace -- no checks and no tests, e.g. a bare `dagger call` -- has no
	// checks section or test rollup to carry the failure, so the whole-trace view
	// would fall back to the bootstrap progress tree and never show why it failed.
	// Surface the root span's root cause directly instead: the same span-derived
	// origin (an `error_origin` link / traceparent marker on the root) the
	// summary's ROOT CAUSE uses. Only at the root zoom; a check/test/span zoom has
	// its own tier.
	if k == zoomRoot && len(fe.db.SurfacedChecks()) == 0 {
		if tv := fe.db.TestView(); tv == nil || !tv.HasTests() {
			pol.showRootCause = true
		}
	}
	return pol
}
