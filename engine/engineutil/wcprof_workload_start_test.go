package engineutil

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// writePidFile writes a pid file the way runc's createPidFile does: a
// temporary file renamed into place.
func writePidFile(t *testing.T, path string) {
	t.Helper()
	tmp := filepath.Join(filepath.Dir(path), "."+filepath.Base(path))
	require.NoError(t, os.WriteFile(tmp, []byte("1234"), 0o666))
	require.NoError(t, os.Rename(tmp, path))
}

func TestWorkloadReleasedFromPidFile(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "init.pid")
	// The started callback's paired wcprof and wall-clock times.
	const startedNS = int64(5 * time.Second)
	startedWall := time.Now()
	time.Sleep(50 * time.Millisecond)
	before := time.Now()
	writePidFile(t, pidFile)
	after := time.Now()

	released := workloadReleasedNS(pidFile, startedNS, startedWall.UnixNano())
	// File times come from the coarse clock: allow it to be a tick early.
	const tick = 20 * time.Millisecond
	require.GreaterOrEqual(t, released, startedNS+int64(before.Sub(startedWall)-tick))
	require.LessOrEqual(t, released, startedNS+int64(after.Sub(startedWall)))
	require.Greater(t, released, startedNS, "the release is after the started callback")
}

func TestWorkloadReleasedUnknown(t *testing.T) {
	now := time.Now().UnixNano()
	missing := filepath.Join(t.TempDir(), "init.pid")
	require.Zero(t, workloadReleasedNS(missing, 1, now), "runc never wrote the pid file")
	require.Zero(t, workloadReleasedNS("", 1, now), "not profiling: no pid file was requested")
	require.Zero(t, workloadReleasedNS(missing, 0, now), "the container never started")
}
