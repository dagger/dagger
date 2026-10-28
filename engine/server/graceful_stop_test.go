package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	bolt "go.etcd.io/bbolt"
	sdklog "go.opentelemetry.io/otel/sdk/log"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/cachevolumedir"
	"github.com/dagger/dagger/engine/engineutil"
	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	"github.com/dagger/dagger/internal/buildkit/util/network"
)

func TestGracefulStopReturnsEarlierShutdownErrors(t *testing.T) {
	srv := newGracefulStopServer(t, filepath.Join(t.TempDir(), "cache.db"))
	// GracefulStop used to collect this close error and then drop it.
	srv.engineUtilOpts = &engineutil.Opts{NetworkProviders: map[pb.NetMode]network.Provider{pb.NetMode_UNSET: failingNetworkProvider{}}}
	err := srv.GracefulStop(boundedContext(t))
	require.ErrorIs(t, err, errNetworkProviderClose)
}

func TestGracefulStopWithDiscardedStateSkipsStateWork(t *testing.T) {
	// with state.discarded, the stop does no work on the state directory,
	// which goes with the engine: it doesn't wait for a garbage collection
	// that's running, save the cache, close the metadata DBs or release the
	// network namespaces
	path := filepath.Join(t.TempDir(), "cache.db")
	srv := newGracefulStopServer(t, path)
	srv.stateDiscarded = true
	provider := &closeCountingNetworkProvider{}
	srv.engineUtilOpts = &engineutil.Opts{NetworkProviders: map[pb.NetMode]network.Provider{pb.NetMode_UNSET: provider}}
	// a scheduled collection is running, and won't finish before the stop
	srv.gcmu.Lock()
	var unlockOnce sync.Once
	unlockGC := func() { unlockOnce.Do(srv.gcmu.Unlock) }
	t.Cleanup(unlockGC)

	stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	stopped := make(chan error, 1)
	go func() { stopped <- srv.GracefulStop(stopCtx) }()
	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		// let the stop finish before the test's directories go
		unlockGC()
		<-stopped
		t.Fatal("the stop waited for the running garbage collection")
	}

	require.Zero(t, provider.closes.Load(), "the stop released the network namespaces")
	require.NoError(t, srv.containerdMetaBoltDB.Update(func(*bolt.Tx) error { return nil }), "the stop closed the metadata DBs")
	reopened, err := dagql.NewCache(t.Context(), path, nil, nil)
	require.NoError(t, err)
	defer func() { require.NoError(t, reopened.CloseDiscardingPersistence()) }()
	require.Equal(t, dagql.CachePersistenceResetUncleanShutdown, reopened.PersistenceResetReason(), "the stop saved the cache")
}

func TestGracefulStopWithDiscardedStateKeepsWhatOutlivesTheEngine(t *testing.T) {
	// with state.discarded, what outlives the engine still happens within
	// a CI-sized stop budget: the export in progress lands, the engine's
	// stop reaches its event export, and the cache volume directory is
	// collected within its capacity and given up for the next engine
	srv := newGracefulStopServer(t, filepath.Join(t.TempDir(), "cache.db"))
	srv.stateDiscarded = true
	srv.localCacheGCEnabled = true
	exporter := &engineEventTestExporter{}
	export := &engineEventTestExport{provider: sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(exporter)))}
	srv.engineEvents = newEngineEventEmitter(export.Logger())
	srv.engineEventExport = export
	srv.engineEventShutdownBudget = engineEventShutdownTimeout

	// three volumes of 1 MiB, the oldest first, in a directory provisioned
	// for 3 MiB: its collection keeps them within 80% of that
	const mib = 1 << 20
	dir := filepath.Join(t.TempDir(), "cache-volumes")
	var clock atomic.Int64
	store, err := cachevolumedir.Open(dir, cachevolumedir.Options{EngineVersion: "test", Now: func() time.Time {
		return time.Unix(clock.Add(60), 0)
	}})
	require.NoError(t, err)
	srv.cacheVolumeDir = store
	srv.cacheVolumesCfg.CapacityBytes = 3 * mib
	for _, key := range []string{"old", "mid", "new"} {
		pin, err := store.Acquire(t.Context(), cachevolumedir.Identity{Key: key}, true, func(context.Context, string) error { return nil })
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(pin.Path(), "f"), make([]byte, mib), 0o644))
		pin.Release()
	}

	release := make(chan struct{})
	exported := slowExport(t, srv, release)
	// CI's stop budget is 50s; this one is shorter than the 30s that the
	// later phases of a stop on kept state reserve for themselves
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	srv.BeginGracefulStop()
	stopped := make(chan error, 1)
	go func() { stopped <- srv.GracefulStop(ctx) }()
	select {
	case err := <-exported:
		t.Fatalf("the export ended when the stop began: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
	close(release)
	require.NoError(t, <-exported, "the export didn't land")
	require.NoError(t, <-stopped)

	var stops []telemetryattrs.EngineStopEvent
	for _, event := range exporter.events(t) {
		if event.kind == telemetryattrs.EngineEventStop {
			stops = append(stops, decodeEngineEvent[telemetryattrs.EngineStopEvent](t, event))
		}
	}
	require.Equal(t, []telemetryattrs.EngineStopEvent{{Clean: false, SavedEntries: 0}}, stops, "the stop reports that it saved nothing")

	require.True(t, store.Closed(), "the directory wasn't given up")
	next, err := cachevolumedir.Open(dir, cachevolumedir.Options{EngineVersion: "test"})
	require.NoError(t, err)
	defer func() { require.NoError(t, next.Close()) }()
	entries, _, err := next.Usage(t.Context())
	require.NoError(t, err)
	var keys []string
	for _, e := range entries {
		keys = append(keys, e.Identity.Key)
	}
	require.ElementsMatch(t, []string{"mid", "new"}, keys, "the next engine doesn't get the volumes within their capacity")
}

var errNetworkProviderClose = errors.New("network provider close failed")

type failingNetworkProvider struct{}

func (failingNetworkProvider) New(context.Context, string) (network.Namespace, error) {
	return nil, errors.New("unused")
}
func (failingNetworkProvider) Close() error { return errNetworkProviderClose }

// closeCountingNetworkProvider counts its closes.
type closeCountingNetworkProvider struct {
	closes atomic.Int64
}

func (*closeCountingNetworkProvider) New(context.Context, string) (network.Namespace, error) {
	return nil, errors.New("unused")
}

func (p *closeCountingNetworkProvider) Close() error {
	p.closes.Add(1)
	return nil
}
