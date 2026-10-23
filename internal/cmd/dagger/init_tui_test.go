package daggercmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/stretchr/testify/require"
	"github.com/vito/tuist"
)

// immediateViewHandle applies updates synchronously and invalidates the view,
// as the pretty frontend's handle does on its event loop.
type immediateViewHandle struct{ view *initView }

func (h immediateViewHandle) Update(fn func()) {
	fn()
	h.view.Update()
}

func newTestInitUI() (*initUI, *initView) {
	view := &initView{spinner: tuist.NewSpinner()}
	return &initUI{view: view, handle: immediateViewHandle{view: view}}, view
}

func renderInitView(t *testing.T, view *initView) string {
	t.Helper()
	tui := tuist.New(tuist.NewHeadlessTerminal(100, 20))
	tui.AddChild(view)
	return ansi.Strip(strings.Join(tui.RenderLines(), "\n"))
}

func TestInitUINilIsNoop(t *testing.T) {
	var ui *initUI
	ui.workspaceStarted(dagui.SpanID{})
	ui.workspaceDone("done")
	ui.modulesStarted()
	ui.modulesFound([]recommendation{{Module: registryModule{Name: "go"}, Match: "go.mod"}})
	ui.addInstall(dagui.SpanID{})
	ui.addInstalled("go")
	ui.modulesInstalled()
	ui.modulesFailed(errors.New("boom"))
	ui.cloudChecking()
	ui.cloudSkipped("skipped")
}

func TestInitViewShowsOutcomesNotPlumbing(t *testing.T) {
	ui, view := newTestInitUI()
	ui.workspaceDone("Workspace configuration initialized at ./dagger.toml")
	ui.modulesSkipped("Skipped. Run later: dagger module recommend")
	ui.cloudSkipped("Skipped. Run later: dagger cloud checks on")

	rendered := renderInitView(t, view)
	for _, want := range []string{
		"Workspace\n  " + idtui.IconSuccess + " Workspace configuration initialized at ./dagger.toml",
		"Modules\n  " + idtui.IconSkipped + " Skipped. Run later: dagger module recommend",
		"Cloud checks\n  " + idtui.IconSkipped + " Skipped. Run later: dagger cloud checks on",
	} {
		require.Contains(t, rendered, want)
	}

	view.SetFinal(true)
	require.Equal(t, strings.Join([]string{
		idtui.IconSuccess + " Workspace configuration initialized at ./dagger.toml",
		idtui.IconSkipped + " Skipped. Run later: dagger module recommend",
		idtui.IconSkipped + " Skipped. Run later: dagger cloud checks on",
	}, "\n"), strings.TrimSpace(renderInitView(t, view)))
}

func TestInitViewHidesStepsUntilAnswered(t *testing.T) {
	ui, view := newTestInitUI()
	ui.workspaceDone("Workspace configuration found at ./dagger.toml")
	rendered := renderInitView(t, view)
	require.NotContains(t, rendered, "Modules")
	require.NotContains(t, rendered, "Cloud checks")

	ui.cloudChecking()
	require.Contains(t, renderInitView(t, view), "Checking cloud checks...")
	ui.cloudPrompting()
	require.NotContains(t, renderInitView(t, view), "Cloud checks")
}

func TestInitViewListsFoundModulesThenInstalls(t *testing.T) {
	ui, view := newTestInitUI()
	ui.modulesStarted()
	require.Contains(t, renderInitView(t, view), "Finding recommended modules...")

	ui.modulesFinding(dagui.SpanID{})
	ui.modulesFound([]recommendation{
		{Module: registryModule{Name: "go", Repo: "dagger.io/go"}, Match: "go.mod"},
		{Module: registryModule{Name: "ruff", Repo: "dagger.io/python/ruff"}, Match: "ruff.toml"},
	})
	rendered := renderInitView(t, view)
	require.Contains(t, rendered, "  go: found go.mod")
	require.Contains(t, rendered, "  ruff: found ruff.toml")
	require.NotContains(t, rendered, "Finding recommended modules...")

	ui.addInstall(dagui.SpanID{})
	ui.addInstalled("go")
	ui.addInstall(dagui.SpanID{})
	ui.addInstalled("ruff")
	ui.modulesInstalled()
	rendered = renderInitView(t, view)
	require.NotContains(t, rendered, "found go.mod")
	require.Contains(t, rendered, idtui.IconSuccess+" Installed go, ruff.")
}

func TestInitViewFinalLeavesErrorsToSpans(t *testing.T) {
	ui, view := newTestInitUI()
	ui.workspaceDone("Workspace configuration initialized at ./dagger.toml")
	ui.modulesStarted()
	ui.modulesFailed(errors.New("install dagger.io/go: boom"))
	require.Contains(t, renderInitView(t, view), idtui.IconFailure+" install dagger.io/go: boom")

	view.SetFinal(true)
	rendered := renderInitView(t, view)
	require.NotContains(t, rendered, "boom")
	require.Contains(t, rendered, "Workspace configuration initialized")
}

func TestInitViewFinalReportsPartialInstalls(t *testing.T) {
	ui, view := newTestInitUI()
	ui.modulesStarted()
	ui.modulesFound([]recommendation{
		{Module: registryModule{Name: "go"}, Match: "go.mod"},
		{Module: registryModule{Name: "ruff"}, Match: "ruff.toml"},
	})
	goID, ruffID := prettySpanID(1), prettySpanID(2)
	ui.addInstall(goID)
	ui.addInstalled("go")
	ui.addInstall(ruffID)
	ui.modulesFailed(errors.New("install dagger.io/python/ruff: boom"))
	require.Equal(t, []dagui.SpanID{goID, ruffID}, view.moduleSpanIDs())

	view.SetFinal(true)
	rendered := renderInitView(t, view)
	require.Contains(t, rendered, idtui.IconSuccess+" Installed go.")
	require.Contains(t, rendered, idtui.IconSkipped+" Not finished. Run later: dagger module recommend")
	require.Contains(t, rendered, idtui.IconSkipped+" Not offered. Run later: dagger cloud checks on")
	// The final render keeps only the failed install's span.
	require.Equal(t, []dagui.SpanID{ruffID}, view.moduleSpanIDs())
}

func TestInitViewFinalAfterWorkspaceFailureOffersNothing(t *testing.T) {
	ui, view := newTestInitUI()
	ui.workspaceStarted(prettySpanID(1))
	ui.workspaceFailed(errors.New("legacy config"))
	view.SetFinal(true)
	require.Empty(t, strings.TrimSpace(renderInitView(t, view)))
}

func TestInitViewSpanSelection(t *testing.T) {
	ui, view := newTestInitUI()
	workspaceID := prettySpanID(1)
	findID := prettySpanID(2)
	installID := prettySpanID(3)

	ui.workspaceStarted(workspaceID)
	require.Equal(t, []dagui.SpanID{workspaceID}, view.workspaceSpanIDs())
	ui.workspaceDone("done")
	require.Empty(t, view.workspaceSpanIDs())

	ui.modulesStarted()
	ui.modulesFinding(findID)
	require.Equal(t, []dagui.SpanID{findID}, view.moduleSpanIDs())
	ui.modulesFound([]recommendation{{Module: registryModule{Name: "go"}, Match: "go.mod"}})
	require.Empty(t, view.moduleSpanIDs())
	ui.addInstall(installID)
	require.Equal(t, []dagui.SpanID{installID}, view.moduleSpanIDs())
	ui.modulesInstalled()
	require.Empty(t, view.moduleSpanIDs())
}

func prettySpanID(n byte) dagui.SpanID {
	var id dagui.SpanID
	id.SpanID[7] = n
	return id
}
