package volume

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/snapshots"
	"github.com/containerd/containerd/snapshots/storage"
	"github.com/containerd/continuity/fs"
	"github.com/containerd/errdefs"
	"github.com/gofrs/flock"
	"github.com/moby/buildkit/cache"
	"github.com/moby/buildkit/solver/pb"
)

// TODO: snapshots can leak if engine exits and never comes back; need to support manual cleanup for these cases

const (
	SnapshotterName = "daggervolume"

	flockRetryInterval = 100 * time.Millisecond
)

func New(ctx context.Context, metaStore *storage.MetaStore, rootDir string) (cache.CtdVolumeSnapshotter, error) {
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

	sn := &snapshotter{
		rootDir:   rootDir,
		metaStore: metaStore,
		snapshots: make(map[string]*snapshot),
	}

	err := metaStore.WithTransaction(ctx, true, func(ctx context.Context) error {
		missingKeys := map[string]struct{}{}
		tree := &snapshotTree{vtxs: make(map[string]*snapshotVtx)}
		if err := storage.WalkInfo(ctx, func(ctx context.Context, info snapshots.Info) error {
			tree.add(info.Name, info.Parent)
			id := denamespaceKey(info.Name)
			snap, err := sn.loadSnapshot(ctx, id)
			if err != nil {
				return fmt.Errorf("failed to load snapshot %s: %w", id, err)
			}

			invalidStateErr := fmt.Errorf("snapshot %s expected %s but got %s", id, info.Kind, snap.curState)
			switch info.Kind {
			case snapshots.KindActive:
				switch snap.curState {
				case notExists:
					// was deleted while we were gone
					missingKeys[info.Name] = struct{}{}
					return nil
				case active:
					return nil
				case committed:
					return invalidStateErr
				case removing:
					// is being deleted currently
					missingKeys[info.Name] = struct{}{}
					return nil
				default:
					return fmt.Errorf("unhandled snapshot state: %v", snap.curState)
				}

			case snapshots.KindCommitted:
				switch snap.curState {
				case notExists:
					// was deleted while we were gone
					missingKeys[info.Name] = struct{}{}
					return nil
				case active:
					return invalidStateErr
				case committed:
					return nil
				case removing:
					// is being deleted currently
					missingKeys[info.Name] = struct{}{}
					return nil
				default:
					return fmt.Errorf("unhandled snapshot state: %v", snap.curState)
				}

			default:
				return fmt.Errorf("unhandled snapshot kind: %v", info.Kind)
			}
		}); err != nil {
			return fmt.Errorf("failed to walk snapshots: %w", err)
		}

		deleted := map[string]struct{}{}
		for missingKey := range missingKeys {
			removeKeys := tree.keysUnder(missingKey)
			for i := len(removeKeys) - 1; i >= 0; i-- {
				removeKey := removeKeys[i]
				_, ok := deleted[removeKey]
				if ok {
					continue
				}
				_, _, err := storage.Remove(ctx, removeKey)
				if err != nil && !errors.Is(err, errdefs.ErrNotFound) {
					return fmt.Errorf("failed to remove missing snapshot %s: %w", removeKey, err)
				}
			}
		}

		return nil
	})
	if err != nil {
		// tolerate ErrNotFound, which happens when the db hasn't been initialized yet
		if !errors.Is(err, errdefs.ErrNotFound) {
			return nil, fmt.Errorf("failed to walk snapshots: %w", err)
		}
	}

	return sn, nil
}

// TODO: explain this mild wtf situation
func FromMetaDB(db *metadata.DB, sn cache.CtdVolumeSnapshotter) cache.CtdVolumeSnapshotter {
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

type snapshotter struct {
	rootDir   string
	metaStore *storage.MetaStore

	snapshots map[string]*snapshot
	mu        sync.RWMutex
}

func (sn *snapshotter) Name() string {
	return SnapshotterName
}

func (sn *snapshotter) Acquire(ctx context.Context, key string, sharingMode pb.CacheSharingOpt) (func() error, error) {
	id := denamespaceKey(key)

	sharingLock := flock.New(sn.snapshotSharingLockPath(id))
	var ok bool
	var err error
	switch sharingMode {
	case pb.CacheSharingOpt_SHARED:
		ok, err = sharingLock.TryRLockContext(ctx, flockRetryInterval)
	case pb.CacheSharingOpt_LOCKED:
		ok, err = sharingLock.TryLockContext(ctx, flockRetryInterval)
	case pb.CacheSharingOpt_PRIVATE:
		// TODO: handle differently than locked
		ok, err = sharingLock.TryLockContext(ctx, flockRetryInterval)
	}

	switch {
	case ok:
		// we got it, return a closer that releases it
		return sharingLock.Close, nil
	case errors.Is(err, os.ErrNotExist):
		return nil, fmt.Errorf("snapshot %s does not exist: %w", id, errdefs.ErrNotFound)
	default:
		return nil, fmt.Errorf("failed to acquire sharing lock: %w", err)
	}
}

// TODO: here and elsewhere, check for dir escape
func (sn *snapshotter) Prepare(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	err := sn.metaStore.WithTransaction(ctx, true, func(ctx context.Context) error {
		_, err := storage.CreateSnapshot(ctx, snapshots.KindActive, key, parent, opts...)
		if err != nil {
			return fmt.Errorf("failed to create snapshot metadata: %w", err)
		}

		if err := sn.toActive(ctx, denamespaceKey(key), denamespaceKey(parent)); err != nil {
			return fmt.Errorf("failed to create snapshot: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return sn.mounts(ctx, denamespaceKey(key))
}

func (sn *snapshotter) mounts(ctx context.Context, id string) ([]mount.Mount, error) {
	return []mount.Mount{{
		Type:    "bind",
		Source:  sn.snapshotContentsDirPath(id),
		Options: []string{"bind"},
	}}, nil
}

func (sn *snapshotter) Commit(ctx context.Context, name, key string, opts ...snapshots.Opt) error {
	return sn.metaStore.WithTransaction(ctx, true, func(ctx context.Context) error {
		_, _, _, err := storage.GetInfo(ctx, key)
		if err != nil {
			return err
		}

		oldID := denamespaceKey(key)
		newID := denamespaceKey(name)
		if err := sn.toCommitted(ctx, oldID, newID); err != nil {
			return err
		}

		usage, err := fs.DiskUsage(ctx, sn.snapshotContentsDirPath(newID))
		if err != nil {
			return err
		}

		if _, err = storage.CommitActive(ctx, key, name, snapshots.Usage(usage), opts...); err != nil {
			return fmt.Errorf("failed to commit snapshot: %w", err)
		}
		return nil
	})
}

func (sn *snapshotter) Remove(ctx context.Context, key string) (err error) {
	return sn.metaStore.WithTransaction(ctx, true, func(ctx context.Context) error {
		_, _, err := storage.Remove(ctx, key)
		if err != nil {
			return fmt.Errorf("failed to remove metadata: %w", err)
		}

		if err := sn.toRemoved(ctx, denamespaceKey(key)); err != nil {
			return err
		}
		return nil
	})
}

func (sn *snapshotter) View(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	return nil, fmt.Errorf("View not implemented")
}

func (sn *snapshotter) Mounts(ctx context.Context, key string) (_ []mount.Mount, err error) {
	return sn.mounts(ctx, denamespaceKey(key))
}

func (sn *snapshotter) Usage(ctx context.Context, key string) (usage snapshots.Usage, err error) {
	var info snapshots.Info

	err = sn.metaStore.WithTransaction(ctx, false, func(ctx context.Context) error {
		_, info, usage, err = storage.GetInfo(ctx, key)
		return err
	})
	if err != nil {
		return snapshots.Usage{}, err
	}

	if info.Kind == snapshots.KindActive {
		du, err := fs.DiskUsage(ctx, sn.snapshotContentsDirPath(denamespaceKey(key)))
		if err != nil {
			return snapshots.Usage{}, err
		}
		usage = snapshots.Usage(du)
	}

	return usage, nil
}

func (sn *snapshotter) Stat(ctx context.Context, key string) (info snapshots.Info, err error) {
	err = sn.metaStore.WithTransaction(ctx, false, func(ctx context.Context) error {
		_, info, _, err = storage.GetInfo(ctx, key)
		return err
	})
	if err != nil {
		return snapshots.Info{}, err
	}

	return info, nil
}

func (sn *snapshotter) Update(ctx context.Context, info snapshots.Info, fieldpaths ...string) (_ snapshots.Info, err error) {
	err = sn.metaStore.WithTransaction(ctx, true, func(ctx context.Context) error {
		info, err = storage.UpdateInfo(ctx, info, fieldpaths...)
		return err
	})
	if err != nil {
		return snapshots.Info{}, err
	}

	return info, nil
}

func (sn *snapshotter) Walk(ctx context.Context, fn snapshots.WalkFunc, fs ...string) error {
	return sn.metaStore.WithTransaction(ctx, false, func(ctx context.Context) error {
		return storage.WalkInfo(ctx, fn, fs...)
	})
}

func (sn *snapshotter) Close() error {
	// TODO: ? could close every open flock, but this should only get called on shutdown so they
	// are getting closed soon anyways?
	return nil
}

// TODO: explain
// TODO: could be more robust too
func denamespaceKey(key string) string {
	if key == "" {
		return key
	}

	// we get keys from containerd in the form of "<namespace>/<incrementing int>/<actual key>", return <actual key>
	split := strings.Split(key, "/")
	return split[len(split)-1]
}
