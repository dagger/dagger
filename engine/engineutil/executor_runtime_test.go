package engineutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestProcKillerRetriesEmptyPidFile(t *testing.T) {
	t.Parallel()

	child := exec.Command("sleep", "60")
	require.NoError(t, child.Start())
	waited := make(chan error, 1)
	go func() { waited <- child.Wait() }()
	t.Cleanup(func() { _ = child.Process.Kill() })

	// crun creates the pid file before writing the pid into it.
	pidFile := filepath.Join(t.TempDir(), "exec.pid")
	require.NoError(t, os.WriteFile(pidFile, nil, 0o600))
	go func() {
		time.Sleep(30 * time.Millisecond)
		_ = os.WriteFile(pidFile, []byte(strconv.Itoa(child.Process.Pid)), 0o600)
	}()

	require.NoError(t, procKiller{id: "exec", pidfile: pidFile}.Kill(t.Context()))
	select {
	case err := <-waited:
		var exitErr *exec.ExitError
		require.ErrorAs(t, err, &exitErr)
		require.Equal(t, syscall.SIGKILL, exitErr.Sys().(syscall.WaitStatus).Signal())
	case <-time.After(5 * time.Second):
		t.Fatal("the process wasn't killed")
	}
}
