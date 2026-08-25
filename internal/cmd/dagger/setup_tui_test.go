package daggercmd

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	cloudauth "github.com/dagger/dagger/internal/cloud/auth"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
	"github.com/vito/tuist"
)

func TestSetupViewFinalSummary(t *testing.T) {
	view := &setupView{
		loginState:       setupLoginComplete,
		loginMessage:     "Already logged in.",
		complete:         true,
		migrationMessage: "No workspace loaded here yet — nothing to migrate.",
		installed:        []string{"eslint", "go"},
	}
	view.SetFinal(true)
	tui := tuist.New(tuist.NewHeadlessTerminal(100, 20))
	tui.AddChild(view)

	rendered := ansi.Strip(strings.Join(tui.RenderLines(), "\n"))
	for _, want := range []string{
		"Cloud account: Already logged in.",
		"Setup complete.",
		"No workspace loaded here yet",
		"Installed eslint, go.",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("final setup view missing %q:\n%s", want, rendered)
		}
	}
	if !strings.HasSuffix(strings.TrimSpace(rendered), "Setup complete.") {
		t.Fatalf("completion message was not last:\n%s", rendered)
	}
}

func TestSetupViewRendersConciseCompletedStates(t *testing.T) {
	view := &setupView{
		loginState:       setupLoginComplete,
		loginMessage:     "Already logged in.",
		migrationMessage: "Nothing to migrate.",
		migrationFinal:   emptyWorkspaceSetupHint,
	}
	tui := tuist.New(tuist.NewHeadlessTerminal(100, 20))
	tui.AddChild(view)

	rendered := ansi.Strip(strings.Join(tui.RenderLines(), "\n"))
	for _, want := range []string{
		"  ✔ Already logged in.",
		"  ✔ Nothing to migrate.",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("setup view missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "Workspace migration") || strings.Contains(rendered, "No workspace loaded") {
		t.Fatalf("setup view retained verbose migration detail:\n%s", rendered)
	}

	view.SetFinal(true)
	rendered = ansi.Strip(strings.Join(tui.RenderLines(), "\n"))
	for _, want := range []string{
		"nothing to migrate",
		"dagger install <module>",
		"dagger sdk search",
		"dagger module init <sdk> <name>",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("final setup view missing %q:\n%s", want, rendered)
		}
	}
}

func TestSetupViewIndentsSkippedLoginAsCanceled(t *testing.T) {
	view := &setupView{
		loginState:   setupLoginSkipped,
		loginMessage: setupLoginSkippedHint,
	}
	tui := tuist.New(tuist.NewHeadlessTerminal(100, 20))
	tui.AddChild(view)

	rendered := ansi.Strip(strings.Join(tui.RenderLines(), "\n"))
	if !strings.Contains(rendered, "  "+idtui.IconSkipped+" Skipped. (dagger login to log in)") {
		t.Fatalf("skipped login is not indented and canceled:\n%s", rendered)
	}
}

func TestSetupViewLoginChoiceHasNoTransitionalCruft(t *testing.T) {
	view := &setupView{
		loginState:   setupLoginPending,
		loginMessage: "Waiting for login choice...",
		loginSpinner: tuist.NewSpinner(),
	}
	tui := tuist.New(tuist.NewHeadlessTerminal(100, 20))
	tui.AddChild(view)

	rendered := ansi.Strip(strings.Join(tui.RenderLines(), "\n"))
	for _, unwanted := range []string{
		"Setting up this workspace",
		"Waiting for login choice",
		"Workspace",
		"Waiting for Cloud account",
	} {
		if strings.Contains(rendered, unwanted) {
			t.Fatalf("setup login choice retained %q:\n%s", unwanted, rendered)
		}
	}
	if !strings.Contains(rendered, "Cloud account") {
		t.Fatalf("setup login choice lost section title:\n%s", rendered)
	}
}

func TestSetupViewHidesKeymapDuringAuthentication(t *testing.T) {
	view := &setupView{
		loginState:   setupLoginPending,
		loginMessage: "Waiting for authentication...",
	}
	if !view.HideKeymap() {
		t.Fatal("authentication wait did not hide keymap")
	}
	view.loginMessage = "Checking Cloud account..."
	if view.HideKeymap() {
		t.Fatal("ordinary setup progress hid keymap")
	}
}

func TestSetupUIRecordsOnlyCompletedInstalls(t *testing.T) {
	view := &setupView{}
	ui := &setupUI{view: view, handle: immediateViewHandle{}}
	id := dagui.SpanID{}

	ui.addInstall(id)
	require.Equal(t, []dagui.SpanID{id}, view.installIDs)
	require.Empty(t, view.installed)

	ui.addInstalled("eslint")
	require.Equal(t, []string{"eslint"}, view.installed)
}

type immediateViewHandle struct{}

func (immediateViewHandle) Update(fn func()) { fn() }

func TestSetupLoginUpdatesCommandView(t *testing.T) {
	view := &setupView{}
	ui := &setupUI{view: view, handle: immediateViewHandle{}, live: true}
	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())

	err := setupStepLogin(t.Context(), cmd, func(context.Context) (*cloudauth.Cloud, error) {
		return &cloudauth.Cloud{}, nil
	}, ui)
	require.NoError(t, err)
	require.Equal(t, setupLoginComplete, view.loginState)
	require.Equal(t, "Already logged in.", view.loginMessage)
}
