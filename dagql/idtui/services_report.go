package idtui

import (
	"fmt"
	"strings"

	"github.com/muesli/termenv"
	"github.com/vito/tuist"

	"github.com/dagger/dagger/dagql/dagui"
)

// servicesReport renders the SERVICES heading plus the surfaced service
// instances for the root --full report: every service that ran (or is still
// running), with its hostname, its command line, its state, and the span
// handle for tailing its logs (ReadLogs) or zooming to it in the TUI console.
// The section exists for AI agents — an agent driving the CLI often sees only
// this report, so service addresses and span handles here are load-bearing,
// while a human already watched the service run in the tree above; the caller
// gates it on RunningInAgent. Services are auxiliary context, not the main
// event, so unlike checks or the conversation this section never replaces the
// progress tree — it renders after the main rows. Returns nil when zoomed
// (the zoom views render themselves) or when nothing surfaces.
func (fe *frontendPretty) servicesReport(_ tuist.Context, r *renderer, zoomed bool) []string {
	if zoomed {
		return nil
	}
	roots := fe.reportServices()
	if len(roots) == 0 {
		return nil
	}

	buf := new(strings.Builder)
	out := NewOutput(buf, termenv.WithProfile(fe.profile))
	for _, root := range roots {
		fe.renderServiceNode(out, r, root, 0)
	}
	body := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")

	hdrOut := NewOutput(new(strings.Builder), termenv.WithProfile(fe.profile))
	return append([]string{reportHeadingLine(hdrOut, fe.agentStyle(), "SERVICES")}, body...)
}

// inlineServiceNodes returns the services an LLM tool call started, for its
// inline SERVICES rollup -- the service analog of inlineCheckNodes' tool case.
// The tool call's boundary keeps them out of the trace-level SERVICES section,
// yet a service often outlives the call that started it, so its hostname and
// state are worth keeping in view. A nested worker's tool calls keep their own.
func (fe *frontendPretty) inlineServiceNodes(row *dagui.TraceRow) []*dagui.ServiceNode {
	if row == nil || row.Span == nil || row.Span.LLMTool == "" {
		return nil
	}
	if row.Expanded && !fe.finalRender {
		return nil
	}
	return fe.db.SurfacedServicesForSpan(row.Span)
}

// renderInlineServices renders a tool call row's inline SERVICES rollup,
// shaped and condensed like renderInlineChecks.
func (s *SpanTreeView) renderInlineServices(ctx tuist.Context, r *renderer, row *dagui.TraceRow) []string {
	nodes := s.fe.inlineServiceNodes(row)
	if len(nodes) == 0 {
		return nil
	}
	return s.frameInlineRollup(r, row, s.fe.servicesRollupLines(r, nodes, s.inlineRollupLimit(ctx)))
}

// renderMessageServices is renderMessageChecks for the services an LLM tool
// call started.
func (fe *frontendPretty) renderMessageServices(_ tuist.Context, r *renderer, span *dagui.Span) []string {
	if span == nil || span.LLMTool == "" {
		return nil
	}
	nodes := fe.db.SurfacedServicesForSpan(span)
	if len(nodes) == 0 {
		return nil
	}
	return fe.servicesRollupLines(r, nodes, 0)
}

// servicesRollupLines builds the inline rollup body for a list of services: a
// SERVICES header followed by each service's status line, with the services
// started within it nested beneath. Condensed to height like
// checksRollupLines, keeping the top-level services when nested ones don't fit.
func (fe *frontendPretty) servicesRollupLines(r *renderer, nodes []*dagui.ServiceNode, height int) []string {
	out := NewOutput(new(strings.Builder), termenv.WithProfile(fe.profile))
	header := reportHeadingLine(out, fe.agentStyle(), "SERVICES")
	bodyBuf := new(strings.Builder)
	bodyOut := NewOutput(bodyBuf, termenv.WithProfile(fe.profile))
	statuses := make([]string, 0, len(nodes))
	for _, node := range nodes {
		fe.renderServiceNode(bodyOut, r, node, 1)
		statuses = append(statuses, fe.serviceStatusLine(out, r, node, "  "))
	}
	lines, _ := condenseRollup(out, header, bodyBuf.String(), statuses, height)
	return lines
}

// renderServiceNode renders one surfaced service at the given depth: its
// status line, then any services started within it one level under.
func (fe *frontendPretty) renderServiceNode(out TermOutput, r *renderer, node *dagui.ServiceNode, depth int) {
	fmt.Fprintln(out, fe.serviceStatusLine(out, r, node, strings.Repeat("  ", depth)))
	for _, child := range node.Children {
		fe.renderServiceNode(out, r, child, depth+1)
	}
}

// serviceStatusLine renders a service's one-line status: its state icon
// (running ● / exited ✔ / failed ✘), hostname, faint command line and
// uptime/duration, and the span ID — the handle for tailing the service's
// logs (ReadLogs) or zooming to it in the TUI console.
func (fe *frontendPretty) serviceStatusLine(out TermOutput, r *renderer, node *dagui.ServiceNode, indent string) string {
	icon, color, status := IconSuccess, termenv.ANSIGreen, "EXITED"
	switch {
	case node.Span.IsFailed():
		icon, color, status = IconFailure, termenv.ANSIRed, "ERROR"
	case node.Span.IsRunning():
		icon, color, status = DotFilled, termenv.ANSIYellow, "RUNNING"
	}

	parts := []string{
		indent + out.String(icon).Foreground(color).String(),
		node.Name(),
	}
	// The exec span's own name is the service's command line ("exec httpd -v
	// ..."), which identifies the instance far better than the API call that
	// produced it; skip it when it already served as the display name above
	// (no hostname).
	if execName := node.Span.Name; execName != node.Name() {
		parts = append(parts, out.String(execName).Faint().String())
	}
	dur := dagui.FormatDuration(node.Span.Activity.Duration(r.now))
	if !node.Span.IsRunning() || fe.finalRender {
		// A live row only repaints when spans arrive, and a running service
		// sends none while it runs, so its uptime would sit frozen; RUNNING
		// alone says what matters.
		parts = append(parts, out.String(dur).Faint().String())
	}
	parts = append(parts, out.String(status).Foreground(color).String())
	if fe.agentStyle() {
		parts = append(parts, out.String("span="+node.Span.ID.String()).Faint().String())
	}
	return strings.Join(parts, " ")
}
