package engineutil

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSignalBlocked(t *testing.T) {
	t.Parallel()

	// crun blocks every signal once it proxies the container's terminal.
	status := []byte("Name:\tcrun\nSigPnd:\t0000000000000000\nSigBlk:\tfffffffe7ffbfeff\nSigIgn:\t0000000000000000\n")
	require.True(t, signalBlocked(status, syscall.SIGWINCH))
	// Before that, SIGWINCH (28) is not blocked.
	status = []byte("Name:\tcrun\nSigBlk:\t0000000000000000\n")
	require.False(t, signalBlocked(status, syscall.SIGWINCH))
	status = []byte("Name:\tcrun\nSigBlk:\t0000000008000000\n")
	require.True(t, signalBlocked(status, syscall.SIGWINCH))
	require.False(t, signalBlocked([]byte("Name:\tcrun\n"), syscall.SIGWINCH))
}

func TestWaitForBlockedSignalNotBlocked(t *testing.T) {
	t.Parallel()

	// This test process doesn't block SIGWINCH, so the wait never fires.
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	select {
	case <-waitForBlockedSignal(ctx, os.Getpid(), syscall.SIGWINCH):
		t.Fatal("reported SIGWINCH blocked in a process that doesn't block it")
	case <-time.After(50 * time.Millisecond):
	}
}

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
