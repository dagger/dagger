package core

import (
	"context"
	"os/exec"
	"time"

	"github.com/creack/pty"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// The setup flow asks its last questions as plain text after the TUI has
// closed. The TUI's terminal keeps reading stdin for the life of the process,
// so those prompts must read through it; reading os.Stdin directly never
// returns. Only a real pty exercises this.
func (WorkspaceSuite) TestSetupPromptReadsInputAfterTUI(ctx context.Context, t *testctx.T) {
	repo := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"remote", "add", "origin", "https://github.com/example/project.git"},
	} {
		git := exec.Command("git", args...)
		git.Dir = repo
		out, err := git.CombinedOutput()
		require.NoError(t, err, string(out))
	}

	console, err := newTUIConsole(t, 60*time.Second)
	require.NoError(t, err)
	defer console.Close()

	tty := console.Tty()
	require.NoError(t, pty.Setsize(tty, &pty.Winsize{Rows: 40, Cols: 120}))

	cmd := hostDaggerCommand(ctx, t, repo, "--progress=tty", "init")
	// No Cloud credentials, so enabling checks stops at the signup prompt.
	cmd.Env = append(cmd.Env, "HOME="+t.TempDir(), "XDG_CONFIG_HOME="+t.TempDir(), "DAGGER_CLOUD_TOKEN=")
	cmd.Stdin = tty
	cmd.Stdout = tty
	cmd.Stderr = tty
	require.NoError(t, cmd.Start())

	// Forms inside the TUI: Enter takes the preselected Skip; left arrow
	// moves to Run.
	_, err = console.ExpectString("Find and install suitable modules?")
	require.NoError(t, err)
	time.Sleep(300 * time.Millisecond)
	_, err = console.Send("\r")
	require.NoError(t, err)

	_, err = console.ExpectString("Enable cloud checks?")
	require.NoError(t, err)
	time.Sleep(300 * time.Millisecond)
	_, err = console.Send("\x1b[D\r")
	require.NoError(t, err)

	// The TUI is gone now; this prompt reads stdin directly.
	_, err = console.ExpectString("Run this command? [Y/n]")
	require.NoError(t, err)
	time.Sleep(300 * time.Millisecond)
	_, err = console.SendLine("n")
	require.NoError(t, err)

	_, err = console.ExpectString("Complete the prerequisite")
	require.NoError(t, err, "the prompt never read the answer")

	go console.ExpectEOF()
	require.Error(t, cmd.Wait(), "declining the prerequisite exits non-zero")
}
