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

func TestWorkloadReleasedWithinATick(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "init.pid")
	const startedNS = int64(5 * time.Second)
	startedWall := time.Now()
	writePidFile(t, pidFile)
	// The coarse clock stamped the file a tick before the started callback.
	early := startedWall.Add(-4 * time.Millisecond)
	require.NoError(t, os.Chtimes(pidFile, early, early))

	require.Equal(t, startedNS, workloadReleasedNS(pidFile, startedNS, startedWall.UnixNano()),
		"a release within a tick of the start is clamped to the start, not dropped")
}

func TestWorkloadReleasedUnknown(t *testing.T) {
	now := time.Now().UnixNano()
	missing := filepath.Join(t.TempDir(), "init.pid")
	require.Zero(t, workloadReleasedNS(missing, 1, now), "runc never wrote the pid file")
	require.Zero(t, workloadReleasedNS("", 1, now), "not profiling: no pid file was requested")
	require.Zero(t, workloadReleasedNS(missing, 0, now), "the container never started")
}

// TestWorkloadReleasedAt: the release on the wall clock, for the span side,
// is the pid file's time, clamped to the started callback, and zero when
// unknown.
func TestWorkloadReleasedAt(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "init.pid")
	startedWall := time.Now()
	writePidFile(t, pidFile)
	stamped := startedWall.Add(30 * time.Millisecond)
	require.NoError(t, os.Chtimes(pidFile, stamped, stamped))
	require.True(t, workloadReleasedAt(pidFile, startedWall).Equal(stamped))

	early := startedWall.Add(-4 * time.Millisecond)
	require.NoError(t, os.Chtimes(pidFile, early, early))
	require.True(t, workloadReleasedAt(pidFile, startedWall).Equal(startedWall), "clamped to the start")

	missing := filepath.Join(t.TempDir(), "init.pid")
	require.True(t, workloadReleasedAt(missing, startedWall).IsZero(), "runc never wrote the pid file")
	require.True(t, workloadReleasedAt("", startedWall).IsZero(), "no pid file was requested")
	require.True(t, workloadReleasedAt(pidFile, time.Time{}).IsZero(), "the container never started")
}
