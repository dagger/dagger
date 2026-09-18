package server

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

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

var errNetworkProviderClose = errors.New("network provider close failed")

type failingNetworkProvider struct{}

func (failingNetworkProvider) New(context.Context, string) (network.Namespace, error) {
	return nil, errors.New("unused")
}
func (failingNetworkProvider) Close() error { return errNetworkProviderClose }
