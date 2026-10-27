package server

import (
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine/wcprof"
)

// recordGlobally turns on engine-global recording for the test.
func recordGlobally(t *testing.T) {
	t.Helper()
	wcprof.EnableGlobal()
	t.Cleanup(wcprof.DisableGlobal)
}

// stopDump reads the server's stop dump: the names of its ended ops and of
// its open ones.
func stopDump(t *testing.T, srv *Server) (ended, open map[string]bool) {
	t.Helper()
	f, err := os.Open(wcprof.StopDumpPath(srv.rootDir))
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

func TestGracefulStopRecordsItsPhases(t *testing.T) {
	// each stop phase is an op, and the stop dump written at the end holds
	// them all, ended
	recordGlobally(t)
	srv := newGracefulStopServer(t, filepath.Join(t.TempDir(), "cache.db"))
	require.NoError(t, srv.GracefulStop(boundedContext(t)))
	ended, _ := stopDump(t, srv)
	for _, class := range []string{"engine.stop", "engine.stop.stop remote cache integration", "engine.stop.remove sessions", "engine.stop.close metadata DBs"} {
		require.True(t, ended[class], "stop dump lacks %q; has %v", class, ended)
	}
}

func TestGracefulStopDumpShowsAHungPhase(t *testing.T) {
	// a phase that hangs, as one does until CI's SIGKILL, is already in the
	// stop dump as an open op, written when the phase began
	recordGlobally(t)
	srv := newGracefulStopServer(t, filepath.Join(t.TempDir(), "cache.db"))
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	require.NoError(t, srv.startRemoteCacheIntegration(&RemoteCacheIntegrationConfig{Run: func(ctx context.Context, _ *RemoteCacheAdapter) error {
		<-ctx.Done()
		<-release // ignores cancellation
		return nil
	}}))
	stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		_ = srv.GracefulStop(stopCtx)
	}()
	require.Eventually(t, func() bool {
		if _, err := os.Stat(wcprof.StopDumpPath(srv.rootDir)); err != nil {
			return false
		}
		_, open := stopDump(t, srv)
		return open["engine.stop.stop remote cache integration"]
	}, 10*time.Second, 20*time.Millisecond, "the hung phase never showed as an open op in the stop dump")
	releaseOnce.Do(func() { close(release) })
	cancel()
	<-stopped
}
