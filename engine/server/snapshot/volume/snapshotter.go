package volume

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/snapshots"
	"github.com/moby/buildkit/cache"
	bksnapshot "github.com/moby/buildkit/snapshot"
	"github.com/moby/buildkit/solver/pb"
)

const SnapshotterName = "dagger-volume"

// TODO: doc
type VolumeSnapshotter struct {
	rootDir string
}

func NewVolumeSnapshotter(ctx context.Context, rootDir string) (*VolumeSnapshotter, error) {
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

var _ cache.CtdVolumeSnapshotter = (*VolumeSnapshotter)(nil)

// TODO: implement snapshots.Cleaner interface?

func (sn *VolumeSnapshotter) Name() string {
	return SnapshotterName
}

func (sn *VolumeSnapshotter) Acquire(ctx context.Context, key string, sharingMode pb.CacheSharingOpt) (func() error, error) {
	snap, err := sn.getSnapshot(ctx, key)
	if err != nil {
		return nil, err
	}
	return snap.acquire(ctx, sharingMode)
}

func (sn *VolumeSnapshotter) Mounts(ctx context.Context, key string) ([]mount.Mount, error) {
	snap, err := sn.getSnapshot(ctx, key)
	if err != nil {
		return nil, err
	}
	return snap.mounts(ctx)
}

func (sn *VolumeSnapshotter) Prepare(ctx context.Context, key, parent string, opts ...snapshots.Opt) (_ []mount.Mount, rerr error) {
	// TODO: support parent
	if parent != "" {
		return nil, fmt.Errorf("parent snapshot is not supported")
	}

	snap, err := sn.getOrInitSnapshot(ctx, key, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to get or init snapshot: %w", err)
	}

	// TODO: mounts currently assumes you have acquired.. doesn't matter in buildkit usage but weird in general case
	return snap.mounts(ctx)
}

func (sn *VolumeSnapshotter) Usage(ctx context.Context, key string) (snapshots.Usage, error) {
	snap, err := sn.getSnapshot(ctx, key)
	if err != nil {
		return snapshots.Usage{}, err
	}
	return snap.usage(ctx)
}

func (sn *VolumeSnapshotter) Stat(ctx context.Context, key string) (snapshots.Info, error) {
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

func (sn *VolumeSnapshotter) Merge(ctx context.Context, key string, diffs []bksnapshot.Diff, opts ...snapshots.Opt) error {
	return fmt.Errorf("merge not supported")
}
