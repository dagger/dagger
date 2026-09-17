package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/config"
	bkconfig "github.com/dagger/dagger/internal/buildkit/cmd/buildkitd/config"
	"github.com/dagger/dagger/internal/buildkit/util/disk"
	"github.com/stretchr/testify/require"
)

type gcTestTypeResolver struct{}

func (gcTestTypeResolver) ObjectType(string) (dagql.ObjectType, bool) {
	return nil, false
}

func (gcTestTypeResolver) ScalarType(string) (dagql.ScalarType, bool) {
	return nil, false
}

type gcTestSizedInt struct {
	dagql.Int
	sizeBytes int64
	identity  string
}

func (v gcTestSizedInt) CacheUsageIdentities() []string {
	return []string{v.identity}
}

func (v gcTestSizedInt) CacheUsageSize(context.Context, dagql.CacheUsageSizeProvider, string) (int64, bool, error) {
	return v.sizeBytes, true, nil
}

type gcTestTrackedInt struct {
	dagql.Int
	sizeBytes  int64
	identity   string
	sizeCalls  *atomic.Int32
	releaseErr error
}

func (v gcTestTrackedInt) CacheUsageIdentities() []string {
	return []string{v.identity}
}

func (v gcTestTrackedInt) CacheUsageSize(context.Context, dagql.CacheUsageSizeProvider, string) (int64, bool, error) {
	if v.sizeCalls != nil {
		v.sizeCalls.Add(1)
	}
	return v.sizeBytes, true, nil
}

func (v gcTestTrackedInt) OnRelease(context.Context) error {
	return v.releaseErr
}

func addGCTestPersistable(t *testing.T, cache *dagql.Cache, sessionID, name string, value dagql.Typed) context.Context {
	t.Helper()
	ctx, _ := addGCTestPersistableResult(t, cache, sessionID, name, value)
	return ctx
}

func addGCTestPersistableResult(t *testing.T, cache *dagql.Cache, sessionID, name string, value dagql.Typed) (context.Context, dagql.AnyResult) {
	t.Helper()
	ctx := engine.ContextWithClientMetadata(t.Context(), &engine.ClientMetadata{
		ClientID:  sessionID,
		SessionID: sessionID,
	})
	frame := &dagql.ResultCall{
		Kind:  dagql.ResultCallKindField,
		Type:  dagql.NewResultCallType(value.Type()),
		Field: name,
	}
	res, err := cache.GetOrInitCall(ctx, sessionID, gcTestTypeResolver{}, &dagql.CallRequest{
		ResultCall:    frame,
		IsPersistable: true,
	}, func(context.Context) (dagql.AnyResult, error) {
		return dagql.NewResultForCall(value, frame)
	})
	require.NoError(t, err)
	return ctx, res
}

func newGCTestCache(t *testing.T) *dagql.Cache {
	t.Helper()
	cache, err := dagql.NewCache(t.Context(), "", nil, nil)
	require.NoError(t, err)
	return cache
}

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

func TestResolveEngineLocalCachePrunePoliciesUsesAvailableSpaceForMinFree(t *testing.T) {
	dstat := disk.DiskStat{
		Total:     100 * 1e9,
		Free:      25 * 1e9,
		Available: 15 * 1e9,
	}
	defaultPolicy := []dagqlCachePrunePolicy{
		{
			All:           true,
			ReservedSpace: 10 * 1e9,
			MaxUsedSpace:  75 * 1e9,
			MinFreeSpace:  20 * 1e9,
		},
	}

	prunePolicies, err := resolveEngineLocalCachePrunePolicies(defaultPolicy, core.EngineCachePruneOptions{
		UseDefaultPolicy: true,
	}, dstat)
	require.NoError(t, err)
	require.Len(t, prunePolicies, 1)
	require.Equal(t, dstat.Available, prunePolicies[0].CurrentFreeSpace)
	require.Less(t, prunePolicies[0].CurrentFreeSpace, prunePolicies[0].MinFreeSpace)
	require.GreaterOrEqual(t, dstat.Free, prunePolicies[0].MinFreeSpace)
}

func TestEngineLocalCachePruneModes(t *testing.T) {
	maximum := int64(100)
	target := int64(50)

	for _, tc := range []struct {
		name         string
		opts         core.EngineCachePruneOptions
		automaticGC  bool
		wantDisk     bool
		wantMetadata bool
	}{
		{name: "no options", wantDisk: true},
		{
			name:     "disk only",
			opts:     core.EngineCachePruneOptions{MaxUsedSpace: "1GB"},
			wantDisk: true,
		},
		{
			name: "metadata only",
			opts: core.EngineCachePruneOptions{
				MaxEstimatedBytes:    &maximum,
				TargetEstimatedBytes: &target,
			},
			wantMetadata: true,
		},
		{
			name: "combined",
			opts: core.EngineCachePruneOptions{
				MaxUsedSpace:         "1GB",
				MaxEstimatedBytes:    &maximum,
				TargetEstimatedBytes: &target,
			},
			wantDisk:     true,
			wantMetadata: true,
		},
		{
			name:         "enabled default policy",
			opts:         core.EngineCachePruneOptions{UseDefaultPolicy: true},
			automaticGC:  true,
			wantDisk:     true,
			wantMetadata: true,
		},
		{
			name:     "disabled default policy",
			opts:     core.EngineCachePruneOptions{UseDefaultPolicy: true},
			wantDisk: true,
		},
		{
			name: "disabled default policy with explicit metadata",
			opts: core.EngineCachePruneOptions{
				UseDefaultPolicy:     true,
				MaxEstimatedBytes:    &maximum,
				TargetEstimatedBytes: &target,
			},
			wantDisk:     true,
			wantMetadata: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			disk, metadata := engineLocalCachePruneModes(tc.opts, tc.automaticGC)
			require.Equal(t, tc.wantDisk, disk)
			require.Equal(t, tc.wantMetadata, metadata)
		})
	}
}

func TestResolveEngineLocalCacheMetadataPruneOptions(t *testing.T) {
	const (
		defaultMaximum = int64(1000)
		defaultTarget  = int64(700)
	)

	maximumOverride := int64(1200)
	targetOverride := int64(600)
	zero := int64(0)
	negative := int64(-1)
	conflictingMaximum := int64(600)
	conflictingTarget := int64(1000)

	for _, tc := range []struct {
		name        string
		opts        core.EngineCachePruneOptions
		wantMaximum int64
		wantTarget  int64
		wantError   string
	}{
		{
			name:        "default policy",
			opts:        core.EngineCachePruneOptions{UseDefaultPolicy: true},
			wantMaximum: defaultMaximum,
			wantTarget:  defaultTarget,
		},
		{
			name:        "maximum only inherits target",
			opts:        core.EngineCachePruneOptions{MaxEstimatedBytes: &maximumOverride},
			wantMaximum: maximumOverride,
			wantTarget:  defaultTarget,
		},
		{
			name:        "target only inherits maximum",
			opts:        core.EngineCachePruneOptions{TargetEstimatedBytes: &targetOverride},
			wantMaximum: defaultMaximum,
			wantTarget:  targetOverride,
		},
		{
			name: "explicit pair overrides defaults",
			opts: core.EngineCachePruneOptions{
				MaxEstimatedBytes:    &maximumOverride,
				TargetEstimatedBytes: &targetOverride,
			},
			wantMaximum: maximumOverride,
			wantTarget:  targetOverride,
		},
		{
			name:      "zero maximum",
			opts:      core.EngineCachePruneOptions{MaxEstimatedBytes: &zero},
			wantError: "maxEstimatedBytes must be positive",
		},
		{
			name:      "zero target",
			opts:      core.EngineCachePruneOptions{TargetEstimatedBytes: &zero},
			wantError: "targetEstimatedBytes must be positive and lower",
		},
		{
			name:      "negative maximum",
			opts:      core.EngineCachePruneOptions{MaxEstimatedBytes: &negative},
			wantError: "maxEstimatedBytes must be positive",
		},
		{
			name:      "negative target",
			opts:      core.EngineCachePruneOptions{TargetEstimatedBytes: &negative},
			wantError: "targetEstimatedBytes must be positive and lower",
		},
		{
			name:      "partial maximum conflicts with inherited target",
			opts:      core.EngineCachePruneOptions{MaxEstimatedBytes: &conflictingMaximum},
			wantError: "targetEstimatedBytes must be positive and lower",
		},
		{
			name:      "partial target conflicts with inherited maximum",
			opts:      core.EngineCachePruneOptions{TargetEstimatedBytes: &conflictingTarget},
			wantError: "targetEstimatedBytes must be positive and lower",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			maximum, target, err := resolveEngineLocalCacheMetadataPruneOptions(
				defaultMaximum,
				defaultTarget,
				tc.opts,
			)
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantMaximum, maximum)
			require.Equal(t, tc.wantTarget, target)
		})
	}
}

func TestManualMetadataPruneOnlySkipsDiskAndProtectsRoots(t *testing.T) {
	cache := newGCTestCache(t)
	sizeCalls := &atomic.Int32{}
	activeCtx := addGCTestPersistable(t, cache, "manual-metadata-active", "manualMetadataActive", gcTestTrackedInt{
		Int:       dagql.NewInt(1),
		sizeBytes: 100,
		identity:  "manual-metadata-active",
		sizeCalls: sizeCalls,
	})
	coldCtx := addGCTestPersistable(t, cache, "manual-metadata-cold", "manualMetadataCold", dagql.NewInt(2))
	require.NoError(t, cache.ReleaseSession(coldCtx, "manual-metadata-cold"))
	unpruneableCtx, unpruneable := addGCTestPersistableResult(t, cache, "manual-metadata-unpruneable", "manualMetadataUnpruneable", dagql.NewInt(3))
	require.NoError(t, cache.MakeResultUnpruneable(unpruneableCtx, unpruneable))
	require.NoError(t, cache.ReleaseSession(unpruneableCtx, "manual-metadata-unpruneable"))

	before := cache.MetadataEstimate()
	maximum := before.EstimatedBytes - 1
	target := int64(1)
	srv := &Server{
		rootDir:                        t.TempDir(),
		engineCache:                    cache,
		localCacheGCEnabled:            false,
		dagqlCacheMaxEstimatedBytes:    1000,
		dagqlCacheTargetEstimatedBytes: 700,
	}
	set, err := srv.PruneEngineLocalCacheEntries(t.Context(), core.EngineCachePruneOptions{
		MaxEstimatedBytes:    &maximum,
		TargetEstimatedBytes: &target,
	})
	require.NoError(t, err)
	require.Zero(t, set.EntryCount)
	require.Zero(t, sizeCalls.Load(), "metadata-only pruning must not measure physical disk usage")
	require.Equal(t, 2, cache.EntryStats().RetainedCalls, "active and unpruneable roots must survive while the cold root is removed")
	require.Less(t, cache.MetadataEstimate().EstimatedBytes, before.EstimatedBytes)
	require.NoError(t, cache.ReleaseSession(activeCtx, "manual-metadata-active"))
}

func TestManualMetadataPruneBelowMaximumIsNoOp(t *testing.T) {
	cache := newGCTestCache(t)
	sizeCalls := &atomic.Int32{}
	ctx := addGCTestPersistable(t, cache, "manual-metadata-below-maximum", "manualMetadataBelowMaximum", gcTestTrackedInt{
		Int:       dagql.NewInt(1),
		sizeBytes: 100,
		identity:  "manual-metadata-below-maximum",
		sizeCalls: sizeCalls,
	})
	require.NoError(t, cache.ReleaseSession(ctx, "manual-metadata-below-maximum"))

	before := cache.MetadataEstimate()
	maximum := before.EstimatedBytes + 1
	target := int64(1)
	srv := &Server{rootDir: t.TempDir(), engineCache: cache}
	set, err := srv.PruneEngineLocalCacheEntries(t.Context(), core.EngineCachePruneOptions{
		MaxEstimatedBytes:    &maximum,
		TargetEstimatedBytes: &target,
	})
	require.NoError(t, err)
	require.Zero(t, set.EntryCount)
	require.Zero(t, sizeCalls.Load(), "metadata-only pruning must not measure physical disk usage")
	require.Equal(t, 1, cache.EntryStats().RetainedCalls, "an estimate below the maximum must not be pruned toward the target")
	require.Equal(t, before, cache.MetadataEstimate())
}

func TestManualCombinedDiskAndMetadataPrune(t *testing.T) {
	cache := newGCTestCache(t)
	sizeCalls := &atomic.Int32{}
	diskCtx := addGCTestPersistable(t, cache, "manual-combined-disk", "manualCombinedDisk", gcTestTrackedInt{
		Int:       dagql.NewInt(1),
		sizeBytes: 100,
		identity:  "manual-combined-disk",
		sizeCalls: sizeCalls,
	})
	require.NoError(t, cache.ReleaseSession(diskCtx, "manual-combined-disk"))
	metadataCtx := addGCTestPersistable(t, cache, "manual-combined-metadata", "manualCombinedMetadata", dagql.NewInt(2))
	require.NoError(t, cache.ReleaseSession(metadataCtx, "manual-combined-metadata"))

	maximum := int64(2)
	target := int64(1)
	srv := &Server{rootDir: t.TempDir(), engineCache: cache}
	set, err := srv.PruneEngineLocalCacheEntries(t.Context(), core.EngineCachePruneOptions{
		MaxUsedSpace:         "1",
		TargetSpace:          "1",
		MaxEstimatedBytes:    &maximum,
		TargetEstimatedBytes: &target,
	})
	require.NoError(t, err)
	require.Equal(t, 1, set.EntryCount, "the detailed response contains only the disk-stage removal")
	require.Greater(t, sizeCalls.Load(), int32(0), "the combined request must run the disk stage")
	// The disk report contains the physically sized root while no roots remain,
	// proving that disk pruning ran before structural pruning removed the
	// zero-disk root left behind by the disk stage.
	require.Zero(t, cache.EntryStats().RetainedCalls, "both stages must complete before the manual call returns")
}

func TestManualUseDefaultPolicyIncludesMetadata(t *testing.T) {
	cache := newGCTestCache(t)
	ctx := addGCTestPersistable(t, cache, "manual-default-metadata", "manualDefaultMetadata", dagql.NewInt(1))
	require.NoError(t, cache.ReleaseSession(ctx, "manual-default-metadata"))

	srv := &Server{
		rootDir:                        t.TempDir(),
		engineCache:                    cache,
		workerGCPolicies:               []dagqlCachePrunePolicy{{All: true, MaxUsedSpace: 1 << 40}},
		localCacheGCEnabled:            true,
		dagqlCacheMaxEstimatedBytes:    2,
		dagqlCacheTargetEstimatedBytes: 1,
	}
	_, err := srv.PruneEngineLocalCacheEntries(t.Context(), core.EngineCachePruneOptions{UseDefaultPolicy: true})
	require.NoError(t, err)
	require.Zero(t, cache.EntryStats().RetainedCalls, "configured structural pruning must occur in the same manual call")
}

func TestManualNoOptionPrunePreservesDiskPruneAll(t *testing.T) {
	cache := newGCTestCache(t)
	sizeCalls := &atomic.Int32{}
	ctx := addGCTestPersistable(t, cache, "manual-no-options", "manualNoOptions", gcTestTrackedInt{
		Int:       dagql.NewInt(1),
		sizeBytes: 100,
		identity:  "manual-no-options",
		sizeCalls: sizeCalls,
	})
	require.NoError(t, cache.ReleaseSession(ctx, "manual-no-options"))

	srv := &Server{rootDir: t.TempDir(), engineCache: cache}
	set, err := srv.PruneEngineLocalCacheEntries(t.Context(), core.EngineCachePruneOptions{})
	require.NoError(t, err)
	require.Equal(t, 1, set.EntryCount)
	require.Greater(t, sizeCalls.Load(), int32(0))
	require.Zero(t, cache.EntryStats().RetainedCalls)
}

func TestManualMetadataPruneCancellationAndReleaseError(t *testing.T) {
	t.Run("cancellation", func(t *testing.T) {
		cache := newGCTestCache(t)
		ctx := addGCTestPersistable(t, cache, "manual-metadata-canceled", "manualMetadataCanceled", dagql.NewInt(1))
		require.NoError(t, cache.ReleaseSession(ctx, "manual-metadata-canceled"))
		maximum := cache.MetadataEstimate().EstimatedBytes - 1
		target := int64(1)
		canceled, cancel := context.WithCancel(t.Context())
		cancel()

		srv := &Server{rootDir: t.TempDir(), engineCache: cache}
		_, err := srv.PruneEngineLocalCacheEntries(canceled, core.EngineCachePruneOptions{
			MaxEstimatedBytes:    &maximum,
			TargetEstimatedBytes: &target,
		})
		require.ErrorIs(t, err, context.Canceled)
		require.Equal(t, 1, cache.EntryStats().RetainedCalls)
	})

	t.Run("release error", func(t *testing.T) {
		cache := newGCTestCache(t)
		releaseErr := errors.New("manual metadata release failed")
		ctx := addGCTestPersistable(t, cache, "manual-metadata-release-error", "manualMetadataReleaseError", gcTestTrackedInt{
			Int:        dagql.NewInt(1),
			identity:   "manual-metadata-release-error",
			releaseErr: releaseErr,
		})
		require.NoError(t, cache.ReleaseSession(ctx, "manual-metadata-release-error"))
		maximum := cache.MetadataEstimate().EstimatedBytes - 1
		target := int64(1)

		srv := &Server{rootDir: t.TempDir(), engineCache: cache}
		_, err := srv.PruneEngineLocalCacheEntries(t.Context(), core.EngineCachePruneOptions{
			MaxEstimatedBytes:    &maximum,
			TargetEstimatedBytes: &target,
		})
		require.ErrorIs(t, err, releaseErr)
		require.Zero(t, cache.EntryStats().RetainedCalls, "the underlying release error must be returned after the root is cut")
	})
}

func TestManualCombinedPruneValidatesBeforeMutation(t *testing.T) {
	t.Run("disk options before metadata mutation", func(t *testing.T) {
		cache := newGCTestCache(t)
		ctx := addGCTestPersistable(t, cache, "manual-combined-invalid-disk", "manualCombinedInvalidDisk", dagql.NewInt(1))
		require.NoError(t, cache.ReleaseSession(ctx, "manual-combined-invalid-disk"))
		maximum := cache.MetadataEstimate().EstimatedBytes - 1
		target := int64(1)

		srv := &Server{rootDir: t.TempDir(), engineCache: cache}
		_, err := srv.PruneEngineLocalCacheEntries(t.Context(), core.EngineCachePruneOptions{
			MaxUsedSpace:         "not-a-size",
			MaxEstimatedBytes:    &maximum,
			TargetEstimatedBytes: &target,
		})
		require.ErrorContains(t, err, "invalid maxUsedSpace value")
		require.Equal(t, 1, cache.EntryStats().RetainedCalls, "neither stage may mutate before the complete request validates")
	})

	t.Run("metadata options before disk mutation", func(t *testing.T) {
		cache := newGCTestCache(t)
		sizeCalls := &atomic.Int32{}
		ctx := addGCTestPersistable(t, cache, "manual-combined-invalid-metadata", "manualCombinedInvalidMetadata", gcTestTrackedInt{
			Int:       dagql.NewInt(1),
			sizeBytes: 100,
			identity:  "manual-combined-invalid-metadata",
			sizeCalls: sizeCalls,
		})
		require.NoError(t, cache.ReleaseSession(ctx, "manual-combined-invalid-metadata"))
		maximum := int64(0)
		target := int64(1)

		srv := &Server{rootDir: t.TempDir(), engineCache: cache}
		_, err := srv.PruneEngineLocalCacheEntries(t.Context(), core.EngineCachePruneOptions{
			MaxUsedSpace:         "1",
			TargetSpace:          "1",
			MaxEstimatedBytes:    &maximum,
			TargetEstimatedBytes: &target,
		})
		require.ErrorContains(t, err, "maxEstimatedBytes must be positive")
		require.Zero(t, sizeCalls.Load(), "disk pruning must not measure physical usage before metadata options validate")
		require.Equal(t, 1, cache.EntryStats().RetainedCalls, "neither stage may mutate before the complete request validates")
	})
}

func TestLocalCacheDiskPressureGCNeeded(t *testing.T) {
	dstat := disk.DiskStat{Available: 100}

	require.False(t, localCacheDiskPressureGCNeeded(nil, dstat))
	require.False(t, localCacheDiskPressureGCNeeded([]dagqlCachePrunePolicy{
		{MinFreeSpace: 100},
	}, dstat))
	require.True(t, localCacheDiskPressureGCNeeded([]dagqlCachePrunePolicy{
		{MinFreeSpace: 101},
	}, dstat))
	require.True(t, localCacheDiskPressureGCNeeded([]dagqlCachePrunePolicy{
		{MinFreeSpace: 0},
		{MinFreeSpace: 101},
	}, dstat))
	require.False(t, localCacheDiskPressureGCNeeded([]dagqlCachePrunePolicy{
		{MaxUsedSpace: 1},
	}, dstat))
}

func TestResolveDagqlCacheGCConfig(t *testing.T) {
	t.Parallel()

	enabled, maximum, target, err := resolveDagqlCacheGCConfig(config.GCConfig{}, bkconfig.GCConfig{})
	require.NoError(t, err)
	require.True(t, enabled)
	require.Equal(t, int64(4<<30), maximum)
	require.Equal(t, int64(3<<30), target)

	enabled, maximum, target, err = resolveDagqlCacheGCConfig(config.GCConfig{
		DagqlCache: config.DagqlCacheGCConfig{
			MaxEstimatedBytes:    1000,
			TargetEstimatedBytes: 700,
		},
	}, bkconfig.GCConfig{})
	require.NoError(t, err)
	require.True(t, enabled)
	require.Equal(t, int64(1000), maximum)
	require.Equal(t, int64(700), target)

	disabled := false
	enabled, _, _, err = resolveDagqlCacheGCConfig(config.GCConfig{Enabled: &disabled}, bkconfig.GCConfig{})
	require.NoError(t, err)
	require.False(t, enabled)

	enabled, maximum, target, err = resolveDagqlCacheGCConfig(config.GCConfig{
		DagqlCache: config.DagqlCacheGCConfig{MaxEstimatedBytes: -1},
	}, bkconfig.GCConfig{})
	require.ErrorContains(t, err, "maxEstimatedBytes must be positive")
	require.ErrorContains(t, err, "resolved maxEstimatedBytes=-1, targetEstimatedBytes=3221225472")
	require.False(t, enabled)
	require.Zero(t, maximum)
	require.Zero(t, target)

	enabled, maximum, target, err = resolveDagqlCacheGCConfig(config.GCConfig{
		DagqlCache: config.DagqlCacheGCConfig{MaxEstimatedBytes: 100, TargetEstimatedBytes: 100},
	}, bkconfig.GCConfig{})
	require.ErrorContains(t, err, "targetEstimatedBytes must be positive and lower")
	require.ErrorContains(t, err, "resolved maxEstimatedBytes=100, targetEstimatedBytes=100")
	require.False(t, enabled)
	require.Zero(t, maximum)
	require.Zero(t, target)

	enabled, maximum, target, err = resolveDagqlCacheGCConfig(config.GCConfig{
		DagqlCache: config.DagqlCacheGCConfig{MaxEstimatedBytes: 2 << 30},
	}, bkconfig.GCConfig{})
	require.ErrorContains(t, err, "targetEstimatedBytes must be positive and lower")
	require.ErrorContains(t, err, "resolved maxEstimatedBytes=2147483648, targetEstimatedBytes=3221225472")
	require.False(t, enabled)
	require.Zero(t, maximum)
	require.Zero(t, target)
}

func TestLocalCacheResetPreservesClientTelemetry(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	srv := &Server{
		rootDir:       root,
		workerRootDir: filepath.Join(root, "worker"),
		clientDBDir:   filepath.Join(root, "clientdbs"),
	}
	workerMarker := filepath.Join(srv.workerRootDir, "cache", "marker")
	archiveMarker := filepath.Join(srv.clientDBDir, "archives", "session.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(workerMarker), 0o700))
	require.NoError(t, os.WriteFile(workerMarker, []byte("disposable"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Dir(archiveMarker), 0o700))
	require.NoError(t, os.WriteFile(archiveMarker, []byte("retained"), 0o600))

	require.NoError(t, srv.removeLocalCacheStateOnDisk())
	require.NoFileExists(t, workerMarker)
	require.FileExists(t, archiveMarker)
	contents, err := os.ReadFile(archiveMarker)
	require.NoError(t, err)
	require.Equal(t, "retained", string(contents))
}

func TestMigrateLegacyClientDBDir(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	srv := &Server{
		workerRootDir: filepath.Join(root, "worker"),
		clientDBDir:   filepath.Join(root, "clientdbs"),
	}
	legacyArchive := filepath.Join(srv.workerRootDir, "clientdbs", "archives", "legacy.json")
	currentArchive := filepath.Join(srv.clientDBDir, "archives", "current.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(legacyArchive), 0o700))
	require.NoError(t, os.WriteFile(legacyArchive, []byte("legacy"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Dir(currentArchive), 0o700))
	require.NoError(t, os.WriteFile(currentArchive, []byte("current"), 0o600))

	require.NoError(t, srv.migrateLegacyClientDBDir())
	require.NoDirExists(t, filepath.Join(srv.workerRootDir, "clientdbs"))
	require.FileExists(t, filepath.Join(srv.clientDBDir, "archives", "legacy.json"))
	require.FileExists(t, currentArchive)
}

func TestGCLockedPrunesMetadataWithoutWorkerDiskPolicies(t *testing.T) {
	t.Parallel()

	cache := newGCTestCache(t)
	ctx := addGCTestPersistable(t, cache, "metadata-no-disk-policies", "metadataNoDiskPolicies", dagql.NewInt(1))
	require.NoError(t, cache.ReleaseSession(ctx, "metadata-no-disk-policies"))
	estimate := cache.MetadataEstimate()

	srv := &Server{
		rootDir:                        t.TempDir(),
		engineCache:                    cache,
		localCacheGCEnabled:            true,
		dagqlCacheMaxEstimatedBytes:    estimate.EstimatedBytes - 1,
		dagqlCacheTargetEstimatedBytes: 1,
	}
	require.NoError(t, srv.gcLocked(t.Context(), localCacheGCScheduled))
	require.Zero(t, cache.EntryStats().RetainedCalls)
}

func TestScheduledGCTrimsImportedMetadataWithoutPersistenceReset(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "dagql-cache.db")
	cacheBeforeRestart, err := dagql.NewCache(t.Context(), dbPath, nil, nil)
	require.NoError(t, err)
	for i := range 4 {
		ctx := addGCTestPersistable(
			t,
			cacheBeforeRestart,
			"metadata-restart",
			fmt.Sprintf("metadataRestart%d", i),
			dagql.NewInt(i),
		)
		if i == 3 {
			require.NoError(t, cacheBeforeRestart.ReleaseSession(ctx, "metadata-restart"))
		}
	}
	beforeRestart := cacheBeforeRestart.MetadataEstimate()
	require.Equal(t, 4, cacheBeforeRestart.EntryStats().RetainedCalls)
	require.NoError(t, cacheBeforeRestart.Close(t.Context()))

	cacheAfterRestart, err := dagql.NewCache(t.Context(), dbPath, nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cacheAfterRestart.Close(context.Background())) })
	require.Equal(t, dagql.CachePersistenceResetNone, cacheAfterRestart.PersistenceResetReason())
	require.Equal(t, beforeRestart, cacheAfterRestart.MetadataEstimate())
	require.Equal(t, 4, cacheAfterRestart.EntryStats().RetainedCalls)

	// NewServer schedules this exact callback one second after startup. Calling
	// it directly keeps this composition test deterministic while covering the
	// imported current-schema graph and scheduled structural pass together.
	srv := &Server{
		rootDir:                        t.TempDir(),
		engineCache:                    cacheAfterRestart,
		localCacheGCEnabled:            true,
		dagqlCacheMaxEstimatedBytes:    beforeRestart.EstimatedBytes - 1,
		dagqlCacheTargetEstimatedBytes: 1,
	}
	srv.gc()
	require.Zero(t, cacheAfterRestart.EntryStats().RetainedCalls)
	require.Less(t, cacheAfterRestart.MetadataEstimate().EstimatedBytes, beforeRestart.EstimatedBytes)
}

func TestGCLockedDiskStatFailureDoesNotSuppressMetadataPrune(t *testing.T) {
	t.Parallel()

	cache := newGCTestCache(t)
	ctx := addGCTestPersistable(t, cache, "metadata-disk-stat-error", "metadataDiskStatError", dagql.NewInt(1))
	require.NoError(t, cache.ReleaseSession(ctx, "metadata-disk-stat-error"))
	estimate := cache.MetadataEstimate()

	srv := &Server{
		rootDir:                        "/path/that/does/not/exist/dagger-metadata-prune-test",
		engineCache:                    cache,
		workerGCPolicies:               []dagqlCachePrunePolicy{{All: true}},
		localCacheGCEnabled:            true,
		dagqlCacheMaxEstimatedBytes:    estimate.EstimatedBytes - 1,
		dagqlCacheTargetEstimatedBytes: 1,
	}
	err := srv.gcLocked(t.Context(), localCacheGCScheduled)
	require.ErrorContains(t, err, "get disk stats for gc")
	require.Zero(t, cache.EntryStats().RetainedCalls)
}

func TestLocalCachePressureCheckUsesMetadataWhenDiskStatFails(t *testing.T) {
	t.Parallel()

	cache := newGCTestCache(t)
	ctx := addGCTestPersistable(t, cache, "metadata-pressure-check", "metadataPressureCheck", dagql.NewInt(1))
	t.Cleanup(func() { require.NoError(t, cache.ReleaseSession(ctx, "metadata-pressure-check")) })
	estimate := cache.MetadataEstimate()
	srv := &Server{
		rootDir:                        "/path/that/does/not/exist/dagger-metadata-pressure-test",
		engineCache:                    cache,
		workerGCPolicies:               []dagqlCachePrunePolicy{{MinFreeSpace: 1}},
		localCacheGCEnabled:            true,
		dagqlCacheMaxEstimatedBytes:    estimate.EstimatedBytes - 1,
		dagqlCacheTargetEstimatedBytes: 1,
	}
	require.True(t, srv.localCachePressureGCNeeded(t.Context()))

	srv.metadataPruneMonitorBlocked.Store(true)
	require.False(t, srv.localCachePressureGCNeeded(t.Context()))
}

func TestMetadataPruneMonitorBlockOutcome(t *testing.T) {
	t.Parallel()

	srv := &Server{dagqlCacheMaxEstimatedBytes: 100}

	srv.updateMetadataPruneMonitorBlocked(localCacheGCMonitor, dagql.CacheMetadataPruneReport{
		Triggered:  true,
		AfterPrune: dagql.CacheMetadataEstimate{EstimatedBytes: 101},
	})
	require.True(t, srv.metadataPruneMonitorBlocked.Load())

	// Compact-only recovery ends at the maximum and clears rather than blocks.
	srv.updateMetadataPruneMonitorBlocked(localCacheGCMonitor, dagql.CacheMetadataPruneReport{
		Triggered:  true,
		AfterPrune: dagql.CacheMetadataEstimate{EstimatedBytes: 100},
	})
	require.False(t, srv.metadataPruneMonitorBlocked.Load())

	srv.metadataPruneMonitorBlocked.Store(true)
	srv.updateMetadataPruneMonitorBlocked(localCacheGCMonitor, dagql.CacheMetadataPruneReport{
		Triggered:                 true,
		RemovedPersistedRootCount: 1,
		AfterPrune:                dagql.CacheMetadataEstimate{EstimatedBytes: 101},
	})
	require.False(t, srv.metadataPruneMonitorBlocked.Load())

	// Non-monitor passes never introduce the monitor-only block.
	srv.updateMetadataPruneMonitorBlocked(localCacheGCScheduled, dagql.CacheMetadataPruneReport{
		Triggered:  true,
		AfterPrune: dagql.CacheMetadataEstimate{EstimatedBytes: 101},
	})
	require.False(t, srv.metadataPruneMonitorBlocked.Load())
}

func TestMonitorMetadataPruneBlocksAfterProtectedNoProgress(t *testing.T) {
	t.Parallel()

	cache := newGCTestCache(t)
	activeCtx := addGCTestPersistable(t, cache, "metadata-protected", "metadataProtected", dagql.NewInt(1))
	estimate := cache.MetadataEstimate()
	srv := &Server{
		rootDir:                        t.TempDir(),
		engineCache:                    cache,
		localCacheGCEnabled:            true,
		dagqlCacheMaxEstimatedBytes:    estimate.EstimatedBytes - 1,
		dagqlCacheTargetEstimatedBytes: 1,
	}

	require.NoError(t, srv.gcLocked(t.Context(), localCacheGCMonitor))
	require.True(t, srv.metadataPruneMonitorBlocked.Load())

	coldCtx := addGCTestPersistable(t, cache, "metadata-later-cold", "metadataLaterCold", dagql.NewInt(2))
	require.NoError(t, cache.ReleaseSession(coldCtx, "metadata-later-cold"))
	require.Greater(t, cache.MetadataEstimate().EstimatedBytes, srv.dagqlCacheMaxEstimatedBytes)
	require.NoError(t, srv.gcLocked(t.Context(), localCacheGCMonitor))
	require.Equal(t, 2, cache.EntryStats().RetainedCalls, "blocked monitor stage must be skipped despite an over-maximum cold root")

	require.NoError(t, cache.ReleaseSession(activeCtx, "metadata-protected"))
	require.NoError(t, srv.gcLocked(t.Context(), localCacheGCScheduled))
	require.Zero(t, cache.EntryStats().RetainedCalls)
}

func TestMetadataPruneMonitorBlockSuppressesOnlyStructuralStage(t *testing.T) {
	t.Parallel()

	cache := newGCTestCache(t)
	activeCtx := addGCTestPersistable(t, cache, "metadata-active", "metadataActive", dagql.NewInt(1))
	coldCtx := addGCTestPersistable(t, cache, "metadata-disk-prunable", "metadataDiskPrunable", gcTestSizedInt{
		Int:       dagql.NewInt(2),
		sizeBytes: 100,
		identity:  "metadata-disk-prunable",
	})
	require.NoError(t, cache.ReleaseSession(coldCtx, "metadata-disk-prunable"))

	srv := &Server{
		rootDir:                        t.TempDir(),
		engineCache:                    cache,
		workerGCPolicies:               []dagqlCachePrunePolicy{{All: true, MaxUsedSpace: 1}},
		localCacheGCEnabled:            true,
		dagqlCacheMaxEstimatedBytes:    2,
		dagqlCacheTargetEstimatedBytes: 1,
	}
	srv.metadataPruneMonitorBlocked.Store(true)
	require.NoError(t, srv.gcLocked(t.Context(), localCacheGCMonitor))
	// Disk pruning still removed its cold persisted root. The active metadata
	// root remains, and the over-maximum structural stage stayed blocked.
	require.Equal(t, 1, cache.EntryStats().RetainedCalls)
	require.True(t, srv.metadataPruneMonitorBlocked.Load())
	require.NoError(t, cache.ReleaseSession(activeCtx, "metadata-active"))
}

func TestMetadataPruneBlockBypassesLifecycleReasons(t *testing.T) {
	for _, reason := range []localCacheGCReason{localCacheGCScheduled, localCacheGCGracefulShutdown} {
		reason := reason
		t.Run(fmt.Sprintf("reason-%d", reason), func(t *testing.T) {
			cache := newGCTestCache(t)
			ctx := addGCTestPersistable(t, cache, "metadata-lifecycle", "metadataLifecycle", dagql.NewInt(1))
			require.NoError(t, cache.ReleaseSession(ctx, "metadata-lifecycle"))
			estimate := cache.MetadataEstimate()
			srv := &Server{
				rootDir:                        t.TempDir(),
				engineCache:                    cache,
				localCacheGCEnabled:            true,
				dagqlCacheMaxEstimatedBytes:    estimate.EstimatedBytes - 1,
				dagqlCacheTargetEstimatedBytes: 1,
			}
			srv.metadataPruneMonitorBlocked.Store(true)
			require.NoError(t, srv.gcLocked(t.Context(), reason))
			require.Zero(t, cache.EntryStats().RetainedCalls)
			require.False(t, srv.metadataPruneMonitorBlocked.Load())
		})
	}
}

func TestGCSessionCompletionClearsMetadataPruneBlock(t *testing.T) {
	t.Parallel()

	cache := newGCTestCache(t)
	ctx := addGCTestPersistable(t, cache, "metadata-session-complete", "metadataSessionComplete", dagql.NewInt(1))
	require.NoError(t, cache.ReleaseSession(ctx, "metadata-session-complete"))
	estimate := cache.MetadataEstimate()
	srv := &Server{
		rootDir:                        t.TempDir(),
		engineCache:                    cache,
		localCacheGCEnabled:            true,
		dagqlCacheMaxEstimatedBytes:    estimate.EstimatedBytes - 1,
		dagqlCacheTargetEstimatedBytes: 1,
	}
	srv.metadataPruneMonitorBlocked.Store(true)
	srv.gcAfterSessionCompletion()
	require.False(t, srv.metadataPruneMonitorBlocked.Load())
	require.Zero(t, cache.EntryStats().RetainedCalls)
}

func TestExplicitPruneClearsMetadataPruneBlock(t *testing.T) {
	t.Parallel()

	srv := &Server{rootDir: t.TempDir(), engineCache: newGCTestCache(t)}
	srv.metadataPruneMonitorBlocked.Store(true)
	_, err := srv.PruneEngineLocalCacheEntries(t.Context(), core.EngineCachePruneOptions{})
	require.NoError(t, err)
	require.False(t, srv.metadataPruneMonitorBlocked.Load())
}

func TestCanceledMonitorMetadataPruneDoesNotSetBlock(t *testing.T) {
	t.Parallel()

	cache := newGCTestCache(t)
	ctx := addGCTestPersistable(t, cache, "metadata-canceled", "metadataCanceled", dagql.NewInt(1))
	t.Cleanup(func() { require.NoError(t, cache.ReleaseSession(ctx, "metadata-canceled")) })
	estimate := cache.MetadataEstimate()
	srv := &Server{
		engineCache:                    cache,
		localCacheGCEnabled:            true,
		dagqlCacheMaxEstimatedBytes:    estimate.EstimatedBytes - 1,
		dagqlCacheTargetEstimatedBytes: 1,
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	err := srv.gcLocked(canceled, localCacheGCMonitor)
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, srv.metadataPruneMonitorBlocked.Load())
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

func TestGetDagqlGCPolicyDefaultPolicies(t *testing.T) {
	cfg := config.Config{
		GC: config.GCConfig{
			GCSpace: config.GCSpace{
				ReservedSpace: config.DiskSpace{Bytes: 100},
				MaxUsedSpace:  config.DiskSpace{Bytes: 1000},
				MinFreeSpace:  config.DiskSpace{Bytes: 200},
			},
		},
	}

	policies := getDagqlGCPolicy(cfg, bkconfig.GCConfig{}, t.TempDir())
	require.Equal(t, []dagqlCachePrunePolicy{
		{
			Filters:      []string{"type==source.local,type==exec.cachemount,type==source.git.checkout"},
			KeepDuration: 48 * time.Hour,
			MaxUsedSpace: 512 * 1e6,
		},
		{
			KeepDuration:  60 * 24 * time.Hour,
			ReservedSpace: 100,
			MaxUsedSpace:  1000,
			MinFreeSpace:  200,
		},
		{
			All:           true,
			ReservedSpace: 100,
			MaxUsedSpace:  1000,
			MinFreeSpace:  200,
		},
	}, policies)
}

func mustParseDiskSpace(t *testing.T, value string, dstat disk.DiskStat) int64 {
	t.Helper()
	var parsed bkconfig.DiskSpace
	require.NoError(t, parsed.UnmarshalText([]byte(value)))
	return parsed.AsBytes(dstat)
}
