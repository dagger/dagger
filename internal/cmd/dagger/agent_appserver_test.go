package daggercmd

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql/idtui"
)

func TestApplyAppServerProgress(t *testing.T) {
	origProgress, origFrontend := progress, Frontend
	t.Cleanup(func() {
		progress, Frontend = origProgress, origFrontend
	})

	// Main resolved "auto" to the pretty TUI on a terminal: app-server mode
	// swaps in a plain stream on stderr rather than refusing to run.
	progress = "tty"
	Frontend = &idtui.FrontendMock{}
	require.NoError(t, applyAppServerProgress(false))
	require.Equal(t, "plain", progress)
	require.NotEqual(t, &idtui.FrontendMock{}, Frontend)

	// An explicitly requested TTY is refused: it would fight the client for
	// the terminal.
	progress = "tty"
	require.Error(t, applyAppServerProgress(true))

	// Any other explicit mode is left alone.
	progress = "report"
	mock := &idtui.FrontendMock{}
	Frontend = mock
	require.NoError(t, applyAppServerProgress(true))
	require.Equal(t, "report", progress)
	require.Same(t, mock, Frontend)
}
