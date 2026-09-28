package engineutil

import (
	"context"
	"testing"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/dagger/dagger/internal/buildkit/executor"
	resourcestypes "github.com/dagger/dagger/internal/buildkit/executor/resources/types"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	bknetwork "github.com/dagger/dagger/internal/buildkit/util/network"
	"github.com/dagger/dagger/util/cleanups"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/require"
)

// Exercise the real OCI spec generator, including containerd's default path.
// This is the boundary runc uses to place withExec and service processes; it
// must not inherit the engine's /init cgroup as its default parent.
func TestGenerateBaseSpecCgroupParent(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name          string
		defaultParent string
		execParent    string
		want          string
	}{
		{name: "default", want: "/buildkit/exec-id"},
		{name: "explicit root", defaultParent: "/", want: "/buildkit/exec-id"},
		{name: "configured parent", defaultParent: "/workloads", want: "/workloads/buildkit/exec-id"},
		{name: "relative parent", defaultParent: "workloads", want: "/workloads/buildkit/exec-id"},
		{name: "exec overrides configured parent", defaultParent: "/workloads", execParent: "/other", want: "/other/buildkit/exec-id"},
		{name: "systemd parent", defaultParent: "workloads.slice:dagger:", want: "workloads.slice:dagger:exec-id"},
		{name: "explicit engine descendant", defaultParent: "/init", want: "/init/buildkit/exec-id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			client := &Client{Opts: &Opts{DefaultCgroupParent: tc.defaultParent}}
			state := &execState{
				id: "exec-id",
				procInfo: &executor.ProcessInfo{Meta: executor.Meta{
					Args:         []string{"sh"},
					Cwd:          "/",
					CgroupParent: tc.execParent,
				}},
				networkNamespace: &noopNetworkNamespace{},
				cleanups:         &cleanups.Cleanups{},
			}
			t.Cleanup(func() { require.NoError(t, state.cleanups.Run()) })

			require.NoError(t, client.generateBaseSpec(t.Context(), state))
			require.Equal(t, tc.want, state.spec.Linux.CgroupsPath)
		})
	}
}

func TestConsumeRecursiveReadOnlyOption(t *testing.T) {
	t.Parallel()

	input := mount.Mount{Type: "bind", Source: "/source", Options: []string{"rbind", "rro", "nosuid"}}
	got, recursive := consumeRecursiveReadOnlyOption(input)
	require.True(t, recursive)
	require.Equal(t, []string{"rbind", "nosuid"}, got.Options)
	// The original is retained for diagnostics and other consumers.
	require.Equal(t, []string{"rbind", "rro", "nosuid"}, input.Options)

	got, recursive = consumeRecursiveReadOnlyOption(mount.Mount{Options: []string{"rbind", "ro"}})
	require.False(t, recursive)
	require.Equal(t, []string{"rbind", "ro"}, got.Options)
}

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
