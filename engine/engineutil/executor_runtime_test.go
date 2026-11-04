package engineutil

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	runc "github.com/containerd/go-runc"
	"github.com/dagger/dagger/internal/buildkit/executor"
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

// An exec must not fail because its runtime is slow to start, such as when the
// machine is stalled or other execs are ahead of it in the launch queue.
func TestCallWithIOWaitsForSlowRuntimeStart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := &Client{Opts: &Opts{}}
		err := c.callWithIO(context.Background(), &executor.ProcessInfo{}, nil, procKiller{}, func(ctx context.Context, started chan<- int, _ runc.IO, _ runc.ConsoleSocket, _ string) error {
			select {
			case <-time.After(time.Minute):
			case <-ctx.Done():
				return context.Cause(ctx)
			}
			started <- os.Getpid()
			return nil
		})
		require.NoError(t, err)
	})
}

// A runtime that fails before starting must still end the exec, with its error.
func TestCallWithIOReturnsWhenRuntimeNeverStarts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		startErr := errors.New("failed to start")
		c := &Client{Opts: &Opts{}}
		err := c.callWithIO(context.Background(), &executor.ProcessInfo{}, nil, procKiller{}, func(context.Context, chan<- int, runc.IO, runc.ConsoleSocket, string) error {
			return startErr
		})
		require.ErrorIs(t, err, startErr)
	})
}

// A request canceled while its exec waits to launch must not leave the process
// running once it does start.
func TestCallWithIOKillsProcessStartedAfterCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pidFile := filepath.Join(t.TempDir(), "exec.pid")

	c := &Client{Opts: &Opts{}}
	killer := procKiller{id: "exec", pidfile: pidFile}
	err := c.callWithIO(ctx, &executor.ProcessInfo{}, nil, killer, func(_ context.Context, started chan<- int, _ runc.IO, _ runc.ConsoleSocket, _ string) error {
		cancel()
		time.Sleep(100 * time.Millisecond)

		child := exec.Command("sleep", "60")
		if err := child.Start(); err != nil {
			return err
		}
		t.Cleanup(func() { _ = child.Process.Kill() })
		if err := os.WriteFile(pidFile, []byte(strconv.Itoa(child.Process.Pid)), 0o600); err != nil {
			return err
		}
		started <- child.Process.Pid
		return child.Wait()
	})
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	require.Equal(t, syscall.SIGKILL, exitErr.Sys().(syscall.WaitStatus).Signal())
}
