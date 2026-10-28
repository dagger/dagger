package main

import (
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine/wcprof"
)

func TestRecordStopSignal(t *testing.T) {
	// the first SIGTERM is an op in the stop dump, which it writes beside
	// the signal's handling, not before it
	wcprof.EnableGlobal()
	t.Cleanup(wcprof.DisableGlobal)
	path := wcprof.StopDumpPath(filepath.Join(t.TempDir(), "state"))
	t.Cleanup(func() { _ = wcprof.FlushStopDump(path) })
	recordStopSignal(path)
	require.NoError(t, wcprof.FlushStopDump(path))
	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()
	zr, err := gzip.NewReader(f)
	require.NoError(t, err)
	h, evs, err := wcprof.ReadDump(zr)
	require.NoError(t, err)
	var found bool
	for _, ev := range evs {
		found = found || (ev.Type == "op" && h.Strings[ev.ClassID] == "engine.stop.signal")
	}
	require.True(t, found, "stop dump has no engine.stop.signal op")
}

// stopDumpClasses reads a stop dump, once the writes asked for so far are on
// disk: its ended ops' and open ops' names.
func stopDumpClasses(t *testing.T, path string) (ended, open map[string]bool) {
	t.Helper()
	require.NoError(t, wcprof.FlushStopDump(path))
	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()
	zr, err := gzip.NewReader(f)
	require.NoError(t, err)
	h, evs, err := wcprof.ReadDump(zr)
	require.NoError(t, err)
	ended, open = map[string]bool{}, map[string]bool{}
	for _, ev := range evs {
		if ev.Type == "op" {
			ended[h.Strings[ev.ClassID]] = true
		}
	}
	for _, oo := range h.OpenOps {
		open[h.Strings[oo.ClassID]] = true
	}
	return ended, open
}

func TestExitStepRecordsAFinalDrain(t *testing.T) {
	// a drain after the server's stop (telemetry flushes) is an op, open in
	// the stop dump while it runs and ended once it's done, so a kill during
	// it shows which drain was running
	wcprof.EnableGlobal()
	t.Cleanup(wcprof.DisableGlobal)
	path := wcprof.StopDumpPath(filepath.Join(t.TempDir(), "state"))
	stopDumpPath.Store(&path)
	t.Cleanup(func() { stopDumpPath.Store(nil) })
	t.Cleanup(func() { _ = wcprof.FlushStopDump(path) })
	var openDuring bool
	exitStep("flush engine events", func() {
		_, open := stopDumpClasses(t, path)
		openDuring = open["engine.exit.flush engine events"]
	})
	require.True(t, openDuring, "the drain wasn't open in the stop dump while it ran")
	ended, _ := stopDumpClasses(t, path)
	require.True(t, ended["engine.exit.flush engine events"], "the drain didn't end in the stop dump")
}
