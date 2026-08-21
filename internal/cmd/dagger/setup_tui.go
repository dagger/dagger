package daggercmd

import (
	"fmt"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/vito/tuist"
)

// setupUI is the command-side controller for SetupView. Every mutation is
// dispatched through the frontend so the model and Tuist render on the same
// goroutine.
type setupUI struct {
	view   *setupView
	handle idtui.ViewHandle
}

func newSetupUI(frontend idtui.Frontend) *setupUI {
	host, ok := frontend.(idtui.CommandFrontend)
	if !ok {
		return nil
	}
	view := &setupView{}
	ui := &setupUI{view: view}
	ui.handle = host.SetView(func(ctx idtui.ViewContext) idtui.CommandView {
		view.workspace = ctx.SpanList(
			func() dagui.SpanID { return view.rootID },
			func() []dagui.SpanID { return validSpanIDs(view.migrationID) },
		)
		view.recommend = ctx.SpanList(
			func() dagui.SpanID { return view.rootID },
			func() []dagui.SpanID {
				if len(view.installIDs) > 0 {
					return append([]dagui.SpanID(nil), view.installIDs...)
				}
				return validSpanIDs(view.recommendID)
			},
		)
		return view
	})
	return ui
}

func validSpanIDs(ids ...dagui.SpanID) []dagui.SpanID {
	valid := ids[:0]
	for _, id := range ids {
		if id.IsValid() {
			valid = append(valid, id)
		}
	}
	return valid
}

func (ui *setupUI) update(fn func(*setupView)) {
	if ui == nil {
		return
	}
	ui.handle.Update(func() { fn(ui.view) })
}

func (ui *setupUI) setRoot(id dagui.SpanID) {
	ui.update(func(view *setupView) { view.rootID = id })
}

func (ui *setupUI) setMigration(id dagui.SpanID) {
	ui.update(func(view *setupView) { view.migrationID = id })
}

func (ui *setupUI) setMigrationMessage(message string) {
	ui.update(func(view *setupView) { view.migrationMessage = message })
}

func (ui *setupUI) setRecommend(id dagui.SpanID) {
	ui.update(func(view *setupView) { view.recommendID = id })
}

func (ui *setupUI) setRecommendMessage(message string) {
	ui.update(func(view *setupView) { view.recommendMessage = message })
}

func (ui *setupUI) addInstall(id dagui.SpanID, name string) {
	ui.update(func(view *setupView) {
		view.installIDs = append(view.installIDs, id)
		view.installed = append(view.installed, name)
	})
}

func (ui *setupUI) complete() {
	ui.update(func(view *setupView) { view.complete = true })
}

func (ui *setupUI) fail(err error) {
	ui.update(func(view *setupView) { view.failure = err.Error() })
}

type setupView struct {
	tuist.Compo

	final bool

	rootID           dagui.SpanID
	migrationID      dagui.SpanID
	recommendID      dagui.SpanID
	installIDs       []dagui.SpanID
	installed        []string
	complete         bool
	failure          string
	workspace        *idtui.SpanListView
	recommend        *idtui.SpanListView
	migrationMessage string
	recommendMessage string
}

var _ idtui.CommandView = (*setupView)(nil)
var _ tuist.Interactive = (*setupView)(nil)

func (view *setupView) SetFinal(final bool) {
	if view.final != final {
		view.final = final
		view.Update()
	}
}

func (view *setupView) Render(ctx tuist.Context) {
	if view.final {
		view.renderFinal(ctx)
		return
	}

	ctx.Line("Setting up this workspace")
	ctx.Line("")
	ctx.Line("Workspace")
	if view.workspace != nil {
		view.RenderChild(ctx, view.workspace)
	}
	lines(ctx, view.migrationMessage)

	if view.recommendID.IsValid() || len(view.installIDs) > 0 || view.recommendMessage != "" {
		ctx.Line("")
		ctx.Line("Recommended modules")
		if view.recommend != nil {
			view.RenderChild(ctx, view.recommend)
		}
		lines(ctx, view.recommendMessage)
	}
}

func (view *setupView) HandleKeyPress(ctx tuist.Context, ev uv.KeyPressEvent) bool {
	key := uv.Key(ev).String()
	switch key {
	case "down", "j":
		if view.workspace != nil && view.workspace.Focused() {
			if view.workspace.HandleKeyPress(ctx, ev) {
				return true
			}
			return view.recommend != nil && view.recommend.FocusFirst()
		}
		if view.recommend != nil && view.recommend.HandleKeyPress(ctx, ev) {
			return true
		}
		return view.workspace != nil && view.workspace.FocusFirst()
	case "up", "k":
		if view.recommend != nil && view.recommend.Focused() {
			if view.recommend.HandleKeyPress(ctx, ev) {
				return true
			}
			return view.workspace != nil && view.workspace.FocusLast()
		}
		if view.workspace != nil && view.workspace.HandleKeyPress(ctx, ev) {
			return true
		}
		return view.recommend != nil && view.recommend.FocusLast()
	case "home":
		return view.workspace != nil && view.workspace.FocusFirst()
	case "end", "G":
		return view.recommend != nil && view.recommend.FocusLast()
	case "left", "h", "right", "l", "enter":
		if view.recommend != nil && view.recommend.Focused() {
			return view.recommend.HandleKeyPress(ctx, ev)
		}
		return view.workspace != nil && view.workspace.HandleKeyPress(ctx, ev)
	}
	return false
}

func (view *setupView) renderFinal(ctx tuist.Context) {
	if view.migrationMessage != "" {
		lines(ctx, view.migrationMessage)
	}
	if len(view.installed) > 0 {
		ctx.Line("")
		ctx.Line("Installed " + strings.Join(view.installed, ", ") + ".")
	} else if view.recommendMessage != "" {
		ctx.Line("")
		lines(ctx, view.recommendMessage)
	}
	ctx.Line("")
	if view.complete {
		ctx.Line("Setup complete.")
	} else if view.failure != "" {
		ctx.Line(fmt.Sprintf("Setup failed: %s", view.failure))
	} else {
		ctx.Line("Setup did not complete.")
	}
}

func lines(ctx tuist.Context, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	ctx.Lines(strings.Split(text, "\n")...)
}
