package server

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	bolt "go.etcd.io/bbolt"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/engineutil"
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
