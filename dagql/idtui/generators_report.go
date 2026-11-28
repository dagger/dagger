package idtui

import (
	"fmt"
	"io"
	"strings"

	"github.com/muesli/termenv"
	"github.com/vito/tuist"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/util/patchpreview"
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
	labels := fe.generatorLabels(roots)
	for _, root := range roots {
		fe.renderGeneratorNode(ctx, out, r, root, labels, 0)
	}
	return strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
}

// renderGeneratorNode renders one surfaced generator at the given depth: its
// status line, a failed leaf's inline error cause, and -- when it has nested
// generators -- a nested GENERATORS header followed by the children one level
// under it, mirroring renderCheckNode.
func (fe *frontendPretty) renderGeneratorNode(ctx tuist.Context, out TermOutput, r *renderer, node *dagui.GeneratorNode, labels artifactLabels, depth int) {
	indent := strings.Repeat("  ", depth)
	fmt.Fprintln(out, fe.generatorStatusLine(out, r, node, labels, indent))

	// A generator that changed files lists them, with their added and
	// removed lines: what `dagger generate` did, or would do.
	if changes := node.Changes(); changes != nil {
		width := 80
		if ctx.Width > 0 {
			width = min(width, ctx.Width-len(indent)-2)
		}
		for _, line := range fe.generatorChangeLines(changes, width) {
			fmt.Fprintf(out, "%s  %s\n", indent, line)
		}
	}

	// A failed leaf generator renders its failure inline; a failed parent
	// defers to the failed children that explain it. Explicit error-origin
	// links (a lazy-eval span that did the failing work elsewhere) render like
	// a check's causes; otherwise the generator span itself carries the detail
	// -- the CLI reports the exec error and its saved output on that span.
	// Hunting descendants
	// instead would repeat the generator's own row and surface runtime
	// internals (e.g. exec.processRun's runc exit status).
	if node.Failed() && !node.HasFailedChild() {
		renderedOrigin := false
		for _, origin := range node.Span.ErrorOrigins.Spans() {
			// A cached error can point to a successful transport span, or an
			// origin that this trace did not receive. Neither carries the error.
			if !origin.Received || !hasSpanErrorMessage(origin) {
				continue
			}
			fe.renderCauseDetail(ctx, out, r, origin, depth+1)
			renderedOrigin = true
		}
		if !renderedOrigin {
			fe.renderSpanFailureDetail(ctx, out, r, node.Span, depth+1)
		}
	}

	if len(node.Children) > 0 {
		subIndent := strings.Repeat("  ", depth+1)
		fmt.Fprintf(out, "%s%s\n", subIndent, generatorsHeaderLine(out, fe.agentStyle(), node.Children))
		childLabels := fe.generatorLabels(node.Children)
		for _, child := range node.Children {
			fe.renderGeneratorNode(ctx, out, r, child, childLabels, depth+2)
		}
	}
}

// renderSpanFailureDetail renders a failed check or generator span's own
// rolled-up logs and error beneath its status line -- renderCauseDetail minus
// the title row, since the status line above already names the span.
func (fe *frontendPretty) renderSpanFailureDetail(ctx tuist.Context, out TermOutput, r *renderer, span *dagui.Span, depth int) {
	indent := strings.Repeat("  ", depth)
	row := &dagui.TraceRow{Span: span, Expanded: true, Depth: depth}
	fe.requestLogsOnRender(span.ID)
	if logs := fe.logs.Logs[span.ID]; logs != nil && !fe.claims.hasLog(span.ID) {
		defer fe.fitFramedLogs(ctx, logs)()
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
// yellow ◐ / green ✔), label (see artifactLabel), and faint duration, at the
// given indent.
func (fe *frontendPretty) generatorStatusLine(out TermOutput, r *renderer, node *dagui.GeneratorNode, labels artifactLabels, indent string) string {
	icon, color, status := generatorStatus(node)
	dur := dagui.FormatDuration(node.Span.Activity.Duration(r.now))
	return fmt.Sprintf("%s%s %s %s %s",
		indent,
		out.String(icon).Foreground(color).String(),
		labels.of(node.Name),
		out.String(dur).Faint().String(),
		out.String(status).Foreground(color).String(),
	)
}

// generatorChangedColor marks a generator that changed files: it succeeded,
// so not the failure color, but did more than pass.
const generatorChangedColor = termenv.ANSICyan

// generatorStatus is surfacedSpanStatus for a generator, which reports
// CHANGED instead of OK when it produced changes.
func generatorStatus(node *dagui.GeneratorNode) (icon string, color termenv.Color, status string) {
	icon, color, status = surfacedSpanStatus(node.Span)
	if status == "OK" && node.Changes() != nil {
		return IconSuccess, generatorChangedColor, "CHANGED"
	}
	return icon, color, status
}

// generatorChangeLines lists a generator's changed files as a diff stat, at
// most width wide (see patchpreview.WriteRows).
func (fe *frontendPretty) generatorChangeLines(changes *dagui.GeneratorChanges, width int) []string {
	entries := make([]patchpreview.Entry, len(changes.Entries))
	for i, change := range changes.Entries {
		entries[i] = patchpreview.Entry{
			Path: change.Path, OldPath: change.OldPath, Kind: change.Kind,
			Added: change.Added, Removed: change.Removed,
		}
	}
	buf := new(strings.Builder)
	patchpreview.WriteRows(NewOutput(buf, termenv.WithProfile(fe.profile)), entries, changes.Files, max(width, 20))
	return strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
}

// generatorLabels labels the generators listed together (see
// siblingLabels).
func (fe *frontendPretty) generatorLabels(nodes []*dagui.GeneratorNode) artifactLabels {
	spans := make([]*dagui.Span, len(nodes))
	names := make([]string, len(nodes))
	for i, node := range nodes {
		spans[i], names[i] = node.Span, node.Name
	}
	return fe.siblingLabels(spans, names)
}

// generatorsHeaderLine renders a "GENERATORS" heading with the failed/passed
// tally for the given generators joined onto the same line, in visual lockstep
// with the CHECKS and TESTS headers. The nodes are the generators listed
// directly beneath this header -- a level -- so the tally agrees with what's
// rendered right under it.
//
// A generator that changed files counts as changed rather than passed, right
// after the failed ones: what a run did comes before what it left alone.
func generatorsHeaderLine(out TermOutput, agent bool, nodes []*dagui.GeneratorNode) string {
	line := reportHeadingLine(out, agent, "GENERATORS")
	var spans []*dagui.Span
	changed := 0
	for _, n := range nodes {
		if _, _, status := generatorStatus(n); status == "CHANGED" {
			changed++
			continue
		}
		spans = append(spans, n.Span)
	}
	counts := surfacedSpanCounts(spans)
	parts := renderTestCountParts(out, dagui.TestCounts{Failing: counts.Failing})
	if changed > 0 {
		parts = append(parts, out.String(fmt.Sprintf("%s %d changed", IconSuccess, changed)).Foreground(generatorChangedColor).String())
	}
	counts.Failing = 0
	parts = append(parts, renderTestCountParts(out, counts)...)
	for _, part := range parts {
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
	prefix := s.inlineRollupPrefix(r, row)
	body := fe.generatorsRollupLines(s.inlineRollupContext(ctx, prefix), r, nodes, s.inlineRollupLimit(ctx))
	return s.frameInlineRollup(prefix, body)
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
	labels := fe.generatorLabels(nodes)
	detail := fe.withForkedClaims(func() {
		for _, node := range nodes {
			fe.renderGeneratorNode(ctx, bodyOut, r, node, labels, 1)
		}
	})
	for _, node := range nodes {
		statuses = append(statuses, fe.generatorStatusLine(out, r, node, labels, "  "))
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
