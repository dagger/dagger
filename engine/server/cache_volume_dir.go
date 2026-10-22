package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/cachevolumedir"
	"github.com/dagger/dagger/engine/config"
	"github.com/dagger/dagger/engine/slog"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/internal/buildkit/util/disk"
)

// initCacheVolumeDir opens the cache volume directory when one is configured
// (cacheVolumes.dir), so that cache volumes are directories there instead of
// snapshots. If another engine owns the directory, this engine keeps cache
// volumes as snapshots for this run.
func (srv *Server) initCacheVolumeDir(cfg config.CacheVolumesConfig) error {
	srv.cacheVolumesCfg = cfg
	core.SetCacheVolumeDir(nil)
	if cfg.Dir == "" {
		return nil
	}
	dir, err := srv.validateCacheVolumeDir(cfg.Dir)
	if err != nil {
		return err
	}
	store, err := cachevolumedir.Open(dir, cachevolumedir.Options{EngineVersion: engine.Version})
	if errors.Is(err, cachevolumedir.ErrOwned) {
		slog.Warn("cache volume directory is in use by another engine; cache volumes are snapshots for this run", "dir", dir)
		return nil
	}
	if err != nil {
		return fmt.Errorf("open cache volume directory: %w", err)
	}
	srv.cacheVolumeDir = store
	core.SetCacheVolumeDir(store)
	slog.Info("cache volumes are directories", "dir", dir)
	logStateFilesystem(dir)
	return nil
}

// validateCacheVolumeDir checks the configured directory and returns it
// cleaned, with symlinks resolved when it exists.
func (srv *Server) validateCacheVolumeDir(dir string) (string, error) {
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("cacheVolumes.dir %q must be an absolute path", dir)
	}
	dir = filepath.Clean(dir)
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("resolve cacheVolumes.dir %q: %w", dir, err)
	}
	if rel, err := filepath.Rel(srv.workerRootDir, dir); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("cacheVolumes.dir %q must not be inside the engine's worker state %q, which a local cache reset removes", dir, srv.workerRootDir)
	}
	return dir, nil
}

// cacheVolumeDirPolicy is the directory's collection policy: its own space
// settings, defaulting from its own filesystem, and its keep duration. all
// removes every volume that isn't in use.
func (srv *Server) cacheVolumeDirPolicy(all bool) (cachevolumedir.Policy, error) {
	dstat, err := disk.GetDiskStat(srv.cacheVolumeDir.Dir())
	if err != nil {
		return cachevolumedir.Policy{}, fmt.Errorf("get cache volume directory disk stats: %w", err)
	}
	space := srv.cacheVolumesCfg.GCSpace
	if space.IsUnset() {
		space = DetectDefaultGCCap(dstat)
	}
	policy := cachevolumedir.Policy{
		ReservedSpace:    space.ReservedSpace.AsBytes(dstat),
		MaxUsedSpace:     space.MaxUsedSpace.AsBytes(dstat),
		MinFreeSpace:     space.MinFreeSpace.AsBytes(dstat),
		CurrentFreeSpace: dstat.Available,
		KeepDuration:     srv.cacheVolumesCfg.KeepDuration.Duration,
		All:              all,
	}
	if space.SweepSize != (config.DiskSpace{}) {
		// As for the engine's other policies: sweep from the maximum toward
		// the reserved floor by SweepSize.
		policy.TargetSpace = policy.MaxUsedSpace - space.SweepSize.AsBytes(disk.DiskStat{Total: policy.MaxUsedSpace - policy.ReservedSpace})
		if policy.TargetSpace <= 0 {
			policy.TargetSpace = 1
		}
	}
	return policy, nil
}

// collectCacheVolumeDir runs the directory's stage of a collection.
func (srv *Server) collectCacheVolumeDir(ctx context.Context, all bool) (cachevolumedir.Report, error) {
	if srv.cacheVolumeDir == nil {
		return cachevolumedir.Report{}, nil
	}
	policy, err := srv.cacheVolumeDirPolicy(all)
	if err != nil {
		return cachevolumedir.Report{}, err
	}
	report, err := srv.cacheVolumeDir.Collect(ctx, policy)
	if len(report.Removed) > 0 {
		slog.Info("removed cache volumes from the cache volume directory", "count", len(report.Removed), "bytes", report.ReclaimedBytes)
	}
	if err != nil {
		return report, fmt.Errorf("collect cache volume directory: %w", err)
	}
	return report, nil
}

// collectCacheVolumeDirAtShutdown collects the directory and waits for the
// removals to finish, so that what the next engine receives is what was
// accounted. Both stop when ctx ends, leaving the directory consistent.
func (srv *Server) collectCacheVolumeDirAtShutdown(ctx context.Context) error {
	if srv.cacheVolumeDir == nil {
		return nil
	}
	_, err := srv.collectCacheVolumeDir(ctx, false)
	if waitErr := srv.cacheVolumeDir.WaitRemovals(ctx); waitErr != nil {
		err = errors.Join(err, fmt.Errorf("wait for cache volume removals: %w", waitErr))
	}
	return err
}

// closeCacheVolumeDir gives up the directory. Removals still running finish
// in the background; the next owner removes whatever they leave.
func (srv *Server) closeCacheVolumeDir() error {
	if srv.cacheVolumeDir == nil {
		return nil
	}
	core.SetCacheVolumeDir(nil)
	err := srv.cacheVolumeDir.Close()
	srv.cacheVolumeDir = nil
	return err
}

// cacheVolumeDirEntries lists the directory's volumes as local cache entries.
func (srv *Server) cacheVolumeDirEntries(ctx context.Context) ([]*core.EngineCacheEntry, error) {
	if srv.cacheVolumeDir == nil {
		return nil, nil
	}
	entries, _, err := srv.cacheVolumeDir.Usage(ctx)
	if err != nil {
		return nil, fmt.Errorf("list cache volume directory: %w", err)
	}
	out := make([]*core.EngineCacheEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, cacheVolumeDirEntry(e))
	}
	return out, nil
}

func cacheVolumeDirEntry(e cachevolumedir.Entry) *core.EngineCacheEntry {
	desc := fmt.Sprintf("cache volume %q (directory)", e.Identity.Key)
	if e.Format != cachevolumedir.FormatVersion {
		desc = fmt.Sprintf("cache volume %s (directory, format %d)", e.ID, e.Format)
	}
	return &core.EngineCacheEntry{
		Description:               desc,
		DiskSpaceBytes:            int(e.SizeBytes),
		CreatedTimeUnixNano:       unixNanoOrZero(e.CreatedAt),
		MostRecentUseTimeUnixNano: unixNanoOrZero(e.LastUsedAt),
		ActivelyUsed:              e.Pinned,
		RecordType:                string(bkclient.UsageRecordTypeCacheMount),
		RecordTypes:               []string{string(bkclient.UsageRecordTypeCacheMount)},
	}
}

func unixNanoOrZero(t time.Time) int {
	if t.IsZero() {
		return 0
	}
	return int(t.UnixNano())
}
