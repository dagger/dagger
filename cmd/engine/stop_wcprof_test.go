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
	// the first SIGTERM is an op in the stop dump, which it writes
	wcprof.EnableGlobal()
	t.Cleanup(wcprof.DisableGlobal)
	path := wcprof.StopDumpPath(filepath.Join(t.TempDir(), "state"))
	recordStopSignal(path)
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
