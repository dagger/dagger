package server

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/dagger/dagger/engine/config"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	bkconfig "github.com/dagger/dagger/internal/buildkit/cmd/buildkitd/config"
	"github.com/dagger/dagger/internal/buildkit/util/bklog"
	"github.com/dagger/dagger/internal/buildkit/util/disk"
	"github.com/dagger/dagger/internal/buildkit/util/imageutil"
	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/core"
)

type dagqlCachePrunePolicy struct {
	All           bool
	Filters       []string
	KeepDuration  time.Duration
	ReservedSpace int64
	MaxUsedSpace  int64
	MinFreeSpace  int64
	TargetSpace   int64
}

func (srv *Server) EngineLocalCachePolicy() *bkclient.PruneInfo {
	return srv.workerDefaultGCPolicy
}

// Return all the cache entries in the local cache. No support for filtering yet.
func (srv *Server) EngineLocalCacheEntries(ctx context.Context) (*core.EngineCacheEntrySet, error) {
	du, err := srv.baseWorker.DiskUsage(ctx, bkclient.DiskUsageInfo{})
	if err != nil {
		return nil, fmt.Errorf("failed to get disk usage from worker: %w", err)
	}

	set := &core.EngineCacheEntrySet{}
	for _, r := range du {
		cacheEnt := &core.EngineCacheEntry{
			Description:         r.Description,
			DiskSpaceBytes:      int(r.Size),
			ActivelyUsed:        r.InUse,
			CreatedTimeUnixNano: int(r.CreatedAt.UnixNano()),
			RecordType:          string(r.RecordType),
		}
		if r.LastUsedAt != nil {
			cacheEnt.MostRecentUseTimeUnixNano = int(r.LastUsedAt.UnixNano())
		}
		set.EntriesList = append(set.EntriesList, cacheEnt)
		set.DiskSpaceBytes += int(r.Size)
	}
	set.EntryCount = len(set.EntriesList)

	return set, nil
}

// Prune the local cache of releaseable entries. If UseDefaultPolicy is true,
// use the engine-wide default pruning policy, otherwise prune the whole cache
// of any releasable entries.
func (srv *Server) PruneEngineLocalCacheEntries(ctx context.Context, opts core.EngineCachePruneOptions) (*core.EngineCacheEntrySet, error) {
	srv.gcmu.Lock()
	defer srv.gcmu.Unlock()

	srv.daggerSessionsMu.RLock()
	cancelLeases := len(srv.daggerSessions) == 0
	srv.daggerSessionsMu.RUnlock()
	if cancelLeases {
		imageutil.CancelCacheLeases()
	}

	wg := &sync.WaitGroup{}
	ch := make(chan bkclient.UsageInfo, 32)
	var pruned []bkclient.UsageInfo
	wg.Add(1)
	go func() {
		defer wg.Done()
		for r := range ch {
			pruned = append(pruned, r)
		}
	}()

	dstat, _ := disk.GetDiskStat(srv.rootDir)
	prunePolicies, err := resolveEngineLocalCachePrunePolicies(
		dagqlCachePrunePoliciesFromBuildkit(srv.baseWorker.GCPolicy()),
		opts,
		dstat,
	)
	if err != nil {
		return nil, err
	}
	err = srv.baseWorker.Prune(ctx, ch, buildkitPruneInfosFromDagqlPolicies(prunePolicies)...)
	if err != nil {
		return nil, fmt.Errorf("worker failed to prune local cache: %w", err)
	}
	close(ch)
	wg.Wait()

	if len(pruned) == 0 {
		return &core.EngineCacheEntrySet{}, nil
	}

	if e, ok := srv.SolverCache.(interface {
		ReleaseUnreferenced(context.Context) error
	}); ok {
		if err := e.ReleaseUnreferenced(ctx); err != nil {
			bklog.G(ctx).Errorf("failed to release cache metadata: %+v", err)
		}
	}

	set := &core.EngineCacheEntrySet{}
	for _, r := range pruned {
		// buildkit's Prune doesn't set RecordType currently, so can't include kind here
		ent := &core.EngineCacheEntry{
			Description:         r.Description,
			DiskSpaceBytes:      int(r.Size),
			CreatedTimeUnixNano: int(r.CreatedAt.UnixNano()),
			ActivelyUsed:        r.InUse,
		}
		if r.LastUsedAt != nil {
			ent.MostRecentUseTimeUnixNano = int(r.LastUsedAt.UnixNano())
		}
		set.EntriesList = append(set.EntriesList, ent)
		set.DiskSpaceBytes += int(r.Size)
	}
	set.EntryCount = len(set.EntriesList)

	return set, nil
}

func trimmedPruneOpts(opts core.EngineCachePruneOptions) (maxUsedSpace, reservedSpace, minFreeSpace, targetSpace string) {
	return strings.TrimSpace(opts.MaxUsedSpace),
		strings.TrimSpace(opts.ReservedSpace),
		strings.TrimSpace(opts.MinFreeSpace),
		strings.TrimSpace(opts.TargetSpace)
}

func dagqlCachePrunePoliciesFromBuildkit(pruneOpts []bkclient.PruneInfo) []dagqlCachePrunePolicy {
	policies := make([]dagqlCachePrunePolicy, 0, len(pruneOpts))
	for _, policy := range pruneOpts {
		policies = append(policies, dagqlCachePrunePolicy{
			All:           policy.All,
			Filters:       slices.Clone(policy.Filter),
			KeepDuration:  policy.KeepDuration,
			ReservedSpace: policy.ReservedSpace,
			MaxUsedSpace:  policy.MaxUsedSpace,
			MinFreeSpace:  policy.MinFreeSpace,
			TargetSpace:   policy.TargetSpace,
		})
	}
	return policies
}

func buildkitPruneInfosFromDagqlPolicies(policies []dagqlCachePrunePolicy) []bkclient.PruneInfo {
	pruneOpts := make([]bkclient.PruneInfo, 0, len(policies))
	for _, policy := range policies {
		pruneOpts = append(pruneOpts, bkclient.PruneInfo{
			All:           policy.All,
			Filter:        slices.Clone(policy.Filters),
			KeepDuration:  policy.KeepDuration,
			ReservedSpace: policy.ReservedSpace,
			MaxUsedSpace:  policy.MaxUsedSpace,
			MinFreeSpace:  policy.MinFreeSpace,
			TargetSpace:   policy.TargetSpace,
		})
	}
	return pruneOpts
}

func cloneDagqlCachePrunePolicies(in []dagqlCachePrunePolicy) []dagqlCachePrunePolicy {
	out := make([]dagqlCachePrunePolicy, 0, len(in))
	for _, policy := range in {
		policy.Filters = slices.Clone(policy.Filters)
		out = append(out, policy)
	}
	return out
}

func resolveEngineLocalCachePrunePolicies(defaultPolicy []dagqlCachePrunePolicy, opts core.EngineCachePruneOptions, dstat disk.DiskStat) ([]dagqlCachePrunePolicy, error) {
	prunePolicies := []dagqlCachePrunePolicy{{All: true}}
	if opts.UseDefaultPolicy && len(defaultPolicy) > 0 {
		// Copy to avoid mutating the default policy if per-call overrides are set.
		prunePolicies = cloneDagqlCachePrunePolicies(defaultPolicy)
	}

	maxUsedSpace, reservedSpace, minFreeSpace, targetSpace := trimmedPruneOpts(opts)
	if maxUsedSpace != "" || reservedSpace != "" || minFreeSpace != "" || targetSpace != "" {
		if err := applyEngineCachePruneSpaceOverrides(prunePolicies, dstat, maxUsedSpace, reservedSpace, minFreeSpace, targetSpace); err != nil {
			return nil, err
		}
	}
	return prunePolicies, nil
}

func applyEngineCachePruneSpaceOverrides(prunePolicies []dagqlCachePrunePolicy, dstat disk.DiskStat, maxUsedSpace, reservedSpace, minFreeSpace, targetSpace string) error {
	var (
		maxUsedSpaceBytes  int64
		hasMaxUsedSpace    bool
		reservedSpaceBytes int64
		hasReservedSpace   bool
		minFreeSpaceBytes  int64
		hasMinFreeSpace    bool
		targetSpaceBytes   int64
		hasTargetSpace     bool
		err                error
	)

	if maxUsedSpace != "" {
		maxUsedSpaceBytes, err = parseEngineCacheDiskSpace("maxUsedSpace", maxUsedSpace, dstat)
		if err != nil {
			return err
		}
		hasMaxUsedSpace = true
	}
	if reservedSpace != "" {
		reservedSpaceBytes, err = parseEngineCacheDiskSpace("reservedSpace", reservedSpace, dstat)
		if err != nil {
			return err
		}
		hasReservedSpace = true
	}
	if minFreeSpace != "" {
		minFreeSpaceBytes, err = parseEngineCacheDiskSpace("minFreeSpace", minFreeSpace, dstat)
		if err != nil {
			return err
		}
		hasMinFreeSpace = true
	}
	if targetSpace != "" {
		targetSpaceBytes, err = parseEngineCacheDiskSpace("targetSpace", targetSpace, dstat)
		if err != nil {
			return err
		}
		hasTargetSpace = true
	}

	for i := range prunePolicies {
		if hasMaxUsedSpace {
			prunePolicies[i].MaxUsedSpace = maxUsedSpaceBytes
		}
		if hasReservedSpace {
			prunePolicies[i].ReservedSpace = reservedSpaceBytes
		}
		if hasMinFreeSpace {
			prunePolicies[i].MinFreeSpace = minFreeSpaceBytes
		}
		if hasTargetSpace {
			prunePolicies[i].TargetSpace = targetSpaceBytes
		}
	}

	return nil
}

func parseEngineCacheDiskSpace(argName, argValue string, dstat disk.DiskStat) (int64, error) {
	var diskSpace bkconfig.DiskSpace
	if err := diskSpace.UnmarshalText([]byte(argValue)); err != nil {
		return 0, fmt.Errorf("invalid %s value %q: %w", argName, argValue, err)
	}
	return diskSpace.AsBytes(dstat), nil
}

func (srv *Server) gc() {
	srv.gcmu.Lock()
	defer srv.gcmu.Unlock()

	ch := make(chan bkclient.UsageInfo)
	eg, ctx := errgroup.WithContext(context.TODO())

	var size int64
	eg.Go(func() error {
		for ui := range ch {
			size += ui.Size
		}
		return nil
	})

	eg.Go(func() error {
		defer close(ch)
		if policy := srv.baseWorker.GCPolicy(); len(policy) > 0 {
			return srv.baseWorker.Prune(ctx, ch, policy...)
		}
		return nil
	})

	err := eg.Wait()
	if err != nil {
		bklog.G(ctx).Errorf("gc error: %+v", err)
	}
	if size > 0 {
		bklog.G(ctx).Debugf("gc cleaned up %d bytes", size)
		go srv.throttledReleaseUnreferenced()
	}
}

func getGCPolicy(cfg config.Config, bkcfg bkconfig.GCConfig, root string) []bkclient.PruneInfo {
	return buildkitPruneInfosFromDagqlPolicies(getDagqlGCPolicy(cfg, bkcfg, root))
}

func getDagqlGCPolicy(cfg config.Config, bkcfg bkconfig.GCConfig, root string) []dagqlCachePrunePolicy {
	if cfg.GC.Enabled != nil && !*cfg.GC.Enabled {
		return nil
	}
	if bkcfg.GC != nil && !*bkcfg.GC {
		return nil
	}

	dstat, _ := disk.GetDiskStat(root)

	policies := cfg.GC.Policies
	if len(policies) == 0 {
		policies = convertBkPolicies(bkcfg.GCPolicy)
	}
	if len(policies) == 0 {
		policies = defaultGCPolicy(cfg, bkcfg, dstat)
	}

	out := make([]dagqlCachePrunePolicy, 0, len(policies))
	for _, policy := range policies {
		info := dagqlCachePrunePolicy{
			Filters:       slices.Clone(policy.Filters),
			All:           policy.All,
			KeepDuration:  policy.KeepDuration.Duration,
			ReservedSpace: policy.ReservedSpace.AsBytes(dstat),
			MaxUsedSpace:  policy.MaxUsedSpace.AsBytes(dstat),
			MinFreeSpace:  policy.MinFreeSpace.AsBytes(dstat),
		}
		if policy.SweepSize != (config.DiskSpace{}) {
			info.TargetSpace = info.MaxUsedSpace - policy.SweepSize.AsBytes(disk.DiskStat{Total: info.MaxUsedSpace - info.ReservedSpace})
			if info.TargetSpace <= 0 { // 0 is a special value indicating to ignore this value
				info.TargetSpace = 1
			}
		}
		out = append(out, info)
	}
	return out
}

func getDefaultGCPolicy(cfg config.Config, bkcfg bkconfig.GCConfig, root string) *bkclient.PruneInfo {
	policy := getDefaultDagqlGCPolicy(cfg, bkcfg, root)
	if policy == nil {
		return nil
	}
	out := buildkitPruneInfosFromDagqlPolicies([]dagqlCachePrunePolicy{*policy})
	return &out[0]
}

func getDefaultDagqlGCPolicy(cfg config.Config, bkcfg bkconfig.GCConfig, root string) *dagqlCachePrunePolicy {
	// the last policy is the default one
	policies := getDagqlGCPolicy(cfg, bkcfg, root)
	if len(policies) == 0 {
		return nil
	}
	policy := policies[len(policies)-1]
	return &policy
}

func defaultGCPolicy(cfg config.Config, bkcfg bkconfig.GCConfig, dstat disk.DiskStat) []config.GCPolicy {
	space := cfg.GC.GCSpace
	if space.IsUnset() {
		space = convertBkSpaceFromConfig(bkcfg)
	}
	if space.IsUnset() {
		space = DetectDefaultGCCap(dstat)
	}

	policies := convertBkPolicies(bkconfig.DefaultGCPolicy(bkconfig.GCConfig{
		GCMinFreeSpace:  bkconfig.DiskSpace(space.MinFreeSpace),
		GCReservedSpace: bkconfig.DiskSpace(space.ReservedSpace),
		GCMaxUsedSpace:  bkconfig.DiskSpace(space.MaxUsedSpace),
	}, dstat))
	for i, policy := range policies {
		policy.SweepSize = space.SweepSize
		policies[i] = policy
	}
	return policies
}

func DetectDefaultGCCap(dstat disk.DiskStat) config.GCSpace {
	reserve := config.DiskSpace{Percentage: diskSpaceReservePercentage}
	if reserve.AsBytes(dstat) > diskSpaceReserveBytes {
		reserve = config.DiskSpace{Bytes: diskSpaceReserveBytes}
	}
	return config.GCSpace{
		ReservedSpace: reserve,
		MinFreeSpace:  config.DiskSpace{Percentage: diskSpaceFreePercentage},
		MaxUsedSpace:  config.DiskSpace{Percentage: diskSpaceMaxPercentage},
		// SweepSize is unset by default, to preserve backwards compat
		// SweepSize: config.DiskSpace{},
	}
}

func convertBkPolicies(bkpolicies []bkconfig.GCPolicy) (policies []config.GCPolicy) {
	for _, policy := range bkpolicies {
		policies = append(policies, config.GCPolicy{
			All:          policy.All,
			Filters:      policy.Filters,
			KeepDuration: config.Duration(policy.KeepDuration),
			GCSpace:      convertBkSpaceFromPolicy(policy),
		})
	}
	return policies
}

func convertBkSpaceFromPolicy(policy bkconfig.GCPolicy) config.GCSpace {
	space := config.GCSpace{
		ReservedSpace: config.DiskSpace(policy.ReservedSpace),
		MaxUsedSpace:  config.DiskSpace(policy.MaxUsedSpace),
		MinFreeSpace:  config.DiskSpace(policy.MinFreeSpace),
	}
	//nolint:staticcheck
	if space.ReservedSpace == (config.DiskSpace{}) && policy.KeepBytes != (bkconfig.DiskSpace{}) {
		space.ReservedSpace = config.DiskSpace(policy.KeepBytes)
	}
	return space
}

func convertBkSpaceFromConfig(cfg bkconfig.GCConfig) config.GCSpace {
	space := config.GCSpace{
		ReservedSpace: config.DiskSpace(cfg.GCReservedSpace),
		MaxUsedSpace:  config.DiskSpace(cfg.GCMaxUsedSpace),
		MinFreeSpace:  config.DiskSpace(cfg.GCMinFreeSpace),
	}
	//nolint:staticcheck
	if space.ReservedSpace == (config.DiskSpace{}) && cfg.GCKeepStorage != (bkconfig.DiskSpace{}) {
		space.ReservedSpace = config.DiskSpace(cfg.GCKeepStorage)
	}
	return space
}

const (
	diskSpaceReservePercentage int64 = 10
	diskSpaceReserveBytes      int64 = 10 * 1e9 // 10GB
	diskSpaceFreePercentage    int64 = 20
	diskSpaceMaxPercentage     int64 = 75
)
