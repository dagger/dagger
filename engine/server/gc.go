package server

import (
	"context"
	"fmt"
	"sync"

	"github.com/dagger/dagger/engine/config"
	bkclient "github.com/moby/buildkit/client"
	bkconfig "github.com/moby/buildkit/cmd/buildkitd/config"
	"github.com/moby/buildkit/util/bklog"
	"github.com/moby/buildkit/util/disk"
	"github.com/moby/buildkit/util/imageutil"
	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/core"
)

func (srv *Server) EngineLocalCachePolicy() bkclient.PruneInfo {
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

// Prune everything that is releasable in the local cache. No support for filtering yet.
func (srv *Server) PruneEngineLocalCacheEntries(ctx context.Context) (*core.EngineCacheEntrySet, error) {
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

	err := srv.baseWorker.Prune(ctx, ch, bkclient.PruneInfo{All: true})
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

	out := make([]bkclient.PruneInfo, 0, len(bkcfg.GCPolicy))
	for _, policy := range policies {
		out = append(out, bkclient.PruneInfo{
			Filter:        policy.Filters,
			All:           policy.All,
			KeepDuration:  policy.KeepDuration.Duration,
			ReservedSpace: policy.ReservedSpace.AsBytes(dstat),
			MaxUsedSpace:  policy.MaxUsedSpace.AsBytes(dstat),
			MinFreeSpace:  policy.MinFreeSpace.AsBytes(dstat),
		})
	}
	return out
}

func getDefaultGCPolicy(cfg config.Config, bkcfg bkconfig.GCConfig, root string) bkclient.PruneInfo {
	// the last policy is the default one
	policies := getGCPolicy(cfg, bkcfg, root)
	return policies[len(policies)-1]
}

func defaultGCPolicy(cfg config.Config, bkcfg bkconfig.GCConfig, dstat disk.DiskStat) []config.GCPolicy {
	space := cfg.GC.GCSpace
	if space.IsUnset() {
		space = convertBkSpaceFromConfig(bkcfg)
	}
	if space.IsUnset() {
		space = DetectDefaultGCCap(dstat)
	}

	return convertBkPolicies(bkconfig.DefaultGCPolicy(bkconfig.GCConfig{
		GCMinFreeSpace:  bkconfig.DiskSpace(space.MinFreeSpace),
		GCReservedSpace: bkconfig.DiskSpace(space.ReservedSpace),
		GCMaxUsedSpace:  bkconfig.DiskSpace(space.MaxUsedSpace),
	}, dstat))
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
