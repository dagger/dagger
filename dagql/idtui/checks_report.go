package idtui

import (
	"fmt"
	"strings"

	"github.com/muesli/termenv"
	"github.com/vito/tuist"

	"github.com/dagger/dagger/dagql/dagui"
)

// checksReport renders the CHECKS heading plus the surfaced-check list for the
// root --full report. It returns nil when zoomed (the zoom views handle their
// own rendering) or when nothing surfaces (e.g. a plain trace, or one whose
// only checks are boundary-contained test fixtures), so the caller can fall
// back to the progress tree.
func (fe *frontendPretty) checksReport(ctx tuist.Context, r *renderer, zoomed bool) []string {
	if zoomed {
		return nil
	}
	checkLines := fe.renderChecksSection(ctx, r)
	if len(checkLines) == 0 {
		return nil
	}
	return append(fe.renderChecksHeader(), checkLines...)
}

// renderChecksSection renders the trace's checks for the final --full report,
// independent of the `reveal` mechanism: every surfaced check (see
// DB.SurfacedChecks) is listed, nested under its parent check, with passing
// checks kept to a single line and failed checks carrying their inline error
// cause -- the same detail the live tree shows on a failed row.
func (fe *frontendPretty) renderChecksSection(ctx tuist.Context, r *renderer) []string {
	roots := fe.db.SurfacedChecks()
	if len(roots) == 0 {
		return nil
	}

	buf := new(strings.Builder)
	out := NewOutput(buf, termenv.WithProfile(fe.profile))

	var render func(node *dagui.CheckNode, depth int)
	render = func(node *dagui.CheckNode, depth int) {
		indent := strings.Repeat("  ", depth)
		icon, color := IconSuccess, termenv.ANSIGreen
		if node.Failed {
			icon, color = IconFailure, termenv.ANSIRed
		}
		dur := dagui.FormatDuration(node.Span.Activity.Duration(r.now))
		fmt.Fprintf(buf, "%s%s %s %s\n",
			indent,
			out.String(icon).Foreground(color).String(),
			node.Name,
			out.String(dur).Faint().String(),
		)

		// A failed leaf check renders its failure inline; a failed parent check
		// defers to the failed children that explain it. A passing leaf check that
		// ran tests nests their full rollup, so every test stays attributed to the
		// check that produced it and the global section is left for tests no check
		// covers.
		switch {
		case node.Failed && !node.HasFailedChild():
			if fe.checkDefersToTests(node.Span) {
				// The check's failures are test cases: render them per-test with
				// rolled-up logs (richer than the check's raw command output).
				for _, line := range fe.renderCheckTests(ctx, node.Span, depth) {
					fmt.Fprintln(buf, line)
				}
			} else {
				// Otherwise show the failed command and its logs.
				for _, origin := range fe.checkRootCauses(node.Span) {
					if !origin.Received {
						// Incremental --full may not have loaded the origin (or its
						// logs); skip rather than render an empty stub.
						continue
					}
					fe.renderCauseDetail(ctx, out, r, origin, depth+1)
				}
			}
		case !node.Failed && len(node.Children) == 0 && fe.checkHasTests(node.Span):
			for _, line := range fe.renderCheckTests(ctx, node.Span, depth) {
				fmt.Fprintln(buf, line)
			}
		}

		for _, child := range node.Children {
			render(child, depth+1)
		}
	}
	for _, root := range roots {
		render(root, 0)
	}
	return strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
}

// renderCauseDetail renders a failed check's cause -- the surfaced command and
// its logs -- indented beneath the check. Unlike renderErrorCause (used by the
// live tree and the zoomed ROOT CAUSE section) it drops the `› parent › parent`
// breadcrumb: at this level the check name is enough context, so we show just
// the failed command and the logs under it.
func (fe *frontendPretty) renderCauseDetail(ctx tuist.Context, out TermOutput, r *renderer, origin *dagui.Span, depth int) {
	indent := strings.Repeat("  ", depth)
	row := &dagui.TraceRow{Span: origin, Expanded: true, Depth: depth}

	fmt.Fprint(out, indent)
	_ = fe.renderStepTitle(ctx, out, r, row, indent, fe, false, false)
	fmt.Fprintln(out)

	fe.requestLogsOnRender(origin.ID)
	if logs := fe.logs.Logs[origin.ID]; logs != nil && !fe.claims.hasLog(origin.ID) {
		pipe := out.String(VertBoldBar).Foreground(restrainedStatusColor(origin)).String()
		logs.SetPrefix(indent + pipe + " ")
		logs.SetHeight(logs.UsedHeight())
		fmt.Fprint(out, logs.View())
	}
	fe.renderStepError(out, r, row, indent)
	fe.claims.claimError(origin)
}

// renderCheckTests renders a check's tests (the failing ones with rolled-up
// logs) beneath it, reusing the zoomed-check tests view. renderZoomedCheckTests
// indents one level under a depth-0 check; pad by the check's own depth so a
// nested check's tests sit one level under it too.
func (fe *frontendPretty) renderCheckTests(ctx tuist.Context, span *dagui.Span, depth int) []string {
	lines := fe.renderZoomedCheckTests(ctx, span)
	if depth == 0 || len(lines) == 0 {
		return lines
	}
	pad := strings.Repeat("  ", depth)
	for i := range lines {
		lines[i] = pad + lines[i]
	}
	return lines
}

// checkDefersToTests reports whether a check's failures are test cases. When
// they are, the global TESTS block renders them per-test (with rolled-up logs),
// so the checks section leaves the failure detail to it rather than dumping the
// check's own command output.
func (fe *frontendPretty) checkDefersToTests(span *dagui.Span) bool {
	return len(failingLeafTestCases(fe.db.TestViewForSpan(span))) > 0
}

// checkHasTests reports whether a check ran any test cases, so a passing leaf
// check can nest their rollup and keep its tests attributed to it.
func (fe *frontendPretty) checkHasTests(span *dagui.Span) bool {
	return fe.db.TestViewForSpan(span).HasTests()
}

// eachFailedLeafCheck visits every surfaced check that failed and has no failed
// child -- i.e. the checks renderChecksSection renders an error cause for. Used
// to pre-fetch their logs before the single final render.
func eachFailedLeafCheck(nodes []*dagui.CheckNode, f func(*dagui.CheckNode)) {
	for _, n := range nodes {
		if n.Failed && !n.HasFailedChild() {
			f(n)
		}
		eachFailedLeafCheck(n.Children, f)
	}
}

// SurfacedFailedCheckSpans returns the span IDs of the surfaced failed leaf
// checks, so the report driver can fetch their subtrees on demand -- a failed
// check's cause is often a deep descendant outside the priority window. Failed
// *parent* checks are skipped: their subtree is the whole run, and they defer
// their detail to the failed children anyway.
func (fe *frontendPretty) SurfacedFailedCheckSpans() []dagui.SpanID {
	// Run on the event loop: SurfacedChecks/checkDefersToTests read (and lazily
	// rebuild) shared DB state -- the test index in particular -- which the
	// loader's dispatched ImportSnapshots is concurrently mutating. Touching it
	// directly from the run goroutine races ("concurrent map iteration and map
	// write"), so go through dispatch like every other DB access and block for
	// the result.
	var ids []dagui.SpanID
	done := make(chan struct{})
	fe.dispatch(func() {
		defer close(done)
		seen := map[dagui.SpanID]bool{}
		add := func(id dagui.SpanID) {
			if id.IsValid() && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		eachFailedLeafCheck(fe.db.SurfacedChecks(), func(n *dagui.CheckNode) {
			if fe.checkDefersToTests(n.Span) {
				// The TESTS block renders these, and its failing-test-case log fetch
				// already pulls their detail -- no need to fetch the check's subtree.
				return
			}
			// The check span's subtree (covers a cause that's a plain descendant).
			add(n.Span.ID)
			// The cause is often reached via a forward link instead -- a check
			// links to the lazy-eval span that did (and failed) the work, which the
			// subtree fetch doesn't descend into. Fetch those targets directly.
			for _, o := range n.Span.ErrorOrigins.Order {
				add(o.ID)
			}
			for _, l := range n.Span.Links {
				add(l.SpanContext.SpanID)
			}
		})
	})
	<-done
	return ids
}
