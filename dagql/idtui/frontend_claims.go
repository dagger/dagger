package idtui

import (
	"github.com/dagger/dagger/dagql/dagui"
	telemetry "github.com/dagger/otel-go"
)

// renderClaims records which pieces of span output have already been
// represented by higher-level sections during a render pass.
//
// Final output can surface the same underlying event in multiple ways: a failed
// test report may include the test span's logs, while the containing check span
// may also point at that same span as an error origin. Claims let each renderer
// declare ownership of the logs/errors it covers so later renderers can skip
// redundant details. Claims are intentionally per-render state; they are reset
// before rendering rather than treated as persistent UI state.
type renderClaims struct {
	errors map[dagui.SpanID]struct{}
	logs   map[dagui.SpanID]struct{}
}

func newRenderClaims() *renderClaims {
	return &renderClaims{
		errors: make(map[dagui.SpanID]struct{}),
		logs:   make(map[dagui.SpanID]struct{}),
	}
}

func (claims *renderClaims) claimError(span *dagui.Span) {
	if span == nil {
		return
	}
	claims.claimErrorID(span.ID)
}

func (claims *renderClaims) claimErrorID(id dagui.SpanID) {
	if claims == nil || !id.IsValid() {
		return
	}
	claims.errors[id] = struct{}{}
}

func (claims *renderClaims) hasError(id dagui.SpanID) bool {
	if claims == nil || !id.IsValid() {
		return false
	}
	_, ok := claims.errors[id]
	return ok
}

func (claims *renderClaims) claimLog(span *dagui.Span) {
	if span == nil {
		return
	}
	claims.claimLogID(span.ID)
}

func (claims *renderClaims) claimLogID(id dagui.SpanID) {
	if claims == nil || !id.IsValid() {
		return
	}
	claims.logs[id] = struct{}{}
}

func (claims *renderClaims) hasLog(id dagui.SpanID) bool {
	if claims == nil || !id.IsValid() {
		return false
	}
	_, ok := claims.logs[id]
	return ok
}

// claimTestReport marks output covered by the test report rooted at span. The
// report owns logs for failed/skipped test cases and supersedes error-origin
// log/error blocks for the parent span that would otherwise duplicate them.
func (claims *renderClaims) claimTestReport(span *dagui.Span, view *dagui.TestView) {
	if claims == nil {
		return
	}
	if span != nil {
		for _, origin := range span.ErrorOrigins.Order {
			if origin == nil || origin.ID == span.ID {
				continue
			}
			claims.claimError(origin)
			claims.claimLog(origin)
		}
	}
	entries := collectTestSummaryEntries(view)
	for _, entry := range entries.failing {
		claims.claimLog(entry.span)
	}
	for _, entry := range entries.skipped {
		claims.claimLog(entry.span)
	}
}

func (claims *renderClaims) hasRootError(err error) bool {
	if claims == nil || err == nil {
		return false
	}
	origins := telemetry.ParseErrorOrigins(err.Error())
	if len(origins) == 0 {
		return false
	}
	for _, origin := range origins {
		if !origin.IsValid() {
			return false
		}
		if !claims.hasError(dagui.SpanID{SpanID: origin.SpanID()}) {
			return false
		}
	}
	return true
}
