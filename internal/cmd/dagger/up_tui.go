package daggercmd

import (
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/vito/tuist"
)

// installUpView installs the command screen for `dagger up`: a run that is
// ABOUT the services it starts. The body is a ServiceList anchored at the
// CLI's zoom span — each per-service display span (rolled-up health-check and
// service logs, ready-URL chip) leads the screen instead of the setup tree.
// Service spans are ambient — any run that binds a service has one — so this
// screen is only ever installed by the command, never inferred from span
// data. Optional capability: streaming frontends show the plain stream
// instead. There is nothing to mutate after installation: the service list
// tracks the trace live on its own.
func installUpView(frontend idtui.Frontend, root dagui.SpanID) {
	host, ok := frontend.(idtui.CommandFrontend)
	if !ok {
		return
	}
	view := &upView{rootID: root}
	host.SetView(func(ctx idtui.ViewContext) idtui.CommandView {
		view.services = ctx.ServiceList(func() dagui.SpanID { return view.rootID })
		return view
	})
}

// upView is the body of the up command screen: just the service list. Final
// mode renders the same rows — each service with its ready URL — as the
// run's durable terminal output.
type upView struct {
	tuist.Compo

	final    bool
	rootID   dagui.SpanID
	services *idtui.SpanListView
}

var _ idtui.CommandView = (*upView)(nil)
var _ tuist.Interactive = (*upView)(nil)

func (view *upView) SetFinal(final bool) {
	if view.final != final {
		view.final = final
		view.Update()
	}
}

func (view *upView) Render(ctx tuist.Context) {
	if view.services != nil {
		view.RenderChild(ctx, view.services)
	}
}

func (view *upView) HandleKeyPress(ctx tuist.Context, ev uv.KeyPressEvent) bool {
	if view.services == nil {
		return false
	}
	switch uv.Key(ev).String() {
	case "down", "j", "up", "k", "home", "end", "G", "left", "h", "right", "l", "enter":
		return view.services.HandleKeyPress(ctx, ev)
	}
	return false
}
