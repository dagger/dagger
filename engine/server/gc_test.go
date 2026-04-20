package server

import (
	"testing"
	"time"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/engine/config"
	bkconfig "github.com/dagger/dagger/internal/buildkit/cmd/buildkitd/config"
	"github.com/dagger/dagger/internal/buildkit/util/disk"
	"github.com/stretchr/testify/require"
)

func TestResolveEngineLocalCachePrunePoliciesUseDefaultPolicyFalse(t *testing.T) {
	dstat := disk.DiskStat{Total: 100 * 1e9}
	defaultPolicy := []dagqlCachePrunePolicy{
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

	prunePolicies, err := resolveEngineLocalCachePrunePolicies(defaultPolicy, opts, dstat)
	require.NoError(t, err)
	require.Len(t, prunePolicies, 1)
	require.True(t, prunePolicies[0].All)
	require.Equal(t, mustParseDiskSpace(t, opts.MaxUsedSpace, dstat), prunePolicies[0].MaxUsedSpace)
	require.Equal(t, mustParseDiskSpace(t, opts.ReservedSpace, dstat), prunePolicies[0].ReservedSpace)
	require.Equal(t, mustParseDiskSpace(t, opts.MinFreeSpace, dstat), prunePolicies[0].MinFreeSpace)
	require.Equal(t, mustParseDiskSpace(t, opts.TargetSpace, dstat), prunePolicies[0].TargetSpace)
}

func TestResolveEngineLocalCachePrunePoliciesOverridesReservedAndMinFree(t *testing.T) {
	dstat := disk.DiskStat{Total: 50 * 1e9}
	defaultPolicy := []dagqlCachePrunePolicy{
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
	originalPolicy := cloneDagqlCachePrunePolicies(defaultPolicy)

	opts := core.EngineCachePruneOptions{
		UseDefaultPolicy: true,
		ReservedSpace:    "123MB",
		MinFreeSpace:     "5%",
	}

	prunePolicies, err := resolveEngineLocalCachePrunePolicies(defaultPolicy, opts, dstat)
	require.NoError(t, err)
	require.Len(t, prunePolicies, len(defaultPolicy))

	wantReserved := mustParseDiskSpace(t, opts.ReservedSpace, dstat)
	wantMinFree := mustParseDiskSpace(t, opts.MinFreeSpace, dstat)
	for i := range prunePolicies {
		require.Equal(t, wantReserved, prunePolicies[i].ReservedSpace)
		require.Equal(t, wantMinFree, prunePolicies[i].MinFreeSpace)
		require.Equal(t, defaultPolicy[i].MaxUsedSpace, prunePolicies[i].MaxUsedSpace)
		require.Equal(t, defaultPolicy[i].TargetSpace, prunePolicies[i].TargetSpace)
	}

	// Ensure default policy was not mutated by per-call overrides.
	require.Equal(t, originalPolicy, defaultPolicy)
}

func TestResolveEngineLocalCachePrunePoliciesInvalidSpaceValue(t *testing.T) {
	dstat := disk.DiskStat{Total: 100 * 1e9}

	_, err := resolveEngineLocalCachePrunePolicies(nil, core.EngineCachePruneOptions{
		UseDefaultPolicy: false,
		ReservedSpace:    "not-a-size",
	}, dstat)
	require.Error(t, err)
	require.ErrorContains(t, err, "invalid reservedSpace value")
}

func TestGetDagqlGCPolicyFromConfiguredPolicies(t *testing.T) {
	cfg := config.Config{
		GC: config.GCConfig{
			Policies: []config.GCPolicy{
				{
					All:          true,
					Filters:      []string{"type==source.local", "id==abc"},
					KeepDuration: config.Duration{Duration: 2 * time.Hour},
					GCSpace: config.GCSpace{
						ReservedSpace: config.DiskSpace{Bytes: 100},
						MaxUsedSpace:  config.DiskSpace{Bytes: 1000},
						MinFreeSpace:  config.DiskSpace{Bytes: 200},
						SweepSize:     config.DiskSpace{Bytes: 300},
					},
				},
			},
		},
	}

	policies := getDagqlGCPolicy(cfg, bkconfig.GCConfig{}, t.TempDir())
	require.Len(t, policies, 1)
	require.Equal(t, dagqlCachePrunePolicy{
		All:           true,
		Filters:       []string{"type==source.local", "id==abc"},
		KeepDuration:  2 * time.Hour,
		ReservedSpace: 100,
		MaxUsedSpace:  1000,
		MinFreeSpace:  200,
		TargetSpace:   700,
	}, policies[0])
}

func TestGetDagqlGCPolicyFallsBackToBuildkitGCPolicy(t *testing.T) {
	bkcfg := bkconfig.GCConfig{
		GCPolicy: []bkconfig.GCPolicy{
			{
				All:          true,
				Filters:      []string{"type==source.git.checkout"},
				KeepDuration: bkconfig.Duration{Duration: 3 * time.Hour},
				ReservedSpace: bkconfig.DiskSpace{
					Bytes: 400,
				},
				MaxUsedSpace: bkconfig.DiskSpace{
					Bytes: 500,
				},
				MinFreeSpace: bkconfig.DiskSpace{
					Bytes: 200,
				},
			},
		},
	}

	policies := getDagqlGCPolicy(config.Config{}, bkcfg, t.TempDir())
	require.Len(t, policies, 1)
	require.Equal(t, dagqlCachePrunePolicy{
		All:           true,
		Filters:       []string{"type==source.git.checkout"},
		KeepDuration:  3 * time.Hour,
		ReservedSpace: 400,
		MaxUsedSpace:  500,
		MinFreeSpace:  200,
		TargetSpace:   0,
	}, policies[0])
}

func mustParseDiskSpace(t *testing.T, value string, dstat disk.DiskStat) int64 {
	t.Helper()
	var parsed bkconfig.DiskSpace
	require.NoError(t, parsed.UnmarshalText([]byte(value)))
	return parsed.AsBytes(dstat)
}
