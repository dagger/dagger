package daggercmd

import (
	"strings"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/vito/tuist"
	"go.opentelemetry.io/otel/trace"
)

// initUI is the command-side controller for initView. Every mutation is
// dispatched through the frontend so the model and Tuist render on the same
// goroutine. A nil *initUI is valid and does nothing, so streaming frontends
// and the standalone commands keep their plain output.
type initUI struct {
	view   *initView
	handle idtui.ViewHandle
}

// newInitUI installs the init screen in a live pretty frontend, or returns
// nil when the frontend cannot host one. root is the span the command's own
// spans start under. Only the spans the view selects from it are rendered, so
// session plumbing (connect, workspace queries, exports) stays in telemetry
// instead of on screen.
func newInitUI(frontend idtui.Frontend, root trace.Span, prefix string) *initUI {
	host, ok := frontend.(idtui.CommandFrontend)
	if !ok || !host.Live() {
		return nil
	}
	spinner := tuist.NewSpinner()
	spinnerStyle := lipgloss.NewStyle().Foreground(lipgloss.Yellow)
	// Indent the frame to line up with the status lines of finished steps.
	spinner.Style = func(frame string) string { return "  " + spinnerStyle.Render(frame) }
	view := &initView{
		rootID:  dagui.SpanID{SpanID: root.SpanContext().SpanID()},
		prefix:  prefix,
		spinner: spinner,
	}
	ui := &initUI{view: view}
	ui.handle = host.SetView(func(ctx idtui.ViewContext) idtui.CommandView {
		rootID := func() dagui.SpanID { return view.rootID }
		view.workspaceSpans = ctx.SpanList(rootID, view.workspaceSpanIDs)
		view.moduleSpans = ctx.SpanList(rootID, view.moduleSpanIDs)
		return view
	})
	return ui
}

func (ui *initUI) update(fn func(*initView)) {
	if ui == nil {
		return
	}
	ui.handle.Update(func() { fn(ui.view) })
}

func spanIDOf(span trace.Span) dagui.SpanID {
	return dagui.SpanID{SpanID: span.SpanContext().SpanID()}
}

// --- Workspace ---

func (ui *initUI) workspaceStarted(id dagui.SpanID) {
	ui.update(func(view *initView) {
		view.workspaceID = id
		view.workspace = initSection{state: initStepRunning}
	})
}

func (ui *initUI) workspaceDone(message string) {
	ui.update(func(view *initView) { view.workspace = initSection{state: initStepDone, message: message} })
}

func (ui *initUI) workspaceFailed(err error) {
	ui.update(func(view *initView) { view.workspace = initSection{state: initStepFailed, message: err.Error()} })
}

// --- Modules ---

func (ui *initUI) modulesStarted() {
	ui.update(func(view *initView) {
		view.modules = initSection{state: initStepRunning, message: "Finding recommended modules..."}
	})
}

func (ui *initUI) modulesFinding(id dagui.SpanID) {
	ui.update(func(view *initView) { view.findID = id })
}

// modulesFound lists what discovery matched while the selection form is open.
func (ui *initUI) modulesFound(recs []recommendation) {
	ui.update(func(view *initView) {
		view.findDone = true
		view.modules.message = ""
		view.found = nil
		for _, rec := range recs {
			view.found = append(view.found, rec.Module.Name+": found "+rec.Match)
		}
	})
}

func (ui *initUI) addInstall(id dagui.SpanID) {
	ui.update(func(view *initView) {
		view.found = nil
		view.installIDs = append(view.installIDs, id)
	})
}

func (ui *initUI) addInstalled(name string) {
	ui.update(func(view *initView) { view.installed = append(view.installed, name) })
}

func (ui *initUI) modulesInstalled() {
	ui.update(func(view *initView) {
		view.modules = initSection{state: initStepDone, message: installedMessage(view.installed)}
	})
}

func installedMessage(names []string) string {
	return "Installed " + strings.Join(names, ", ") + "."
}

func (ui *initUI) modulesDone(message string) {
	ui.update(func(view *initView) {
		view.findDone = true
		view.found = nil
		view.modules = initSection{state: initStepDone, message: message}
	})
}

func (ui *initUI) modulesSkipped(message string) {
	ui.update(func(view *initView) {
		view.found = nil
		view.modules = initSection{state: initStepSkipped, message: message}
	})
}

func (ui *initUI) modulesFailed(err error) {
	ui.update(func(view *initView) {
		view.found = nil
		view.modules = initSection{state: initStepFailed, message: err.Error()}
	})
}

// --- Cloud checks ---

func (ui *initUI) cloudChecking() {
	ui.update(func(view *initView) {
		view.cloud = initSection{state: initStepRunning, message: "Checking cloud checks..."}
	})
}

// cloudPrompting hides the section while the prompt below asks about it.
func (ui *initUI) cloudPrompting() {
	ui.update(func(view *initView) { view.cloud = initSection{} })
}

func (ui *initUI) cloudDone(message string) {
	ui.update(func(view *initView) { view.cloud = initSection{state: initStepDone, message: message} })
}

func (ui *initUI) cloudNext(message string) {
	ui.update(func(view *initView) { view.cloud = initSection{state: initStepNext, message: message} })
}

func (ui *initUI) cloudSkipped(message string) {
	ui.update(func(view *initView) { view.cloud = initSection{state: initStepSkipped, message: message} })
}

type initStepState int

const (
	initStepHidden initStepState = iota
	initStepRunning
	initStepDone
	initStepNext
	initStepSkipped
	initStepFailed
)

type initSection struct {
	state   initStepState
	message string
}

// initView is the body of `dagger init` in the pretty frontend: a section per
// step, each reduced to its outcome once it finishes. A step's spans are
// embedded only while it runs or after it fails, so slow or broken work can
// still be expanded and inspected. A step's section stays hidden while its
// prompt is open below the view.
type initView struct {
	tuist.Compo

	final  bool
	rootID dagui.SpanID
	// prefix is the dagger invocation for this workspace, for the commands
	// the summary suggests.
	prefix string

	workspaceID dagui.SpanID
	workspace   initSection

	modules    initSection
	findID     dagui.SpanID
	findDone   bool
	found      []string
	installIDs []dagui.SpanID
	installed  []string

	cloud initSection

	spinner        *tuist.Spinner
	workspaceSpans *idtui.SpanListView
	moduleSpans    *idtui.SpanListView
}

var _ idtui.CommandView = (*initView)(nil)
var _ tuist.Interactive = (*initView)(nil)

func (section initSection) showsSpans() bool {
	return section.state == initStepRunning || section.state == initStepFailed
}

func (view *initView) workspaceSpanIDs() []dagui.SpanID {
	if !view.workspace.showsSpans() {
		return nil
	}
	return validSpanIDs(view.workspaceID)
}

func (view *initView) moduleSpanIDs() []dagui.SpanID {
	if !view.modules.showsSpans() {
		return nil
	}
	var ids []dagui.SpanID
	if !view.findDone {
		// Discovery is an implementation detail: show it while it runs (or
		// after it fails), then only what it found and what was installed.
		ids = validSpanIDs(view.findID)
	}
	if view.final && len(view.installIDs) > 0 {
		// Installs stop at the first failure, so the last one is what
		// failed; the summary lists the ones before it.
		return append(ids, view.installIDs[len(view.installIDs)-1])
	}
	return append(ids, view.installIDs...)
}

func validSpanIDs(ids ...dagui.SpanID) []dagui.SpanID {
	var valid []dagui.SpanID
	for _, id := range ids {
		if id.IsValid() {
			valid = append(valid, id)
		}
	}
	return valid
}

func (view *initView) SetFinal(final bool) {
	if view.final != final {
		view.final = final
		view.Update()
	}
}

func (view *initView) Render(ctx tuist.Context) {
	if view.final {
		view.renderFinal(ctx)
		return
	}
	var started bool
	for _, section := range []struct {
		title   string
		section initSection
		spans   *idtui.SpanListView
		ids     []dagui.SpanID
		details []string
	}{
		{"Workspace", view.workspace, view.workspaceSpans, view.workspaceSpanIDs(), nil},
		{"Modules", view.modules, view.moduleSpans, view.moduleSpanIDs(), view.found},
		{"Cloud checks", view.cloud, nil, nil, nil},
	} {
		if section.section.state == initStepHidden {
			continue
		}
		if started {
			ctx.Line("")
		}
		started = true
		ctx.Line(lipgloss.NewStyle().Bold(true).Render(section.title))
		view.renderSection(ctx, section.section, section.spans, section.ids, section.details)
	}
}

func (view *initView) renderSection(ctx tuist.Context, section initSection, spans *idtui.SpanListView, ids []dagui.SpanID, details []string) {
	if spans != nil && len(ids) > 0 {
		view.RenderChild(ctx, spans)
	}
	for _, detail := range details {
		ctx.Line("  " + detail)
	}
	switch section.state {
	case initStepRunning:
		// A running span renders its own progress. The spinner covers the
		// gaps, such as connecting before the step's first span starts.
		if len(ids) == 0 && section.message != "" {
			if view.spinner.Label != section.message {
				view.spinner.Label = section.message
				view.spinner.Update()
			}
			view.RenderChild(ctx, view.spinner)
		}
	case initStepFailed:
		// A failed span above already carries the error.
		if len(ids) == 0 {
			ctx.Line("  " + statusLine(section))
		}
	default:
		ctx.Line("  " + statusLine(section))
	}
}

// renderFinal leaves a short summary of what was done and what is left to
// run. A failed step renders its spans, so the failure's logs stay on screen
// after the TUI exits, as they would in the generic trace.
func (view *initView) renderFinal(ctx tuist.Context) {
	for _, section := range []struct {
		section   initSection
		spans     *idtui.SpanListView
		ids       []dagui.SpanID
		installed []string
		retry     string
	}{
		{view.workspace, view.workspaceSpans, view.workspaceSpanIDs(), nil, ""},
		{view.modules, view.moduleSpans, view.moduleSpanIDs(), view.installed, initRecommendCommand},
		{view.cloud, nil, nil, nil, initCloudChecksCommand},
	} {
		switch section.section.state {
		case initStepDone, initStepNext, initStepSkipped:
			ctx.Line(statusLine(section.section))
		case initStepFailed:
			// Report what a failed step finished before it stopped.
			if len(section.installed) > 0 {
				ctx.Line(statusLine(initSection{state: initStepDone, message: installedMessage(section.installed)}))
			}
			if section.spans != nil && len(section.ids) > 0 {
				view.RenderChild(ctx, section.spans)
			}
			if section.retry != "" {
				ctx.Line(statusLine(initSection{state: initStepSkipped, message: "Not finished. Run later: " + view.command(section.retry)}))
			}
		case initStepHidden:
			// A failed module step stops the offers after it; say how to run
			// them. A failed workspace step leaves nothing to run yet.
			if section.retry != "" && view.modules.state == initStepFailed {
				ctx.Line(statusLine(initSection{state: initStepSkipped, message: "Not offered. Run later: " + view.command(section.retry)}))
			}
		}
	}
}

func (view *initView) command(sub string) string {
	if view.prefix == "" {
		return "dagger " + sub
	}
	return view.prefix + " " + sub
}

func statusLine(section initSection) string {
	var icon string
	switch section.state {
	case initStepDone:
		icon = lipgloss.NewStyle().Foreground(lipgloss.Green).Render(idtui.IconSuccess)
	case initStepNext:
		icon = lipgloss.NewStyle().Foreground(lipgloss.Blue).Render("→")
	case initStepSkipped:
		icon = lipgloss.NewStyle().Foreground(lipgloss.BrightBlack).Render(idtui.IconSkipped)
	case initStepFailed:
		icon = lipgloss.NewStyle().Foreground(lipgloss.Red).Render(idtui.IconFailure)
	}
	return icon + " " + section.message
}

// HandleKeyPress moves focus across the embedded span lists as if they were
// one list.
func (view *initView) HandleKeyPress(ctx tuist.Context, ev uv.KeyPressEvent) bool {
	var lists []*idtui.SpanListView
	for _, list := range []*idtui.SpanListView{view.workspaceSpans, view.moduleSpans} {
		if list != nil {
			lists = append(lists, list)
		}
	}
	focused := -1
	for i, list := range lists {
		if list.Focused() {
			focused = i
		}
	}
	switch uv.Key(ev).String() {
	case "down", "j":
		if focused >= 0 && lists[focused].HandleKeyPress(ctx, ev) {
			return true
		}
		for _, list := range lists[focused+1:] {
			if list.FocusFirst() {
				return true
			}
		}
	case "up", "k":
		if focused >= 0 && lists[focused].HandleKeyPress(ctx, ev) {
			return true
		}
		start := len(lists) - 1
		if focused >= 0 {
			start = focused - 1
		}
		for i := start; i >= 0; i-- {
			if lists[i].FocusLast() {
				return true
			}
		}
	case "home":
		for _, list := range lists {
			if list.FocusFirst() {
				return true
			}
		}
	case "end", "G":
		for i := len(lists) - 1; i >= 0; i-- {
			if lists[i].FocusLast() {
				return true
			}
		}
	case "left", "h", "right", "l", "enter":
		if focused >= 0 {
			return lists[focused].HandleKeyPress(ctx, ev)
		}
		for _, list := range lists {
			if list.HandleKeyPress(ctx, ev) {
				return true
			}
		}
	}
	return false
}
