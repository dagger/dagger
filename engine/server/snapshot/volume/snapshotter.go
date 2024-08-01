package volume

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/snapshots"
	"github.com/moby/buildkit/cache"
	"github.com/moby/buildkit/solver/pb"
)

const SnapshotterName = "daggervolume"

func NewVolumeSnapshotter(ctx context.Context, rootDir string) (cache.CtdVolumeSnapshotter, error) {
	// TODO: use consts/funcs, fix perms
	if err := os.MkdirAll(rootDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create root dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(rootDir, "stage"), 0755); err != nil {
		return nil, fmt.Errorf("failed to create stage dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(rootDir, "rm"), 0755); err != nil {
		return nil, fmt.Errorf("failed to create rm dir: %w", err)
	}

	return &VolumeSnapshotter{rootDir: rootDir}, nil
}

// TODO: explain this mild wtf situation
func VolumeSnapshotterFromMetaDB(db *metadata.DB, sn cache.CtdVolumeSnapshotter) cache.CtdVolumeSnapshotter {
	return adapter{
		Snapshotter: db.Snapshotter(sn.Name()),
		base:        sn,
	}
}

type adapter struct {
	snapshots.Snapshotter
	base cache.CtdVolumeSnapshotter
}

var _ cache.CtdVolumeSnapshotter = (*adapter)(nil)

func (a adapter) Acquire(ctx context.Context, key string, sharingMode pb.CacheSharingOpt) (func() error, error) {
	return a.base.Acquire(ctx, key, sharingMode)
}

func (a adapter) Name() string {
	return a.base.Name()
}

// TODO: doc
type VolumeSnapshotter struct {
	rootDir string
}

var _ cache.CtdVolumeSnapshotter = (*VolumeSnapshotter)(nil)

// TODO: implement snapshots.Cleaner interface?

func (sn *VolumeSnapshotter) Name() string {
	return SnapshotterName
}

func (sn *VolumeSnapshotter) Acquire(ctx context.Context, key string, sharingMode pb.CacheSharingOpt) (func() error, error) {
	key = denamespaceKey(key)
	snap, err := sn.getSnapshot(ctx, key)
	if err != nil {
		return nil, err
	}
	return snap.acquire(ctx, sharingMode)
}

func (sn *VolumeSnapshotter) Mounts(ctx context.Context, key string) ([]mount.Mount, error) {
	key = denamespaceKey(key)
	snap, err := sn.getSnapshot(ctx, key)
	if err != nil {
		return nil, err
	}
	return snap.mounts(ctx)
}

func (sn *VolumeSnapshotter) Prepare(ctx context.Context, key, parent string, opts ...snapshots.Opt) (_ []mount.Mount, rerr error) {
	key = denamespaceKey(key)
	// TODO: support parent
	if parent != "" {
		return nil, fmt.Errorf("parent snapshot is not supported")
	}

	snap, err := sn.getOrInitSnapshot(ctx, key, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to get or init snapshot: %w", err)
	}

	// TODO: mounts currently assumes you have acquired.. doesn't matter in buildkit usage but weird in general case
	// TODO: could set readonly on these out of caution? Or plumb sharing mode through opts?
	return snap.mounts(ctx)
}

func (sn *VolumeSnapshotter) Usage(ctx context.Context, key string) (snapshots.Usage, error) {
	key = denamespaceKey(key)
	snap, err := sn.getSnapshot(ctx, key)
	if err != nil {
		return snapshots.Usage{}, err
	}
	return snap.usage(ctx)
}

func (sn *VolumeSnapshotter) Stat(ctx context.Context, key string) (snapshots.Info, error) {
	key = denamespaceKey(key)
	snap, err := sn.getSnapshot(ctx, key)
	if err != nil {
		return snapshots.Info{}, err
	}
	return snap.info(ctx)
}

func (sn *VolumeSnapshotter) Update(ctx context.Context, info snapshots.Info, fieldpaths ...string) (snapshots.Info, error) {
	snap, err := sn.getSnapshot(ctx, info.Name)
	if err != nil {
		return snapshots.Info{}, err
	}
	return snap.updateInfo(ctx, info, fieldpaths...)
}

// TODO: remember to use errdefs.ErrFailedPrecondition if you can't remove it, to play nice with containerd gc.
// same for other errdefs
func (sn *VolumeSnapshotter) Remove(ctx context.Context, key string) error {
	key = denamespaceKey(key)
	snap, err := sn.getSnapshot(ctx, key)
	if err != nil {
		return err
	}
	return snap.tryRemove(ctx)
}

func (sn *VolumeSnapshotter) Walk(ctx context.Context, fn snapshots.WalkFunc, filters ...string) error {
	// TODO: support filters

	curSnaps, err := sn.currentSnapshots(ctx)
	if err != nil {
		return fmt.Errorf("failed to get current snapshots: %w", err)
	}
	for _, snap := range curSnaps {
		info, err := snap.info(ctx)
		if err != nil {
			return fmt.Errorf("failed to get snapshot info: %w", err)
		}
		if err := fn(ctx, info); err != nil {
			return err
		}
	}

	return nil
}

func (sn *VolumeSnapshotter) Close() error {
	// TODO:??
	return nil
}

func (sn *VolumeSnapshotter) View(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	return nil, fmt.Errorf("view not supported")
}

func (sn *VolumeSnapshotter) Commit(ctx context.Context, name, key string, opts ...snapshots.Opt) error {
	return fmt.Errorf("commit not supported")
}

// TODO: explain
// TODO: could be more robust too
func denamespaceKey(key string) string {
	// we get keys from containerd in the form of "<namespace>/<incrementing int>/<actual key>", return <actual key>
	split := strings.Split(key, "/")
	return split[len(split)-1]
}
