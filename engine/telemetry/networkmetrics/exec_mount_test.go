package networkmetrics

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestExecMountWaitsAndRetainsFinalCounters(t *testing.T) {
	exited := make(chan struct{})
	waiting := make(chan struct{})
	command := &testCommandCounters{
		closed: make(chan struct{}),
		wait: func(context.Context) error {
			close(waiting)
			<-exited
			return nil
		},
	}
	command.reads.Store(1)
	r := &execMountResources{commands: []commandNetworkCounters{command}}
	finish := r.finish(command)
	done := make(chan error, 1)
	go func() { done <- finish() }()
	<-waiting
	select {
	case <-done:
		t.Fatal("release returned before the daemon exited")
	default:
	}
	close(exited)
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("release did not observe daemon exit")
	}
	require.NoError(t, finish())
	select {
	case <-command.closed:
		t.Fatal("counters closed before the final exec sample")
	default:
	}
	sample, err := r.Sample()
	require.NoError(t, err)
	require.True(t, sample.ScopeSupported)
	require.EqualValues(t, 19, sample.ExternalTxBytes)
	require.NoError(t, r.Close())
	<-command.closed
}

// Closing removes the mount accounting's own cgroup only. The execution
// cgroup is the executor's to remove, and may still hold what the
// container's runtime left, which must not fail the close.
func TestExecMountResourcesCloseLeavesExecutionCgroup(t *testing.T) {
	root := t.TempDir()
	saved := cgroupRoot
	cgroupRoot = root
	t.Cleanup(func() { cgroupRoot = saved })

	exec := filepath.Join(root, "exec", "x")
	require.NoError(t, os.MkdirAll(filepath.Join(exec, "sshfs"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(exec, "container", "default"), 0o755))

	r := &execMountResources{path: "/exec/x", active: true}
	require.NoError(t, r.Close())
	require.NoDirExists(t, filepath.Join(exec, "sshfs"))
	require.DirExists(t, exec)
}
