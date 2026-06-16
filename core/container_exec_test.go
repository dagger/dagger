package core

import (
	"testing"

	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	"github.com/stretchr/testify/require"
)

func TestResourcesIntoMeta(t *testing.T) {
	t.Parallel()

	t.Run("nil returns zero meta", func(t *testing.T) {
		t.Parallel()
		m := resourcesIntoMeta(nil)
		require.Zero(t, m.MemoryBytes)
		require.Zero(t, m.MemorySoftBytes)
		require.Zero(t, m.CPUQuota)
		require.Zero(t, m.CPUPeriod)
		require.Zero(t, m.CPUShares)
		require.Zero(t, m.PidsLimit)
	})

	t.Run("memory bytes", func(t *testing.T) {
		t.Parallel()
		m := resourcesIntoMeta(&ContainerExecResources{MemoryBytes: 512 * 1024 * 1024})
		require.Equal(t, int64(512*1024*1024), m.MemoryBytes)
		require.Zero(t, m.MemorySoftBytes)
	})

	t.Run("memory soft", func(t *testing.T) {
		t.Parallel()
		m := resourcesIntoMeta(&ContainerExecResources{MemorySoftBytes: 256 * 1024 * 1024})
		require.Equal(t, int64(256*1024*1024), m.MemorySoftBytes)
	})

	t.Run("CPUs 1.5 translates to quota 150000 period 100000", func(t *testing.T) {
		t.Parallel()
		m := resourcesIntoMeta(&ContainerExecResources{CPUs: 1.5})
		require.Equal(t, int64(150000), m.CPUQuota)
		require.Equal(t, uint64(100000), m.CPUPeriod)
	})

	t.Run("CPUs 0 leaves quota and period zero", func(t *testing.T) {
		t.Parallel()
		m := resourcesIntoMeta(&ContainerExecResources{MemoryBytes: 1024})
		require.Zero(t, m.CPUQuota)
		require.Zero(t, m.CPUPeriod)
	})

	t.Run("CPUShares", func(t *testing.T) {
		t.Parallel()
		m := resourcesIntoMeta(&ContainerExecResources{CPUShares: 512})
		require.Equal(t, int64(512), m.CPUShares)
	})

	t.Run("pids", func(t *testing.T) {
		t.Parallel()
		m := resourcesIntoMeta(&ContainerExecResources{Pids: 256})
		require.Equal(t, int64(256), m.PidsLimit)
	})
}

func TestExecNetModeDefault(t *testing.T) {
	t.Parallel()

	mode, err := execNetMode(ContainerExecOpts{})
	require.NoError(t, err)
	require.Equal(t, pb.NetMode_UNSET, mode)
}

func TestExecNetModeNone(t *testing.T) {
	t.Parallel()

	mode, err := execNetMode(ContainerExecOpts{NoNetwork: true})
	require.NoError(t, err)
	require.Equal(t, pb.NetMode_NONE, mode)
}

func TestExecNetModeHost(t *testing.T) {
	t.Parallel()

	mode, err := execNetMode(ContainerExecOpts{HostNetwork: true})
	require.NoError(t, err)
	require.Equal(t, pb.NetMode_HOST, mode)
}

func TestExecNetModeConflict(t *testing.T) {
	t.Parallel()

	_, err := execNetMode(ContainerExecOpts{
		NoNetwork:   true,
		HostNetwork: true,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot set both noNetwork and hostNetwork")
}
