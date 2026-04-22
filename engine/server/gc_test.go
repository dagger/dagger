package server

import (
	"testing"

	"github.com/dagger/dagger/core"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	bkconfig "github.com/dagger/dagger/internal/buildkit/cmd/buildkitd/config"
	"github.com/dagger/dagger/internal/buildkit/util/disk"
	"github.com/stretchr/testify/require"
)

func TestResolveEngineLocalCachePruneOptionsUseDefaultPolicyFalse(t *testing.T) {
	dstat := disk.DiskStat{Total: 100 * 1e9}
	defaultPolicy := []bkclient.PruneInfo{
		{
			All:           false,
			MaxUsedSpace:  11,
			ReservedSpace: 22,
			MinFreeSpace:  33,
			TargetSpace:   44,
		},
	}

	opts := core.EngineCachePruneOptions{
		UseDefaultPolicy: false,
		MaxUsedSpace:     "3GB",
		ReservedSpace:    "500MB",
		MinFreeSpace:     "10%",
		TargetSpace:      "2GB",
	}

	pruneOpts, err := resolveEngineLocalCachePruneOptions(defaultPolicy, opts, dstat)
	require.NoError(t, err)
	require.Len(t, pruneOpts, 1)
	require.True(t, pruneOpts[0].All)
	require.Equal(t, mustParseDiskSpace(t, opts.MaxUsedSpace, dstat), pruneOpts[0].MaxUsedSpace)
	require.Equal(t, mustParseDiskSpace(t, opts.ReservedSpace, dstat), pruneOpts[0].ReservedSpace)
	require.Equal(t, mustParseDiskSpace(t, opts.MinFreeSpace, dstat), pruneOpts[0].MinFreeSpace)
	require.Equal(t, mustParseDiskSpace(t, opts.TargetSpace, dstat), pruneOpts[0].TargetSpace)
}

func TestResolveEngineLocalCachePruneOptionsOverridesReservedAndMinFree(t *testing.T) {
	dstat := disk.DiskStat{Total: 50 * 1e9}
	defaultPolicy := []bkclient.PruneInfo{
		{
			All:           false,
			MaxUsedSpace:  100,
			ReservedSpace: 200,
			MinFreeSpace:  300,
			TargetSpace:   400,
		},
		{
			All:           true,
			MaxUsedSpace:  500,
			ReservedSpace: 600,
			MinFreeSpace:  700,
			TargetSpace:   800,
		},
	}
	originalPolicy := append([]bkclient.PruneInfo(nil), defaultPolicy...)

	opts := core.EngineCachePruneOptions{
		UseDefaultPolicy: true,
		ReservedSpace:    "123MB",
		MinFreeSpace:     "5%",
	}

	pruneOpts, err := resolveEngineLocalCachePruneOptions(defaultPolicy, opts, dstat)
	require.NoError(t, err)
	require.Len(t, pruneOpts, len(defaultPolicy))

	wantReserved := mustParseDiskSpace(t, opts.ReservedSpace, dstat)
	wantMinFree := mustParseDiskSpace(t, opts.MinFreeSpace, dstat)
	for i := range pruneOpts {
		require.Equal(t, wantReserved, pruneOpts[i].ReservedSpace)
		require.Equal(t, wantMinFree, pruneOpts[i].MinFreeSpace)
		require.Equal(t, defaultPolicy[i].MaxUsedSpace, pruneOpts[i].MaxUsedSpace)
		require.Equal(t, defaultPolicy[i].TargetSpace, pruneOpts[i].TargetSpace)
	}

	// Ensure default policy was not mutated by per-call overrides.
	require.Equal(t, originalPolicy, defaultPolicy)
}

func TestResolveEngineLocalCachePruneOptionsInvalidSpaceValue(t *testing.T) {
	dstat := disk.DiskStat{Total: 100 * 1e9}

	_, err := resolveEngineLocalCachePruneOptions(nil, core.EngineCachePruneOptions{
		UseDefaultPolicy: false,
		ReservedSpace:    "not-a-size",
	}, dstat)
	require.Error(t, err)
	require.ErrorContains(t, err, "invalid reservedSpace value")
}

func mustParseDiskSpace(t *testing.T, value string, dstat disk.DiskStat) int64 {
	t.Helper()
	var parsed bkconfig.DiskSpace
	require.NoError(t, parsed.UnmarshalText([]byte(value)))
	return parsed.AsBytes(dstat)
}
