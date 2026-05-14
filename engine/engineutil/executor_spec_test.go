package engineutil

import (
	"context"
	"testing"

	"github.com/dagger/dagger/internal/buildkit/executor"
	resourcestypes "github.com/dagger/dagger/internal/buildkit/executor/resources/types"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	bknetwork "github.com/dagger/dagger/internal/buildkit/util/network"
	"github.com/dagger/dagger/util/cleanups"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/require"
)

func TestSetupNetworkUsesPoolForDefaultHostname(t *testing.T) {
	provider := &recordingNetworkProvider{}
	client := &Client{
		Opts: &Opts{
			ExecutorRoot: t.TempDir(),
			NetworkProviders: map[pb.NetMode]bknetwork.Provider{
				pb.NetMode_UNSET: provider,
			},
		},
	}
	state := &execState{
		procInfo: &executor.ProcessInfo{
			Meta: executor.Meta{
				NetMode: pb.NetMode_UNSET,
			},
		},
		cleanups: &cleanups.Cleanups{},
	}
	t.Cleanup(func() {
		require.NoError(t, state.cleanups.Run())
	})

	require.NoError(t, client.setupNetwork(context.Background(), state))
	require.Equal(t, defaultHostname, state.procInfo.Meta.Hostname)
	require.Equal(t, []string{""}, provider.hostnames)
}

func TestSetupNetworkPassesCustomHostnameToNetworkProvider(t *testing.T) {
	provider := &recordingNetworkProvider{}
	client := &Client{
		Opts: &Opts{
			ExecutorRoot: t.TempDir(),
			NetworkProviders: map[pb.NetMode]bknetwork.Provider{
				pb.NetMode_UNSET: provider,
			},
		},
	}
	state := &execState{
		procInfo: &executor.ProcessInfo{
			Meta: executor.Meta{
				NetMode:  pb.NetMode_UNSET,
				Hostname: "custom",
			},
		},
		cleanups: &cleanups.Cleanups{},
	}
	t.Cleanup(func() {
		require.NoError(t, state.cleanups.Run())
	})

	require.NoError(t, client.setupNetwork(context.Background(), state))
	require.Equal(t, "custom", state.procInfo.Meta.Hostname)
	require.Equal(t, []string{"custom"}, provider.hostnames)
}

func TestSetupNetworkKeepsInsecureExecsOutOfPool(t *testing.T) {
	provider := &recordingNetworkProvider{}
	client := &Client{
		Opts: &Opts{
			ExecutorRoot: t.TempDir(),
			NetworkProviders: map[pb.NetMode]bknetwork.Provider{
				pb.NetMode_UNSET: provider,
			},
		},
	}
	state := &execState{
		procInfo: &executor.ProcessInfo{
			Meta: executor.Meta{
				NetMode:      pb.NetMode_UNSET,
				SecurityMode: pb.SecurityMode_INSECURE,
			},
		},
		cleanups: &cleanups.Cleanups{},
	}
	t.Cleanup(func() {
		require.NoError(t, state.cleanups.Run())
	})

	require.NoError(t, client.setupNetwork(context.Background(), state))
	require.NotEmpty(t, state.procInfo.Meta.Hostname)
	require.NotEqual(t, defaultHostname, state.procInfo.Meta.Hostname)
	require.Equal(t, []string{state.procInfo.Meta.Hostname}, provider.hostnames)
}

type recordingNetworkProvider struct {
	hostnames []string
}

var _ bknetwork.Provider = (*recordingNetworkProvider)(nil)

func (p *recordingNetworkProvider) New(_ context.Context, hostname string) (bknetwork.Namespace, error) {
	p.hostnames = append(p.hostnames, hostname)
	return &noopNetworkNamespace{}, nil
}

func (p *recordingNetworkProvider) Close() error {
	return nil
}

type noopNetworkNamespace struct{}

var _ bknetwork.Namespace = (*noopNetworkNamespace)(nil)

func (ns *noopNetworkNamespace) Close() error {
	return nil
}

func (ns *noopNetworkNamespace) Set(*specs.Spec) error {
	return nil
}

func (ns *noopNetworkNamespace) Sample() (*resourcestypes.NetworkSample, error) {
	return nil, nil
}
