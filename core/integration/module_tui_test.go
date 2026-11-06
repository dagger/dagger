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

// The TUI hides the hardware cursor while it draws and shows it again as
// the very last step of teardown, so this escape sequence marks the
// boundary between frames the user only sees while the command runs and
// output that stays in their terminal after it exits. (The alternate
// screen is not a reliable boundary: the TUI only falls back to it when
// the live tree overflows a terminal without synchronized-output support,
// and the live view condenses itself to fit the viewport.)
const restoreCursor = "\x1b[?25h"

// TestInteractiveFinalRenderEmitsProgress guards #12936: a failed `dagger
// call` under an interactive TTY must still leave the progress tree and the
// failing exec's stderr visible in the user's terminal after the CLI exits.
// The regression was subtle because the TUI shows both while it's running:
// only the post-teardown render was empty.
func (ModuleSuite) TestInteractiveFinalRenderEmitsProgress(ctx context.Context, t *testctx.T) {
	t.Run("failure renders final progress", func(ctx context.Context, t *testctx.T) {
		modDir := t.TempDir()

		err := os.WriteFile(filepath.Join(modDir, "dagger.json"), []byte(`{"name":"progress","engineVersion":"latest","sdk":{"source":"go"}}`), 0o644)
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
		_, err = hostDaggerExec(ctx, t, modDir, "-m", ".", "api", "functions")
		require.NoError(t, err)

		console, err := newTUIConsole(t, 60*time.Second)
		require.NoError(t, err)
		defer console.Close()

		tty := console.Tty()
		require.NoError(t, pty.Setsize(tty, &pty.Winsize{Rows: 12, Cols: 60}))

		cmd := hostDaggerCommand(ctx, t, modDir, "-m", ".", "call", "fail")
		cmd.Stdin = tty
		cmd.Stdout = tty
		cmd.Stderr = tty

		require.NoError(t, cmd.Start())

		// The key idea of this test: go-expect reads the pty stream in order
		// and never rewinds. Once we've matched a piece of output, any later
		// ExpectString can only match bytes that came *after* it.
		//
		// So we first wait for the TUI to restore the cursor, the last thing
		// it writes as it tears down (it hides the cursor for the whole run,
		// and nothing in this run — no forms, no terminal — shows it early).
		// After that point, everything we match is part of what the user
		// still sees in their terminal after the command has exited: which
		// is exactly the final render we're trying to protect.
		//
		// Doing it this way matters: while the TUI is running it also shows
		// the failure icon and the "tui-final-render" marker, so if we just
		// called ExpectString without the teardown gate first, the test
		// would happily pass even when the final render is completely empty
		// (which is the bug we're guarding against).
		_, err = console.ExpectString(restoreCursor)
		require.NoError(t, err, "TUI never tore down (cursor never restored)")

		_, err = console.ExpectString(idtui.IconFailure)
		require.NoError(t, err, "final render is missing the failure icon")

		_, err = console.ExpectString("tui-final-render")
		require.NoError(t, err, "final render is missing the failing exec's stderr")

		go console.ExpectEOF()

		err = cmd.Wait()
		require.Error(t, err, "dagger call fail should exit non-zero")
	})
}

// TestInteractiveTUIOwnsTerminal guards the TUI against writes to its
// terminal from anything but itself, which scramble its frames: a CLI
// diagnostic sent to stderr, a stray fmt.Fprintln(os.Stderr), a library's
// own logging. The writers keep changing, so this checks what reaches the
// terminal rather than any one of them.
//
// The TUI puts the terminal in raw mode, which turns off the line
// discipline's "\n" to "\r\n" translation, and ends every line it draws with
// "\r\n" itself. So while it runs, a bare "\n" on the terminal came from
// some other writer: on a real terminal, whatever follows it starts in the
// middle of the line, and the TUI's frames come apart.
//
// The command logs a warning through slog while the TUI runs, which must
// show up in the TUI instead.
func (ModuleSuite) TestInteractiveTUIOwnsTerminal(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(workdir, "query.graphql"), []byte(`{ version }`), 0o644))

	console, err := newTUIConsole(t, 60*time.Second)
	require.NoError(t, err)
	defer console.Close()

	tty := console.Tty()
	require.NoError(t, pty.Setsize(tty, &pty.Winsize{Rows: 30, Cols: 200}))

	cmd := hostDaggerCommand(ctx, t, workdir, "api", "query", "--doc", "query.graphql")
	// Closing the session reads this, and warns when it isn't a duration.
	cmd.Env = append(cmd.Env, "_EXPERIMENTAL_DAGGER_SHUTDOWN_TIMEOUT=not-a-duration")
	cmd.Stdin = tty
	cmd.Stdout = tty
	cmd.Stderr = tty

	require.NoError(t, cmd.Start())

	// Everything up to the TUI restoring the cursor, the last thing it
	// writes as it tears down, was written while it owned the terminal.
	live, err := console.ExpectString(restoreCursor)
	require.NoError(t, err, "TUI never tore down (cursor never restored)")

	go console.ExpectEOF()

	require.NoError(t, cmd.Wait())

	for i := range len(live) {
		if live[i] == '\n' && (i == 0 || live[i-1] != '\r') {
			t.Fatalf("something wrote to the terminal behind the TUI's back, ending at a bare newline:\n%q",
				live[max(0, i-300):i+1])
		}
	}
	require.Contains(t, live, "invalid _EXPERIMENTAL_DAGGER_SHUTDOWN_TIMEOUT value",
		"the TUI never showed the warning")
}
