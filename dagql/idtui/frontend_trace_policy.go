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
	// their logs, derived from ErrorOrigins, ABOVE the tree. The section
	// renders first, claiming the origins, so the tree below stays terse.
	showRootCause bool
	// showRootCauseLast surfaces the same root-cause origins BELOW the tree,
	// skipping any the tree already rendered (claims). The tree renders first
	// and keeps the failure attributed in place -- inline origins under the
	// failing row, with logs -- and this section appends only the detail the
	// tree couldn't carry (e.g. an origin whose row printed a bare error with
	// its logs collapsed).
	showRootCauseLast bool
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
	// Root-cause origins only accompany a FAILED check zoom: a passing check
	// can still carry error origins propagated up from causal children --
	// retried attempts, tolerated probes -- and appending their ERROR blocks
	// to a PASSED report would be misleading (mirroring the zoomRoot guard
	// below).
	if k == zoomCheck {
		if span := fe.db.Spans.Map[fe.ZoomedSpan]; span == nil || !span.IsFailedOrCausedFailure() {
			pol.showRootCause = false
		}
	}
	// A plain trace -- no checks and no tests, e.g. a bare `dagger call` -- has no
	// checks section or test rollup to carry the failure. The tree itself
	// attributes it (the failing call renders as a row, often with its origin
	// inline), so let the tree lead, then append the root span's root-cause
	// origins BELOW it for whatever the tree couldn't show -- typically the
	// origin's logs, which a stored trace only has because this policy also
	// drives their fetch. Hoisting the cause ABOVE the tree instead (the
	// showRootCause treatment) claimed the origins and stripped them from the
	// tree, orphaning the failure from the call that owns it. Only at the root
	// zoom; a check/test/span zoom has its own tier. Only when the root
	// actually failed (mirroring the drill-in suggestions guard): a passing
	// run can still carry error origins -- tolerated probes (docker-build's
	// .dockerignore stats) or encapsulated failures -- and appending their
	// ERROR blocks to a PASSED report would be misleading. Anchored on the
	// primary span (what the section itself renders from), not db.RootSpan: a
	// nested run -- a propagated traceparent, e.g. dagger-in-dagger -- has no
	// parentless root span at all.
	if k == zoomRoot && len(fe.db.SurfacedChecks()) == 0 {
		if primary := fe.db.Spans.Map[fe.db.PrimarySpan]; primary != nil && primary.IsFailed() {
			if tv := fe.db.TestView(); tv == nil || !tv.HasTests() {
				pol.showRootCauseLast = true
			}
		}
	}
	return pol
}
