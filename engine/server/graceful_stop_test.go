package server

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/snapshots/storage"
	"github.com/stretchr/testify/require"
	bolt "go.etcd.io/bbolt"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/engineutil"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	"github.com/dagger/dagger/internal/buildkit/util/network"
)

func boundedContext(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// newGracefulStopServer has the state GracefulStop closes, with a persistent
// cache and no sessions.
func newGracefulStopServer(t *testing.T, cachePath string) *Server {
	t.Helper()
	dir := t.TempDir()
	cache, err := dagql.NewCache(t.Context(), cachePath, nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.CloseDiscardingPersistence() })
	meta, err := storage.NewMetaStore(filepath.Join(dir, "snapshots.db"))
	require.NoError(t, err)
	db, err := bolt.Open(filepath.Join(dir, "containerdmeta.db"), 0o600, nil)
	require.NoError(t, err)
	ctx, cancel := context.WithCancelCause(context.Background())
	t.Cleanup(func() { cancel(nil) })
	addGCTestPersistable(t, cache, "graceful", "gracefulRoot", dagql.NewInt(1))
	return &Server{rootDir: dir, workerRootDir: dir, engineCache: cache, snapshotterMDStore: meta, containerdMetaBoltDB: db, shutdownCtx: ctx, shutdownCancel: cancel}
}

func TestGracefulStopReturnsEarlierShutdownErrors(t *testing.T) {
	srv := newGracefulStopServer(t, filepath.Join(t.TempDir(), "cache.db"))
	// GracefulStop used to collect this close error and then drop it.
	srv.engineUtilOpts = &engineutil.Opts{NetworkProviders: map[pb.NetMode]network.Provider{pb.NetMode_UNSET: failingNetworkProvider{}}}
	err := srv.GracefulStop(boundedContext(t))
	require.ErrorIs(t, err, errNetworkProviderClose)
}

var errNetworkProviderClose = errors.New("network provider close failed")

type failingNetworkProvider struct{}

func (failingNetworkProvider) New(context.Context, string) (network.Namespace, error) {
	return nil, errors.New("unused")
}
func (failingNetworkProvider) Close() error { return errNetworkProviderClose }
