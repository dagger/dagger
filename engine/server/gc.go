package server

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/config"
	"github.com/dagger/dagger/engine/slog"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	bkconfig "github.com/dagger/dagger/internal/buildkit/cmd/buildkitd/config"
	"github.com/dagger/dagger/internal/buildkit/util/disk"

	"github.com/dagger/dagger/core"
)

type dagqlCachePrunePolicy = dagql.CachePrunePolicy

func (srv *Server) EngineLocalCachePolicy() *core.EngineCachePolicy {
	if srv.workerDefaultGCPolicy == nil {
		return nil
	}
	return &core.EngineCachePolicy{
		All:           srv.workerDefaultGCPolicy.All,
		Filters:       slices.Clone(srv.workerDefaultGCPolicy.Filters),
		KeepDuration:  srv.workerDefaultGCPolicy.KeepDuration,
		ReservedSpace: srv.workerDefaultGCPolicy.ReservedSpace,
		MaxUsedSpace:  srv.workerDefaultGCPolicy.MaxUsedSpace,
		MinFreeSpace:  srv.workerDefaultGCPolicy.MinFreeSpace,
		TargetSpace:   srv.workerDefaultGCPolicy.TargetSpace,
	}
}

// Return all the cache entries in the local cache. No support for filtering yet.
func (srv *Server) EngineLocalCacheEntries(ctx context.Context) (*core.EngineCacheEntrySet, error) {
	if srv.baseDagqlCache == nil {
		return &core.EngineCacheEntrySet{}, nil
	}
	entries := srv.baseDagqlCache.UsageEntries(ctx)
	return engineCacheEntrySetFromDagqlEntries(entries), nil
}

// Prune the local cache of releaseable entries. If UseDefaultPolicy is true,
// use the engine-wide default pruning policy, otherwise prune the whole cache
// of any releasable entries.
func (srv *Server) PruneEngineLocalCacheEntries(ctx context.Context, opts core.EngineCachePruneOptions) (*core.EngineCacheEntrySet, error) {
	srv.gcmu.Lock()
	defer srv.gcmu.Unlock()
	if srv.baseDagqlCache == nil {
		return &core.EngineCacheEntrySet{}, nil
	}

	dstat, _ := disk.GetDiskStat(srv.rootDir)
	var defaultPolicies []dagqlCachePrunePolicy
	if srv.baseWorker != nil {
		defaultPolicies = dagqlCachePrunePoliciesFromBuildkit(srv.baseWorker.GCPolicy())
	}
	prunePolicies, err := resolveEngineLocalCachePrunePolicies(
		defaultPolicies,
		opts,
		dstat,
	)
	if err != nil {
		return nil, err
	}

	pruned, err := srv.baseDagqlCache.Prune(ctx, dagql.CachePruneOpts{
		Policies:       prunePolicies,
		FreeSpaceBytes: dstat.Available,
	})
	if err != nil {
		return nil, fmt.Errorf("dagql prune failed: %w", err)
	}

	set := &core.EngineCacheEntrySet{
		EntriesList: make([]*core.EngineCacheEntry, 0, len(pruned.PrunedEntries)),
	}
	for _, entry := range pruned.PrunedEntries {
		coreEntry := engineCacheEntryFromDagqlEntry(entry)
		set.EntriesList = append(set.EntriesList, coreEntry)
		set.DiskSpaceBytes += coreEntry.DiskSpaceBytes
	}
	set.EntryCount = len(set.EntriesList)
	return set, nil
}

func engineCacheEntrySetFromDagqlEntries(entries []dagql.CacheUsageEntry) *core.EngineCacheEntrySet {
	set := &core.EngineCacheEntrySet{
		EntriesList: make([]*core.EngineCacheEntry, 0, len(entries)),
	}
	for _, entry := range entries {
		coreEntry := engineCacheEntryFromDagqlEntry(entry)
		set.EntriesList = append(set.EntriesList, coreEntry)
		set.DiskSpaceBytes += coreEntry.DiskSpaceBytes
	}
	set.EntryCount = len(set.EntriesList)
	return set
}

func engineCacheEntryFromDagqlEntry(entry dagql.CacheUsageEntry) *core.EngineCacheEntry {
	return &core.EngineCacheEntry{
		Description:               entry.Description,
		DiskSpaceBytes:            int(entry.SizeBytes),
		CreatedTimeUnixNano:       int(entry.CreatedTimeUnixNano),
		MostRecentUseTimeUnixNano: int(entry.MostRecentUseTimeUnixNano),
		ActivelyUsed:              entry.ActivelyUsed,
		RecordType:                entry.RecordType,
	}
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
	if srv.baseDagqlCache == nil {
		return
	}

	if srv.baseWorker == nil {
		return
	}
	policies := dagqlCachePrunePoliciesFromBuildkit(srv.baseWorker.GCPolicy())
	if len(policies) == 0 {
		return
	}
	dstat, _ := disk.GetDiskStat(srv.rootDir)
	pruned, err := srv.baseDagqlCache.Prune(context.TODO(), dagql.CachePruneOpts{
		Policies:       policies,
		FreeSpaceBytes: dstat.Available,
	})
	if err != nil {
		slog.Error("dagql cache gc error", "error", err)
		return
	}
	if pruned.PrunedBytes > 0 {
		slog.Debug("dagql cache gc cleaned up entries", "bytes", pruned.PrunedBytes, "count", len(pruned.PrunedEntries))
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
