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
// running), with its hostname, the API call that produced it, and its state.
// Services are auxiliary context, not the main event, so unlike checks or the
// conversation this section never replaces the progress tree — the caller
// renders it after the main rows. Returns nil when zoomed (the zoom views
// render themselves) or when nothing surfaces.
func (fe *frontendPretty) servicesReport(_ tuist.Context, r *renderer, zoomed bool) []string {
	if zoomed {
		return nil
	}
	roots := fe.db.SurfacedServices()
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
	return append([]string{reportHeadingLine(hdrOut, "SERVICES")}, body...)
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
// (running ● / exited ✔ / failed ✘), hostname, the API call that produced it,
// and faint uptime/duration. Under an AI agent it appends the span ID — the
// handle for tailing the service's logs (ReadLogs) or zooming to it in the
// TUI console.
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
	if origin := node.Origin(); origin != nil {
		parts = append(parts, out.String("via "+origin.Name).Faint().String())
	}
	dur := dagui.FormatDuration(node.Span.Activity.Duration(r.now))
	parts = append(parts,
		out.String(dur).Faint().String(),
		out.String(status).Foreground(color).String(),
	)
	if RunningInAgent() {
		parts = append(parts, out.String("span="+node.Span.ID.String()).Faint().String())
	}
	return strings.Join(parts, " ")
}
