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
	srv.cacheVolumeDir = nil
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
// canonical: cleaned, with the symlinks of its longest existing prefix
// resolved, since the directory itself may not exist yet.
func (srv *Server) validateCacheVolumeDir(dir string) (string, error) {
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("cacheVolumes.dir %q must be an absolute path", dir)
	}
	dir, err := resolveExistingPrefix(filepath.Clean(dir))
	if err != nil {
		return "", fmt.Errorf("resolve cacheVolumes.dir: %w", err)
	}
	worker, err := resolveExistingPrefix(srv.workerRootDir)
	if err != nil {
		return "", fmt.Errorf("resolve engine worker state: %w", err)
	}
	if rel, err := filepath.Rel(worker, dir); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("cacheVolumes.dir %q must not be inside the engine's worker state %q, which a local cache reset removes", dir, worker)
	}
	return dir, nil
}

// resolveExistingPrefix resolves the symlinks of path's longest existing
// prefix, and appends the rest unchanged.
func resolveExistingPrefix(path string) (string, error) {
	rest := ""
	for cur := path; ; {
		resolved, err := filepath.EvalSymlinks(cur)
		if err == nil {
			return filepath.Join(resolved, rest), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return path, nil
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

// cacheVolumeDirMaxUsedPercentage is the share of cacheVolumes.capacityBytes
// the directory's volumes may use.
const cacheVolumeDirMaxUsedPercentage = 80

// cacheVolumeDirPolicy is the directory's collection policy, from its
// filesystem's current state. all removes every volume that isn't in use.
func (srv *Server) cacheVolumeDirPolicy(all bool) (cachevolumedir.Policy, error) {
	getDiskStat := disk.GetDiskStat
	if srv.cacheVolumeDirDiskStat != nil {
		getDiskStat = srv.cacheVolumeDirDiskStat
	}
	dstat, err := getDiskStat(srv.cacheVolumeDir.Dir())
	if err != nil {
		return cachevolumedir.Policy{}, fmt.Errorf("get cache volume directory disk stats: %w", err)
	}
	return newCacheVolumeDirPolicy(srv.cacheVolumesCfg.CapacityBytes, dstat, all), nil
}

// newCacheVolumeDirPolicy keeps the directory's volumes within 80% of
// capacityBytes when it's set, and otherwise within the engine's default
// limits for the directory's own filesystem.
func newCacheVolumeDirPolicy(capacityBytes int64, dstat disk.DiskStat, all bool) cachevolumedir.Policy {
	policy := cachevolumedir.Policy{
		CurrentFreeSpace: dstat.Available,
		All:              all,
	}
	if capacityBytes > 0 {
		policy.MaxUsedSpace = capacityBytes * cacheVolumeDirMaxUsedPercentage / 100
		return policy
	}
	space := DetectDefaultGCCap(dstat)
	policy.ReservedSpace = space.ReservedSpace.AsBytes(dstat)
	policy.MaxUsedSpace = space.MaxUsedSpace.AsBytes(dstat)
	policy.MinFreeSpace = space.MinFreeSpace.AsBytes(dstat)
	return policy
}

// collectCacheVolumeDir runs the directory's stage of a collection.
func (srv *Server) collectCacheVolumeDir(ctx context.Context, all bool) (cachevolumedir.Report, error) {
	if srv.cacheVolumeDir == nil || srv.cacheVolumeDir.Closed() {
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
// in the background; the next owner removes whatever they leave. The store
// stays in srv.cacheVolumeDir, closed: the field is only written at startup,
// so the GC monitor, which reads it outside gcmu, never races a write.
func (srv *Server) closeCacheVolumeDir() error {
	if srv.cacheVolumeDir == nil {
		return nil
	}
	core.SetCacheVolumeDir(nil)
	err := srv.cacheVolumeDir.Close()
	if errors.Is(err, cachevolumedir.ErrPinned) {
		// Operations that outlived session teardown still mount volumes:
		// keep the directory until the process exits, so no other engine
		// collects them meanwhile.
		slog.Warn("cache volume directory still in use at stop; keeping it until the engine exits", "error", err)
		return nil
	}
	return err
}

// cacheVolumeDirPressure reports whether the directory's filesystem is below
// the directory's minimum free space, as the monitor checks the engine's
// state disk.
func (srv *Server) cacheVolumeDirPressure(ctx context.Context) bool {
	if srv.cacheVolumeDir == nil || !srv.localCacheGCEnabled || srv.cacheVolumeDir.Closed() {
		return false
	}
	policy, err := srv.cacheVolumeDirPolicy(false)
	if err != nil {
		slog.WarnContext(ctx, "cache volume directory pressure check skipped", "error", err)
		return false
	}
	return policy.MinFreeSpace > 0 && policy.CurrentFreeSpace < policy.MinFreeSpace
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
	return &core.EngineCacheEntry{
		Description:               fmt.Sprintf("cache volume %q (directory)", e.Identity.Key),
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
