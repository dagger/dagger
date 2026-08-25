package daggercmd

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
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
	live   bool
}

type setupLoginState int

const (
	setupLoginPending setupLoginState = iota
	setupLoginComplete
	setupLoginSkipped
	setupLoginFailed
)

func newSetupUI(frontend idtui.Frontend) *setupUI {
	host, ok := frontend.(idtui.CommandFrontend)
	if !ok {
		return nil
	}
	view := &setupView{
		loginState:   setupLoginPending,
		loginMessage: "Checking Cloud account...",
		loginSpinner: tuist.NewSpinner(),
	}
	ui := &setupUI{view: view, live: host.Live()}
	ui.handle = host.SetView(func(ctx idtui.ViewContext) idtui.CommandView {
		view.workspace = ctx.SpanList(
			func() dagui.SpanID { return view.rootID },
			func() []dagui.SpanID { return validSpanIDs(view.migrationID) },
		)
		view.recommend = ctx.SpanList(
			func() dagui.SpanID { return view.rootID },
			func() []dagui.SpanID {
				// Discovery is an implementation detail: retain its span for
				// telemetry, but only render the resulting install actions.
				return append([]dagui.SpanID(nil), view.installIDs...)
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

func (ui *setupUI) setLoginPending(message string) {
	ui.update(func(view *setupView) {
		view.loginState = setupLoginPending
		view.loginMessage = message
	})
}

func (ui *setupUI) setLoginComplete(message string) {
	ui.update(func(view *setupView) {
		view.loginState = setupLoginComplete
		view.loginMessage = message
		view.loginDetail = ""
	})
}

func (ui *setupUI) setLoginSkipped(message string) {
	ui.update(func(view *setupView) {
		if view.loginState != setupLoginPending {
			return
		}
		view.loginState = setupLoginSkipped
		view.loginMessage = message
	})
}

func (ui *setupUI) setLoginFailed(err error) {
	ui.update(func(view *setupView) {
		view.loginState = setupLoginFailed
		view.loginMessage = err.Error()
	})
}

func (ui *setupUI) appendLoginDetail(detail string) {
	ui.update(func(view *setupView) { view.loginDetail += detail })
}

func (ui *setupUI) setMigration(id dagui.SpanID) {
	ui.update(func(view *setupView) { view.migrationID = id })
}

func (ui *setupUI) setMigrationMessage(message string) {
	ui.update(func(view *setupView) { view.migrationMessage = message })
}

func (ui *setupUI) setMigrationFinalMessage(message string) {
	ui.update(func(view *setupView) { view.migrationFinal = message })
}

func (ui *setupUI) setRecommend(id dagui.SpanID) {
	ui.update(func(view *setupView) { view.recommendID = id })
}

func (ui *setupUI) setRecommendMessage(message string) {
	ui.update(func(view *setupView) { view.recommendMessage = message })
}

func (ui *setupUI) addInstall(id dagui.SpanID) {
	ui.update(func(view *setupView) {
		view.installIDs = append(view.installIDs, id)
	})
}

func (ui *setupUI) addInstalled(name string) {
	ui.update(func(view *setupView) {
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

	final        bool
	loginState   setupLoginState
	loginMessage string
	loginDetail  string
	loginSpinner *tuist.Spinner

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
	migrationFinal   string
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

func (view *setupView) HideKeymap() bool {
	return view.loginState == setupLoginPending && view.loginMessage == "Waiting for authentication..."
}

func (view *setupView) Render(ctx tuist.Context) {
	if view.final {
		view.renderFinal(ctx)
		return
	}

	ctx.Line("Cloud account")
	view.renderLogin(ctx)
	if view.loginState != setupLoginPending {
		ctx.Line("")
		ctx.Line("Workspace")
		if view.migrationMessage == "" && view.migrationID.IsValid() && view.workspace != nil {
			view.RenderChild(ctx, view.workspace)
		}
		if view.migrationMessage == "Nothing to migrate." || view.migrationMessage == "No migration needed." {
			view.renderSuccess(ctx, view.migrationMessage)
		} else {
			lines(ctx, view.migrationMessage)
		}
	}

	if view.recommendID.IsValid() || len(view.installIDs) > 0 || view.recommendMessage != "" {
		ctx.Line("")
		ctx.Line("Recommended modules")
		if view.recommend != nil {
			view.RenderChild(ctx, view.recommend)
		}
		lines(ctx, view.recommendMessage)
	}
}

func (view *setupView) renderLogin(ctx tuist.Context) {
	switch view.loginState {
	case setupLoginPending:
		if view.loginMessage != "Waiting for login choice..." && view.loginMessage != "Waiting for authentication..." {
			view.loginSpinner.Label = view.loginMessage
			view.RenderChild(ctx, view.loginSpinner)
		}
	case setupLoginComplete:
		view.renderSuccess(ctx, view.loginMessage)
	case setupLoginSkipped:
		message := strings.ReplaceAll(
			view.loginMessage,
			"dagger login",
			lipgloss.NewStyle().Bold(true).Render("dagger login"),
		)
		canceled := lipgloss.NewStyle().Foreground(lipgloss.BrightBlack).Render(idtui.IconSkipped)
		ctx.Line("  " + canceled + " " + message)
	case setupLoginFailed:
		ctx.Line("✗ " + view.loginMessage)
	}
	if detail := strings.TrimSpace(view.loginDetail); detail != "" {
		ctx.Lines(strings.Split(detail, "\n")...)
	}
}

func (view *setupView) renderSuccess(ctx tuist.Context, message string) {
	check := lipgloss.NewStyle().Foreground(lipgloss.Green).Render("✔")
	ctx.Line("  " + check + " " + message)
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
	switch view.loginState {
	case setupLoginComplete:
		ctx.Line("Cloud account: " + view.loginMessage)
	case setupLoginSkipped:
		ctx.Line("Cloud account: " + view.loginMessage)
	case setupLoginFailed:
		ctx.Line("Cloud account: " + view.loginMessage)
	}
	migrationMessage := view.migrationMessage
	if view.migrationFinal != "" {
		migrationMessage = view.migrationFinal
	}
	if migrationMessage != "" {
		ctx.Line("")
	}
	if migrationMessage != "" {
		lines(ctx, migrationMessage)
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
