package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/internal/buildkit/util/disk"
)

// Discarded local cache state (the worker root dir: snapshots, content store,
// cache mounts, ...) can be enormous — tens of GB across millions of inodes —
// so deleting it inline at startup both blocks the engine from serving and
// saturates the disk queue with unlink/journal traffic for as long as the
// delete takes. Instead the invalid state is renamed aside to a sibling
// "worker-trash-<unique>" directory (an O(1) rename; always the same
// filesystem since the trash dir is a sibling), and a background sweeper
// removes the tree with a bounded duty cycle so the filesystem journal can
// drain between bursts. Leftover trash dirs from an interrupted sweep are
// picked up on the next startup.
const (
	workerTrashPrefix = "worker-trash-"

	// trashSweepWorkSlice/trashSweepRestSlice define the sweeper's duty
	// cycle: unlink for up to workSlice, then idle for restSlice. The ~20%
	// duty cycle trades a longer total sweep for leaving disk bandwidth to
	// the rest of the system (a saturating delete storm is what makes
	// desktop audio/video stutter during cache resets).
	trashSweepWorkSlice = 50 * time.Millisecond
	trashSweepRestSlice = 200 * time.Millisecond

	// trashSweepBatch is how many directory entries are read per scan pass
	// while walking a trash tree, bounding memory on huge directories.
	trashSweepBatch = 256
)

// moveLocalCacheStateToTrash renames dir aside to a sibling trash directory
// for background removal, returning the trash path. A missing dir is a no-op
// returning "".
func moveLocalCacheStateToTrash(dir string) (string, error) {
	if _, err := os.Lstat(dir); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	parent := filepath.Dir(dir)
	for attempt := range 3 {
		trashDir := filepath.Join(parent, workerTrashPrefix+strconv.FormatInt(time.Now().UnixNano(), 10))
		err := os.Rename(dir, trashDir)
		if err == nil {
			return trashDir, nil
		}
		// A same-nanosecond collision with an existing trash dir is all but
		// impossible, but cheap to retry under a fresh name.
		collision := errors.Is(err, fs.ErrExist) || errors.Is(err, unix.ENOTEMPTY)
		if attempt == 2 || !collision {
			return "", err
		}
	}
	return "", fmt.Errorf("move %s to trash: retries exhausted", dir)
}

// findLocalCacheTrashDirs lists the trash directories under rootDir: state
// discarded by this startup plus any leftovers from a previous engine whose
// sweep never finished.
func findLocalCacheTrashDirs(rootDir string) ([]string, error) {
	entries, err := os.ReadDir(rootDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var trashDirs []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), workerTrashPrefix) {
			trashDirs = append(trashDirs, filepath.Join(rootDir, entry.Name()))
		}
	}
	return trashDirs, nil
}

// startLocalCacheTrashSweeper scans for trash directories and, if any exist,
// starts sweeping them in the background. Called once at startup, after the
// local cache state (and any reset of it) is settled.
func (srv *Server) startLocalCacheTrashSweeper() {
	trashDirs, err := findLocalCacheTrashDirs(srv.rootDir)
	if err != nil {
		slog.Warn("failed to scan for discarded local cache state", "err", err)
		return
	}
	if len(trashDirs) == 0 {
		return
	}
	go srv.sweepLocalCacheTrash(srv.shutdownCtx, trashDirs)
}

func (srv *Server) sweepLocalCacheTrash(ctx context.Context, trashDirs []string) {
	pacer := &trashPacer{
		workSlice: trashSweepWorkSlice,
		restSlice: trashSweepRestSlice,
		fullSpeed: srv.trashSweepFullSpeed,
	}
	for _, trashDir := range trashDirs {
		start := time.Now()
		slog.Info("removing discarded local cache state in background", "dir", trashDir)
		// An uncleanly stopped engine can leave mounts behind (snapshotter
		// overlays, cache mount binds) in the state it discarded; detach them
		// first so the walk below removes the trash tree itself rather than
		// descending into mounted filesystems. Mount paths follow the rename,
		// so they show up under the trash dir. Best-effort: anything missed
		// is caught by the EBUSY fallback in removeOnePaced.
		if err := mount.UnmountRecursive(trashDir, unix.MNT_DETACH); err != nil {
			slog.Warn("failed to unmount leftover mounts under discarded local cache state", "dir", trashDir, "err", err)
		}
		err := removeAllPaced(ctx, trashDir, pacer)
		switch {
		case err == nil:
			slog.Info("removed discarded local cache state", "dir", trashDir, "duration", time.Since(start).Round(time.Millisecond))
		case errors.Is(err, context.Canceled):
			slog.Info("interrupted removing discarded local cache state; resuming on next startup", "dir", trashDir)
			return
		default:
			slog.Warn("failed to remove discarded local cache state; retrying on next startup", "dir", trashDir, "err", err)
		}
	}
}

// trashSweepFullSpeed reports whether the sweeper should skip its rest slices:
// once the disk is under the same free-space pressure that triggers cache GC,
// reclaiming the trashed bytes promptly matters more than smoothing I/O.
func (srv *Server) trashSweepFullSpeed() bool {
	if len(srv.workerGCPolicies) == 0 {
		return false
	}
	dstat, err := disk.GetDiskStat(srv.rootDir)
	if err != nil {
		return false
	}
	return localCacheDiskPressureGCNeeded(srv.workerGCPolicies, dstat)
}

// trashPacer bounds the sweeper's duty cycle: removals proceed for up to
// workSlice, then pause for restSlice, unless fullSpeed says the rest should
// be skipped. A zero workSlice disables pacing entirely.
type trashPacer struct {
	workSlice time.Duration
	restSlice time.Duration
	fullSpeed func() bool

	sliceEnd time.Time
}

// pace is called before each removal; it returns promptly while the current
// work slice lasts and sleeps for restSlice once it is exhausted.
func (p *trashPacer) pace(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.workSlice <= 0 {
		return nil
	}
	now := time.Now()
	if p.sliceEnd.IsZero() {
		p.sliceEnd = now.Add(p.workSlice)
		return nil
	}
	if now.Before(p.sliceEnd) {
		return nil
	}
	if p.fullSpeed != nil && p.fullSpeed() {
		p.sliceEnd = now.Add(p.workSlice)
		return nil
	}
	timer := time.NewTimer(p.restSlice)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	p.sliceEnd = time.Now().Add(p.workSlice)
	return nil
}

// removeAllPaced removes path and everything below it like os.RemoveAll, but
// paced by pacer, and resilient to leftover mountpoints: an EBUSY removal
// attempts a lazy unmount first, since an uncleanly stopped engine can leave
// mounts behind in the state it discarded. It stops at the first hard error;
// the sweep is retried on the next startup.
func removeAllPaced(ctx context.Context, path string, pacer *trashPacer) error {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if !info.IsDir() {
		return removeOnePaced(ctx, path, pacer)
	}
	for {
		names, err := readDirnamesBatch(path, trashSweepBatch)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if len(names) == 0 {
			break
		}
		for _, name := range names {
			if err := removeAllPaced(ctx, filepath.Join(path, name), pacer); err != nil {
				return err
			}
		}
		if len(names) < trashSweepBatch {
			// The batch covered every remaining entry and all were removed,
			// so the directory is empty now.
			break
		}
	}
	return removeOnePaced(ctx, path, pacer)
}

func removeOnePaced(ctx context.Context, path string, pacer *trashPacer) error {
	if err := pacer.pace(ctx); err != nil {
		return err
	}
	err := os.Remove(path)
	if err == nil || errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if errors.Is(err, unix.EBUSY) {
		// Likely a mountpoint left behind by an unclean shutdown; detach it
		// and retry. MNT_DETACH succeeds even while the mount is in use.
		if unmountErr := unix.Unmount(path, unix.MNT_DETACH); unmountErr == nil {
			err = os.Remove(path)
			if err == nil || errors.Is(err, fs.ErrNotExist) {
				return nil
			}
		}
	}
	return err
}

// readDirnamesBatch returns up to n names from dir. Reopening the directory
// per batch resets the read position, which keeps the walk correct while
// entries are being unlinked between batches.
func readDirnamesBatch(dir string, n int) ([]string, error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	names, err := f.Readdirnames(n)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return names, nil
}
