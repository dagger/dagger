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

		// A failed leaf check shows its error cause inline; a failed parent check
		// defers to the failed children that explain it.
		if node.Failed && !node.HasFailedChild() {
			row := &dagui.TraceRow{Span: node.Span, Expanded: true, Depth: depth + 1}
			for _, origin := range fe.checkRootCauses(node.Span) {
				if !origin.Received {
					// Incremental --full may not have loaded the origin (or its logs);
					// skip rather than render an empty stub.
					continue
				}
				fe.renderErrorCause(ctx, out, r, row, "", origin, fe)
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
