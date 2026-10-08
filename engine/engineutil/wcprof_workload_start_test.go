package engineutil

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine/wcprof"
)

// writePidFile writes a pid file the way runc's createPidFile does: a
// temporary file renamed into place.
func writePidFile(t *testing.T, path string) {
	t.Helper()
	tmp := filepath.Join(filepath.Dir(path), "."+filepath.Base(path))
	require.NoError(t, os.WriteFile(tmp, []byte("1234"), 0o666))
	require.NoError(t, os.Rename(tmp, path))
}

func TestWorkloadStartWatchSeesPidFileRename(t *testing.T) {
	wcprof.EnsureRecorder()
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "init.pid")

	w, err := watchWorkloadStart(pidFile)
	require.NoError(t, err)
	// Other files in the bundle are not the boundary.
	writePidFile(t, filepath.Join(dir, "other.pid"))
	before := wcprof.NowNS()
	writePidFile(t, pidFile)
	// stop may run before the watcher reads the event; wait for it.
	require.Eventually(t, func() bool { return w.atNS.Load() != 0 }, 5*time.Second, time.Millisecond)
	at := w.stop()
	require.GreaterOrEqual(t, at, before)
	require.LessOrEqual(t, at, wcprof.NowNS())
}

func TestWorkloadStartWatchWithoutPidFile(t *testing.T) {
	wcprof.EnsureRecorder()
	w, err := watchWorkloadStart(filepath.Join(t.TempDir(), "init.pid"))
	require.NoError(t, err)
	require.Zero(t, w.stop(), "a run that never released its workload has no boundary")
	var nilWatch *workloadStartWatch
	require.Zero(t, nilWatch.stop())
}
