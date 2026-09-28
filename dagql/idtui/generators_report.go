package idtui

import (
	"fmt"
	"io"
	"strings"

	"github.com/muesli/termenv"
	"github.com/vito/tuist"

	"github.com/dagger/dagger/dagql/dagui"
)

// generatorsReport renders the GENERATORS heading plus the surfaced-generator
// list for the root --full report -- the `dagger generate` analog of
// checksReport. It returns nil when zoomed (the zoom views handle their own
// rendering) or when nothing surfaces (e.g. a plain trace, or one whose only
// generators are boundary-contained test fixtures), so the caller can fall back
// to the progress tree.
func (fe *frontendPretty) generatorsReport(ctx tuist.Context, r *renderer, zoomed bool) []string {
	if zoomed {
		return nil
	}
	genLines := fe.renderGeneratorsSection(ctx, r)
	if len(genLines) == 0 {
		return nil
	}
	out := NewOutput(io.Discard, termenv.WithProfile(fe.profile))
	header := generatorsHeaderLine(out, fe.agentStyle(), fe.reportGenerators())
	return append([]string{header}, genLines...)
}

// renderGeneratorsSection renders the trace's generators for the final report,
// independent of the `reveal` mechanism: every surfaced generator (see
// DB.SurfacedGenerators) is listed, nested under its parent generator, with
// passing generators kept to a single line and failed generators carrying their
// inline error cause -- the same detail the live tree shows on a failed row.
func (fe *frontendPretty) renderGeneratorsSection(ctx tuist.Context, r *renderer) []string {
	roots := fe.reportGenerators()
	if len(roots) == 0 {
		return nil
	}

	buf := new(strings.Builder)
	out := NewOutput(buf, termenv.WithProfile(fe.profile))
	for _, root := range roots {
		fe.renderGeneratorNode(ctx, out, r, root, 0)
	}
	return strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
}

// renderGeneratorNode renders one surfaced generator at the given depth: its
// status line, a failed leaf's inline error cause, and -- when it has nested
// generators -- a nested GENERATORS header followed by the children one level
// under it, mirroring renderCheckNode.
func (fe *frontendPretty) renderGeneratorNode(ctx tuist.Context, out TermOutput, r *renderer, node *dagui.GeneratorNode, depth int) {
	indent := strings.Repeat("  ", depth)
	fmt.Fprintln(out, fe.generatorStatusLine(out, r, node, indent))

	// A failed leaf generator renders its failure inline; a failed parent
	// defers to the failed children that explain it. Explicit error-origin
	// links (a lazy-eval span that did the failing work elsewhere) render like
	// a check's causes; otherwise the generator span itself carries the detail
	// -- its changeset is forced inside the span (see runGeneratorLocally), so
	// its rolled-up logs and error ARE the exec failure. Hunting descendants
	// instead would repeat the generator's own row and surface runtime
	// internals (e.g. exec.processRun's runc exit status).
	if node.Failed() && !node.HasFailedChild() {
		if origins := node.Span.ErrorOrigins.Order; len(origins) > 0 {
			for _, origin := range origins {
				if !origin.Received {
					// Incremental --full may not have loaded the origin (or its
					// logs); skip rather than render an empty stub.
					continue
				}
				fe.renderCauseDetail(ctx, out, r, origin, depth+1)
			}
		} else {
			fe.renderGeneratorFailureDetail(out, r, node.Span, depth+1)
		}
	}

	if len(node.Children) > 0 {
		subIndent := strings.Repeat("  ", depth+1)
		fmt.Fprintf(out, "%s%s\n", subIndent, generatorsHeaderLine(out, fe.agentStyle(), node.Children))
		for _, child := range node.Children {
			fe.renderGeneratorNode(ctx, out, r, child, depth+2)
		}
	}
}

// renderGeneratorFailureDetail renders a failed generator span's own rolled-up
// logs and error beneath its status line -- renderCauseDetail minus the title
// row, since the generator's status line above already names the span.
func (fe *frontendPretty) renderGeneratorFailureDetail(out TermOutput, r *renderer, span *dagui.Span, depth int) {
	indent := strings.Repeat("  ", depth)
	row := &dagui.TraceRow{Span: span, Expanded: true, Depth: depth}
	fe.requestLogsOnRender(span.ID)
	if logs := fe.logs.Logs[span.ID]; logs != nil && !fe.claims.hasLog(span.ID) {
		pipe := out.String(VertBoldBar).Foreground(restrainedStatusColor(span)).String()
		logs.SetPrefix(indent + pipe + " ")
		logs.SetHeight(logs.UsedHeight())
		fmt.Fprint(out, logs.View())
		fe.claims.claimLog(span)
	}
	fe.renderStepError(out, r, row, indent)
	fe.claims.claimError(span)
}

// generatorStatusLine renders a generator's one-line status: its icon (red ✘ /
// yellow ◐ / green ✔), name, and faint duration, at the given indent.
func (fe *frontendPretty) generatorStatusLine(out TermOutput, r *renderer, node *dagui.GeneratorNode, indent string) string {
	icon, color, status := surfacedSpanStatus(node.Span)
	dur := dagui.FormatDuration(node.Span.Activity.Duration(r.now))
	return fmt.Sprintf("%s%s %s %s %s",
		indent,
		out.String(icon).Foreground(color).String(),
		node.Name,
		out.String(dur).Faint().String(),
		out.String(status).Foreground(color).String(),
	)
}

// generatorsHeaderLine renders a "GENERATORS" heading with the failed/passed
// tally for the given generators joined onto the same line, in visual lockstep
// with the CHECKS and TESTS headers. The nodes are the generators listed
// directly beneath this header -- a level -- so the tally agrees with what's
// rendered right under it.
func generatorsHeaderLine(out TermOutput, agent bool, nodes []*dagui.GeneratorNode) string {
	line := reportHeadingLine(out, agent, "GENERATORS")
	spans := make([]*dagui.Span, len(nodes))
	for i, n := range nodes {
		spans[i] = n.Span
	}
	for _, part := range renderTestCountParts(out, surfacedSpanCounts(spans)) {
		line += "  " + part
	}
	return line
}

// inlineGeneratorNodes returns the generators an LLM tool call ran, for its
// inline GENERATORS rollup -- the generator analog of inlineCheckNodes' tool
// case. The tool call's boundary keeps them out of the trace-level GENERATORS
// section, and a nested worker's tool calls keep their own.
func (fe *frontendPretty) inlineGeneratorNodes(row *dagui.TraceRow) []*dagui.GeneratorNode {
	if row == nil || row.Span == nil || row.Span.LLMTool == "" {
		return nil
	}
	if row.Expanded && !fe.finalRender {
		return nil
	}
	return fe.db.SurfacedGeneratorsForSpan(row.Span)
}

// renderInlineGenerators renders a tool call row's inline GENERATORS rollup,
// shaped and condensed like renderInlineChecks.
func (s *SpanTreeView) renderInlineGenerators(ctx tuist.Context, r *renderer, row *dagui.TraceRow) []string {
	fe := s.fe
	nodes := fe.inlineGeneratorNodes(row)
	if len(nodes) == 0 {
		return nil
	}
	body := fe.generatorsRollupLines(ctx, r, nodes, s.inlineRollupLimit(ctx))
	return s.frameInlineRollup(r, row, body)
}

// generatorsRollupLines builds the inline rollup body for a list of
// generators: a GENERATORS header followed by the generators, condensed to
// height like checksRollupLines.
func (fe *frontendPretty) generatorsRollupLines(ctx tuist.Context, r *renderer, nodes []*dagui.GeneratorNode, height int) []string {
	out := NewOutput(io.Discard, termenv.WithProfile(fe.profile))
	header := generatorsHeaderLine(out, fe.agentStyle(), nodes)

	bodyBuf := new(strings.Builder)
	bodyOut := NewOutput(bodyBuf, termenv.WithProfile(fe.profile))
	statuses := make([]string, 0, len(nodes))
	detail := fe.withForkedClaims(func() {
		for _, node := range nodes {
			fe.renderGeneratorNode(ctx, bodyOut, r, node, 1)
		}
	})
	for _, node := range nodes {
		statuses = append(statuses, fe.generatorStatusLine(out, r, node, "  "))
	}
	lines, full := condenseRollup(out, header, bodyBuf.String(), statuses, height)
	if full {
		detail.commit()
	}
	return lines
}

// eachFailedLeafGenerator visits every surfaced generator that failed and has
// no failed child -- i.e. the generators renderGeneratorsSection renders an
// error cause for. Used to pre-fetch their logs before the single final render.
func eachFailedLeafGenerator(nodes []*dagui.GeneratorNode, f func(*dagui.GeneratorNode)) {
	for _, n := range nodes {
		if n.Failed() && !n.HasFailedChild() {
			f(n)
		}
		eachFailedLeafGenerator(n.Children, f)
	}
}
