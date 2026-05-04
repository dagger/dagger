package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/creack/pty"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// The TUI draws on the terminal's alternate screen; this escape sequence is
// emitted when it leaves that screen, marking the boundary between frames
// the user only sees while the command runs and output that stays in their
// terminal after it exits.
const exitAltScreen = "\x1b[?1049l"

// TestInteractiveFinalRenderEmitsProgress guards #12936: a failed `dagger
// call` under an interactive TTY must still leave the progress tree and the
// failing exec's stderr visible in the user's terminal after the CLI exits.
// The regression was subtle because the TUI shows both while it's running:
// only the post-teardown render was empty.
func (ModuleSuite) TestInteractiveFinalRenderEmitsProgress(ctx context.Context, t *testctx.T) {
	t.Run("failure renders final progress", func(ctx context.Context, t *testctx.T) {
		modDir := t.TempDir()

		_, err := hostDaggerExec(ctx, t, modDir, "init", "--source=.", "--name=progress", "--sdk=go")
		require.NoError(t, err)

		moduleSrc := fmt.Sprintf(`package main

import (
	"context"
	"errors"
)

type Progress struct{}

func (m *Progress) Fail(ctx context.Context) (string, error) {
	_, err := dag.Container().
		From("%[1]s").
		WithExec([]string{"sh", "-c", "printf tui-final-render && exit 1"}).
		Sync(ctx)
	if err == nil {
		return "", errors.New("expected failure")
	}
	return "", err
}
`, alpineImage)
		err = os.WriteFile(filepath.Join(modDir, "main.go"), []byte(moduleSrc), 0o644)
		require.NoError(t, err)

		// Warm the module so the run under test is bounded by the failing
		// exec, not by codegen/load.
		_, err = hostDaggerExec(ctx, t, modDir, "functions")
		require.NoError(t, err)

		console, err := newTUIConsole(t, 60*time.Second)
		require.NoError(t, err)
		defer console.Close()

		tty := console.Tty()
		require.NoError(t, pty.Setsize(tty, &pty.Winsize{Rows: 12, Cols: 60}))

		cmd := hostDaggerCommand(ctx, t, modDir, "call", "fail")
		cmd.Stdin = tty
		cmd.Stdout = tty
		cmd.Stderr = tty

		require.NoError(t, cmd.Start())

		// The key idea of this test: go-expect reads the pty stream in order
		// and never rewinds. Once we've matched a piece of output, any later
		// ExpectString can only match bytes that came *after* it.
		//
		// So we first wait for the TUI to leave the alternate screen. After
		// that point, everything we match is part of what the user still
		// sees in their terminal after the command has exited: which is
		// exactly the final render we're trying to protect.
		//
		// Doing it this way matters: while the TUI is running it also shows
		// the failure icon and the "tui-final-render" marker, so if we just
		// called ExpectString without the alt-screen gate first, the test
		// would happily pass even when the final render is completely empty
		// (which is the bug we're guarding against).
		_, err = console.ExpectString(exitAltScreen)
		require.NoError(t, err, "TUI never left the alternate screen")

		_, err = console.ExpectString(idtui.IconFailure)
		require.NoError(t, err, "final render is missing the failure icon")

		_, err = console.ExpectString("tui-final-render")
		require.NoError(t, err, "final render is missing the failing exec's stderr")

		go console.ExpectEOF()

		err = cmd.Wait()
		require.Error(t, err, "dagger call fail should exit non-zero")
	})
}
