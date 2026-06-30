package idtui

import (
	"fmt"
	"io"
	"strings"

	"github.com/muesli/termenv"
	"github.com/vito/tuist"

	"github.com/dagger/dagger/dagql/dagui"
)

const (
	// inlineChecksChromeReserve is the height the inline CHECKS rollup leaves for
	// the check row's own title, the pipe gap above the rollup, and the keymap
	// bar, so the rollup condenses to fit the viewport instead of being
	// tail-cropped (which would drop the failing sub-checks that matter most).
	inlineChecksChromeReserve = 3
	// minInlineChecksRollupHeight is the floor the rollup never condenses below:
	// the CHECKS header alone, whose tally is the at-a-glance outcome.
	minInlineChecksRollupHeight = 1
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
	for _, root := range roots {
		fe.renderCheckNode(ctx, out, r, root, 0)
	}
	return strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
}

// renderCheckNode renders one surfaced check at the given depth: its status
// line, the inline detail it carries (a failed leaf's error cause, or a leaf's
// test rollup), and -- when it has sub-checks -- a nested CHECKS header followed
// by the children one level under it. It writes to out and is shared by the
// final report (renderChecksSection) and the live inline rollup
// (renderInlineChecks), so both surface sub-checks identically.
func (fe *frontendPretty) renderCheckNode(ctx tuist.Context, out TermOutput, r *renderer, node *dagui.CheckNode, depth int) {
	indent := strings.Repeat("  ", depth)
	fmt.Fprintln(out, fe.checkStatusLine(out, r, node, indent))

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
				fmt.Fprintln(out, line)
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
			fmt.Fprintln(out, line)
		}
	}

	// Sub-checks get their own CHECKS header (with this check's direct-children
	// tally), the way a check's tests get a TESTS header, then nest one level
	// under it.
	if len(node.Children) > 0 {
		subIndent := strings.Repeat("  ", depth+1)
		fmt.Fprintf(out, "%s%s\n", subIndent, checksHeaderLine(out, node.Children))
		for _, child := range node.Children {
			fe.renderCheckNode(ctx, out, r, child, depth+2)
		}
	}
}

// checkStatusLine renders a check's one-line status: its icon (red ✘ / green ✔),
// name, and faint duration, at the given indent.
func (fe *frontendPretty) checkStatusLine(out TermOutput, r *renderer, node *dagui.CheckNode, indent string) string {
	icon, color := IconSuccess, termenv.ANSIGreen
	if node.Failed {
		icon, color = IconFailure, termenv.ANSIRed
	}
	dur := dagui.FormatDuration(node.Span.Activity.Duration(r.now))
	return fmt.Sprintf("%s%s %s %s",
		indent,
		out.String(icon).Foreground(color).String(),
		node.Name,
		out.String(dur).Faint().String(),
	)
}

// checkNodeForSpan returns the surfaced CheckNode for a span (matched by check
// name, which SurfacedChecks dedups globally), or nil if the span isn't a
// surfaced check. Used to find a check row's sub-checks for the inline rollup.
func (fe *frontendPretty) checkNodeForSpan(span *dagui.Span) *dagui.CheckNode {
	if span == nil || span.CheckName == "" {
		return nil
	}
	var find func(ns []*dagui.CheckNode) *dagui.CheckNode
	find = func(ns []*dagui.CheckNode) *dagui.CheckNode {
		for _, n := range ns {
			if n.Name == span.CheckName {
				return n
			}
			if hit := find(n.Children); hit != nil {
				return hit
			}
		}
		return nil
	}
	return find(fe.db.SurfacedChecks())
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

// shouldRenderInlineChecks reports whether a check row should show its
// sub-checks as an inline CHECKS rollup. Only in the live tree (the report path
// renders checks via renderChecksSection) and only while collapsed -- expanding
// the row reveals the sub-checks as their own tree rows instead, the same
// progressive disclosure inline tests use.
func (fe *frontendPretty) shouldRenderInlineChecks(row *dagui.TraceRow) bool {
	if fe.finalRender || row.Expanded {
		return false
	}
	node := fe.checkNodeForSpan(row.Span)
	return node != nil && len(node.Children) > 0
}

// renderInlineChecks renders a collapsed check row's sub-checks as an inline
// rollup -- a CHECKS header and the sub-checks beneath it, the same shape a
// check's tests get -- so a parent check like `ci:bootstrap` surfaces the checks
// nested under it instead of just its own orchestrating command's failure. The
// rollup condenses to the viewport height (renderInlineChecks reads
// ScreenHeight, so it reflows on resize) and carries the row's tree pipe.
func (s *SpanTreeView) renderInlineChecks(ctx tuist.Context, r *renderer, row *dagui.TraceRow) []string {
	fe := s.fe
	if !fe.shouldRenderInlineChecks(row) {
		return nil
	}
	node := fe.checkNodeForSpan(row.Span)
	if node == nil || len(node.Children) == 0 {
		return nil
	}

	limit := 0
	if sh := ctx.ScreenHeight(); sh > 0 {
		limit = max(sh-inlineChecksChromeReserve, minInlineChecksRollupHeight)
	}
	body := fe.checksRollupLines(ctx, r, node.Children, limit)
	if len(body) == 0 {
		return nil
	}

	// Prefix every rollup line with the row's tree pipe, like renderInlineTests.
	prefixBuf := new(strings.Builder)
	prefixOut := NewOutput(prefixBuf, termenv.WithProfile(fe.profile))
	r.indentFunc = s.indentFunc(prefixOut)
	r.fancyIndent(prefixOut, row, false, false)
	pipe := prefixOut.String(VertBoldBar).Foreground(restrainedStatusColor(row.Span))
	if s.focused {
		pipe = hl(pipe)
	}
	fmt.Fprint(prefixOut, pipe.String())
	fmt.Fprint(prefixOut, " ")
	prefix := prefixBuf.String()

	lines := make([]string, 0, len(body)+1)
	lines = append(lines, strings.TrimRight(prefix, " ")) // pipe-only gap above the rollup
	for _, line := range body {
		lines = append(lines, prefix+line)
	}
	return lines
}

// checksRollupLines builds the inline rollup body for a check's sub-checks: a
// CHECKS header followed by the sub-checks. It condenses to fit height: full
// detail (each sub-check with its failure cause) when it fits, else names only,
// else as many names as fit with a "… N more …" marker, and never below the
// header alone -- whose tally is the at-a-glance outcome. height <= 0 means
// unbounded.
func (fe *frontendPretty) checksRollupLines(ctx tuist.Context, r *renderer, children []*dagui.CheckNode, height int) []string {
	out := NewOutput(io.Discard, termenv.WithProfile(fe.profile))
	header := checksHeaderLine(out, children)

	bodyBuf := new(strings.Builder)
	bodyOut := NewOutput(bodyBuf, termenv.WithProfile(fe.profile))
	for _, child := range children {
		fe.renderCheckNode(ctx, bodyOut, r, child, 1)
	}
	full := append([]string{header}, strings.Split(strings.TrimSuffix(bodyBuf.String(), "\n"), "\n")...)
	if height <= 0 || len(full) <= height {
		return full
	}

	// Drop the per-sub-check failure detail and keep one status line each.
	names := make([]string, 0, len(children)+1)
	names = append(names, header)
	for _, child := range children {
		names = append(names, fe.checkStatusLine(out, r, child, "  "))
	}
	if len(names) <= height {
		return names
	}

	// Even the names overflow: show as many as fit and mark the remainder. The
	// header alone (its tally) is the floor.
	if height <= 1 {
		return []string{header}
	}
	shown := max(height-2, 0) // header + "… N more …"
	lines := make([]string, 0, height)
	lines = append(lines, header)
	for _, child := range children[:shown] {
		lines = append(lines, fe.checkStatusLine(out, r, child, "  "))
	}
	lines = append(lines, fe.checksRollupMore(out, len(children)-shown))
	return lines
}

// checksRollupMore is the faint "… N more …" line shown when condensing drops
// sub-check rows that wouldn't fit.
func (fe *frontendPretty) checksRollupMore(out TermOutput, hidden int) string {
	return "  " + out.String(fmt.Sprintf("… %d more …", hidden)).Foreground(termenv.ANSIBrightBlack).Faint().String()
}
