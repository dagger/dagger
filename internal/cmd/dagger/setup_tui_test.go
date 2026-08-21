package daggercmd

import (
	"strings"
	"testing"

	"github.com/vito/tuist"
)

func TestSetupViewFinalSummary(t *testing.T) {
	view := &setupView{
		complete:         true,
		migrationMessage: "No workspace loaded here yet — nothing to migrate.",
		installed:        []string{"eslint", "go"},
	}
	view.SetFinal(true)
	tui := tuist.New(tuist.NewHeadlessTerminal(100, 20))
	tui.AddChild(view)

	rendered := strings.Join(tui.RenderLines(), "\n")
	for _, want := range []string{
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
