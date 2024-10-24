package server

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/cmd/buildkitd/config"
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
			dt, _ := json.Marshal(policy)
			bklog.G(ctx).Debugf("applying gc policy %s", string(dt))
			stat, _ := disk.GetDiskStat(srv.rootDir)
			dt, _ = json.Marshal(stat)
			bklog.G(ctx).Debugf("disk stat %s", string(dt))
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
	}
}

func getGCPolicy(cfg config.GCConfig, root string) []bkclient.PruneInfo {
	if cfg.GC != nil && !*cfg.GC {
		return nil
	}
	dstat, err := disk.GetDiskStat(root)
	if err != nil {
		panic(err)
	}
	if len(cfg.GCPolicy) == 0 {
		cfg.GCPolicy = defaultGCPolicy(cfg, dstat)
	}
	out := make([]bkclient.PruneInfo, 0, len(cfg.GCPolicy))
	for _, rule := range cfg.GCPolicy {
		//nolint:staticcheck
		if rule.ReservedSpace == (config.DiskSpace{}) && rule.KeepBytes != (config.DiskSpace{}) {
			rule.ReservedSpace = rule.KeepBytes
		}
		out = append(out, bkclient.PruneInfo{
			Filter:        rule.Filters,
			All:           rule.All,
			KeepDuration:  rule.KeepDuration.Duration,
			ReservedSpace: rule.ReservedSpace.AsBytes(dstat),
			MaxUsedSpace:  rule.MaxUsedSpace.AsBytes(dstat),
			MinFreeSpace:  rule.MinFreeSpace.AsBytes(dstat),
		})
	}
	return out
}

func getDefaultGCPolicy(cfg config.GCConfig, root string) bkclient.PruneInfo {
	// the last policy is the default one
	policies := getGCPolicy(cfg, root)
	return policies[len(policies)-1]
}

func defaultGCPolicy(cfg config.GCConfig, dstat disk.DiskStat) []config.GCPolicy {
	if cfg.IsUnset() {
		cfg.GCReservedSpace = cfg.GCKeepStorage //nolint: staticcheck // backwards-compat
	}
	if cfg.IsUnset() {
		// use our own default caps, so we can configure the limits ourselves
		cfg = DetectDefaultGCCap(dstat)
	}
	return config.DefaultGCPolicy(cfg, dstat)
}

func DetectDefaultGCCap(dstat disk.DiskStat) config.GCConfig {
	reserve := config.DiskSpace{Percentage: diskSpaceReservePercentage}
	if reserve.AsBytes(dstat) > diskSpaceReserveBytes {
		reserve = config.DiskSpace{Bytes: diskSpaceReserveBytes}
	}
	max := config.DiskSpace{Percentage: diskSpaceMaxPercentage}
	if max.AsBytes(dstat) > diskSpaceMaxBytes {
		max = config.DiskSpace{Bytes: diskSpaceMaxBytes}
	}
	return config.GCConfig{
		GCReservedSpace: reserve,
		GCMinFreeSpace:  config.DiskSpace{Percentage: diskSpaceFreePercentage},
		GCMaxUsedSpace:  max,
	}
}

const (
	diskSpaceReservePercentage int64 = 10
	diskSpaceReserveBytes      int64 = 10 * 1e9 // 10GB
	diskSpaceFreePercentage    int64 = 20
	diskSpaceMaxPercentage     int64 = 80
	diskSpaceMaxBytes          int64 = 200 * 1e9 // 200GB
)
