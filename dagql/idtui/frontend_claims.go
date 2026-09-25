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
	// tests records test-case spans already represented by a check's test report,
	// so the global tests section can show only the cases no check covered.
	tests map[dagui.SpanID]struct{}
	// parent, when set, is the claims this set was forked from (see fork):
	// lookups consult it too, and commit folds this set's claims into it.
	parent *renderClaims
}

func newRenderClaims() *renderClaims {
	return &renderClaims{
		errors: make(map[dagui.SpanID]struct{}),
		logs:   make(map[dagui.SpanID]struct{}),
		tests:  make(map[dagui.SpanID]struct{}),
	}
}

// fork returns claims for one sub-render: they dedupe against everything
// already claimed (lookups fall through to claims), but record their own
// claims separately until commit. That lets a caller
//
//   - ask what exactly the sub-render represented (ownsTestCase), e.g. a tool
//     call's TESTS rollup leaving out the cases its CHECKS rollup nested under
//     their checks, untouched by claims seeded elsewhere in the pass; and
//   - discard a render it ends up not showing (e.g. a rollup's full detail,
//     condensed away to fit the screen) by simply not committing it.
func (claims *renderClaims) fork() *renderClaims {
	fork := newRenderClaims()
	fork.parent = claims
	return fork
}

// commit folds a fork's claims into the claims it was forked from.
func (claims *renderClaims) commit() {
	if claims == nil || claims.parent == nil {
		return
	}
	for id := range claims.errors {
		claims.parent.claimErrorID(id)
	}
	for id := range claims.logs {
		claims.parent.claimLogID(id)
	}
	for id := range claims.tests {
		claims.parent.claimTestCase(id)
	}
}

func (claims *renderClaims) claimTestCase(id dagui.SpanID) {
	if claims == nil || !id.IsValid() {
		return
	}
	claims.tests[id] = struct{}{}
}

// withForkedClaims runs render against claims forked from the pass's (see
// renderClaims.fork) and returns the fork, uncommitted: the caller decides
// whether what render claimed counts.
func (fe *frontendPretty) withForkedClaims(render func()) *renderClaims {
	parent := fe.claims
	fork := parent.fork()
	fe.claims = fork
	defer func() { fe.claims = parent }()
	render()
	return fork
}

// anyTestCases reports whether any check's test report claimed cases this pass.
// When false the global section is the whole test view (no check rendered
// tests), so missing-ancestor cases are not displaced and warrant no warning.
func (claims *renderClaims) anyTestCases() bool {
	return claims.testCaseCount() > 0
}

// testCaseCount is the number of claimed test cases. Claims only grow within a
// render pass, so (claims pointer, count) identifies the claimed-case set --
// derived-view memos key on it.
func (claims *renderClaims) testCaseCount() int {
	if claims == nil {
		return 0
	}
	return len(claims.tests) + claims.parent.testCaseCount()
}

func (claims *renderClaims) hasTestCase(id dagui.SpanID) bool {
	return claims.ownsTestCase(id) || (claims != nil && claims.parent.hasTestCase(id))
}

// ownsTestCase reports whether these claims themselves -- not the claims they
// were forked from -- claimed a test case.
func (claims *renderClaims) ownsTestCase(id dagui.SpanID) bool {
	if claims == nil || !id.IsValid() {
		return false
	}
	_, ok := claims.tests[id]
	return ok
}

// ownsAnyTestCases reports whether these claims themselves claimed any case.
func (claims *renderClaims) ownsAnyTestCases() bool {
	return claims != nil && len(claims.tests) > 0
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
	if _, ok := claims.errors[id]; ok {
		return true
	}
	return claims.parent.hasError(id)
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
	if _, ok := claims.logs[id]; ok {
		return true
	}
	return claims.parent.hasLog(id)
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
	// Claim every case the report covers -- including passing ones -- so the
	// global tests section can subtract them and surface only cases no check
	// represented (e.g. orphans whose ancestor spans are missing from the data).
	claims.claimTestCases(view)
}

// claimTestCases marks every test-case span a view covers as represented,
// without touching the log/error claims claimTestReport also records. The
// interactive render emits the global tests section before the trace rows (so
// the section's log claims suppress duplicate logs in the rows above it), yet
// the section's orphan filter must already know which cases the checks below it
// own. Seeding just the case claims up front satisfies that without disturbing
// the log/error claim ordering; the later rollup render re-claims idempotently.
func (claims *renderClaims) claimTestCases(view *dagui.TestView) {
	if claims == nil || view == nil {
		return
	}
	for id, node := range view.BySpan {
		if node == nil {
			continue
		}
		// Case-less suites (e.g. a package that failed to compile) are entries
		// in their own right -- they have no case children to carry their
		// status -- so claim them too, keeping the global section from
		// repeating a suite a check's report already covers.
		if node.Kind == dagui.TestNodeCase || len(node.Children) == 0 {
			claims.claimTestCase(id)
		}
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
