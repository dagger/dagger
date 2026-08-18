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
	parts = append(parts,
		out.String(dur).Faint().String(),
		out.String(status).Foreground(color).String(),
		out.String("span="+node.Span.ID.String()).Faint().String(),
	)
	return strings.Join(parts, " ")
}
