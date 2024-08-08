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
	"github.com/moby/buildkit/identity"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/util/bklog"
	"golang.org/x/sys/unix"
)

/* TODO: snapshots can leak if:
* - engine A and engine B both are using snapshot
* - engine A deletes the snapshot, but engine B is still using it so storage remains
* - engine B dies ungracefully (crash, sigkill, etc.)
* - engine B never starts again

Users would have to manually clean those up right now.
Ideally want to setup a way of identifying such snapshots and auto cleaning them. Could just be
a file that tracks each engine usage of snapshot with some (long) "heartbeat" interval and rm
orphaned snapshots after some long period of time.

TODO: even simpler variation on above would be re-using an existing cache volume dir on new engine's
without the local state that mentions the existence of the old snapshots and never references them.
^ Should we just tell containerd about all the snapshots that exist when we start? Could skip creating leases on them (or add leases with some timeout). Coudl do this on the buildkit ref level too during startup
*/

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
		rootDir:     rootDir,
		metaStore:   metaStore,
		stateFlocks: make(map[string]*flock.Flock),
	}

	err := metaStore.WithTransaction(ctx, false, func(ctx context.Context) error {
		return storage.WalkInfo(ctx, func(ctx context.Context, info snapshots.Info) error {
			id := denamespaceKey(info.Name)
			err := sn.toCreated(ctx, id, unknown)
			if err != nil {
				return fmt.Errorf("failed to transition snapshot %s to created: %w", id, err)
			}
			return nil
		})
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

	// id -> shared flock for retaining the snapshot state
	// TODO: doc more, needs to be in memory etc.
	stateFlocks map[string]*flock.Flock
	mu          sync.RWMutex
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

func (sn *snapshotter) Prepare(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	// TODO: here and elsewhere, check for dir escape

	id := denamespaceKey(key)

	err := sn.metaStore.WithTransaction(ctx, true, func(ctx context.Context) error {
		_, err := storage.CreateSnapshot(ctx, snapshots.KindActive, key, parent, opts...)
		if err != nil {
			return fmt.Errorf("failed to create snapshot metadata: %w", err)
		}

		// TODO: handle parent

		err = sn.toCreated(ctx, id, unknown)
		if err != nil {
			return fmt.Errorf("failed to transition snapshot %s to created: %w", id, err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return sn.mounts(ctx, id)
}

func (sn *snapshotter) Mounts(ctx context.Context, key string) (_ []mount.Mount, err error) {
	err = sn.metaStore.WithTransaction(ctx, false, func(ctx context.Context) error {
		_, err = storage.GetSnapshot(ctx, key)
		if err != nil {
			return fmt.Errorf("failed to get snapshot mount: %w", err)
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

func (sn *snapshotter) Remove(ctx context.Context, key string) (err error) {
	return sn.metaStore.WithTransaction(ctx, true, func(ctx context.Context) error {
		_, _, err := storage.Remove(ctx, key)
		if err != nil {
			return fmt.Errorf("failed to remove metadata: %w", err)
		}

		id := denamespaceKey(key)
		err = sn.toRemoved(ctx, id, created)
		if err != nil {
			return fmt.Errorf("failed to transition snapshot %s to removed: %w", id, err)
		}

		return nil
	})
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

func (sn *snapshotter) Commit(ctx context.Context, name, key string, opts ...snapshots.Opt) error {
	return fmt.Errorf("commit not supported")
}

func (sn *snapshotter) View(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	return nil, fmt.Errorf("view not supported")
}

type state string

const (
	// TODO: comments on each one
	unknown state = ""
	created state = "created"
	removed state = "removed"
)

type InvalidStateTransitionError struct {
	From state
	To   state
}

func (e InvalidStateTransitionError) Error() string {
	return fmt.Sprintf("invalid state transition from %s to %s", e.From, e.To)
}

// TODO: from state isn't really useful after all, can simplify for now
func (sn *snapshotter) toCreated(ctx context.Context, id string, from state) (rerr error) {
	switch from {
	case unknown:
		stateLock := flock.New(sn.snapshotStateLockPath(id))
		ok, err := stateLock.TryRLock()
		switch {
		case ok:
			// we got the shared state lock, snapshot already created
			sn.mu.Lock()
			defer sn.mu.Unlock()
			_, alreadyExists := sn.stateFlocks[id]
			if alreadyExists {
				// we already have a shared state lock, can just discard the one opened above
				stateLock.Close()
			} else {
				sn.stateFlocks[id] = stateLock
			}
			return nil

		case err == nil:
			// we failed to get the lock even though it exists, someone else is deleting it.
			// try again keeping state unknown
			// TODO: theoretically could infinite loop in case of bug, add some protection?
			// TODO: also might be worth a sleep or use of TryRLockContext w/ a timeout?
			return sn.toCreated(ctx, id, unknown)

		case errors.Is(err, os.ErrNotExist):
			// snapshot doesn't exist, see if we can create it
			tmpID := identity.NewID()

			stageRootDirPath := sn.snapshotStageRootDirPath(id, tmpID)
			if err := os.Mkdir(stageRootDirPath, 0755); err != nil {
				return fmt.Errorf("failed to create stage dir: %w", err)
			}
			defer os.RemoveAll(stageRootDirPath) // cleanup in all cases if rename doesn't work or we return before it

			if err := os.WriteFile(sn.snapshotStageStateLockPath(id, tmpID), []byte{}, 0644); err != nil {
				return fmt.Errorf("failed to create stage state lock: %w", err)
			}
			if err := os.WriteFile(sn.snapshotStageSharingLockPath(id, tmpID), []byte{}, 0644); err != nil {
				return fmt.Errorf("failed to create stage sharing lock: %w", err)
			}
			if err := os.Mkdir(sn.snapshotStageContentsDirPath(id, tmpID), 0755); err != nil {
				return fmt.Errorf("failed to create stage contents dir: %w", err)
			}

			err := os.Rename(stageRootDirPath, sn.snapshotRootDirPath(id))
			switch {
			case err == nil:
				// success
			case errors.Is(err, os.ErrExist) || errors.Is(err, unix.ENOTEMPTY):
				// someone beat us to creating it
			default:
				// something else went wrong
				return fmt.Errorf("failed to rename stage dir: %w", err)
			}

			// someone else theoretically could being racing with us and delete it, so
			// keep state unknown and try again
			// TODO: theoretically could infinite loop in case of bug, add some protection?
			return sn.toCreated(ctx, id, unknown)

		default:
			// some other unhandled error occurred
			return fmt.Errorf("failed to rlock state lock: %w", err)
		}

	case created:
		return nil

	default:
		return InvalidStateTransitionError{From: from, To: created}
	}
}

func (sn *snapshotter) toRemoved(ctx context.Context, id string, from state) (rerr error) {
	switch from {
	case unknown:
		// TODO: double check this now
		// invalid because we should never be trying to remove a snapshot we don't
		// even claim to know the existence of
		return InvalidStateTransitionError{From: from, To: removed}

	case created:
		// release our shared state lock
		sn.mu.Lock()
		stateLock, ok := sn.stateFlocks[id]
		if ok {
			stateLock.Close()
			delete(sn.stateFlocks, id)
		} else {
			// TODO: is this an expected case ever? Probably?
		}
		sn.mu.Unlock()

		// now try to get an exclusive state lock, if we get it we can remove
		// the underlying snapshot storage
		stateLock = flock.New(sn.snapshotStateLockPath(id))
		ok, err := stateLock.TryLock()
		switch {
		case ok:
			// we got the exclusive state lock, can remove the storage
			defer stateLock.Close()
			err := os.Rename(sn.snapshotRootDirPath(id), sn.snapshotRmDirPath(id))
			switch {
			case err == nil:
				// successfully renamed, can remove the final dir now
				if err := os.RemoveAll(sn.snapshotRmDirPath(id)); err != nil {
					bklog.G(ctx).Errorf("failed to remove snapshot dir after rename: %v", err)
				}
				return nil

			case errors.Is(err, os.ErrNotExist):
				// rootDir didn't exist, so someone beat us to removing it
				return nil

			case errors.Is(err, os.ErrExist):
				// the rmDir already exists, so someone beat us to removing it
				return nil

			default:
				// something else went wrong, just log it rather than trying to rollback state
				// TODO: is rolling back easy? just do that if so? nice to avoid weird leaks
				// TODO: might just need to reaquire shared lock and ensure snapshotter state
				// knows it's no deleted?
				bklog.G(ctx).Errorf("failed to rename snapshot dir: %v", err)
				return nil
			}

		case err == nil:
			// lock exists but we couldn't get it, someone else is using it so we can't
			// delete the data but consider it removed in terms of our metadata
			bklog.G(ctx).Debugf("snapshot %s is in use by another process, leaving storage behind", id)
			return nil

		case errors.Is(err, os.ErrNotExist):
			// someone else beat us to removing it between when we released the shared state lock and
			// got the exclusive state lock
			return nil

		default:
			// something else went wrong
			// TODO: same comment as above about whether to rollback or not
			bklog.G(ctx).Errorf("failed to get exclusive state lock: %v", err)
			return nil
		}

	case removed:
		return nil

	default:
		return InvalidStateTransitionError{From: from, To: removed}
	}
}

// TODO: consolidate

func (sn *snapshotter) stageDirPath() string {
	return filepath.Join(sn.rootDir, "stage")
}

func (sn *snapshotter) rmDirPath() string {
	return filepath.Join(sn.rootDir, "rm")
}

func (sn *snapshotter) snapshotRootDirPath(id string) string {
	return filepath.Join(sn.rootDir, id)
}

func (sn *snapshotter) snapshotStateLockPath(id string) string {
	return filepath.Join(sn.snapshotRootDirPath(id), "state.lock")
}

func (sn *snapshotter) snapshotSharingLockPath(id string) string {
	return filepath.Join(sn.snapshotRootDirPath(id), "sharing.lock")
}

func (sn *snapshotter) snapshotContentsDirPath(id string) string {
	return filepath.Join(sn.snapshotRootDirPath(id), "contents")
}

func (sn *snapshotter) snapshotStageRootDirPath(id, tmpID string) string {
	return filepath.Join(sn.stageDirPath(), id+"-"+tmpID)
}

func (sn *snapshotter) snapshotStageStateLockPath(id, tmpID string) string {
	return filepath.Join(sn.snapshotStageRootDirPath(id, tmpID), "state.lock")
}

func (sn *snapshotter) snapshotStageSharingLockPath(id, tmpID string) string {
	return filepath.Join(sn.snapshotStageRootDirPath(id, tmpID), "sharing.lock")
}

func (sn *snapshotter) snapshotStageContentsDirPath(id, tmpID string) string {
	return filepath.Join(sn.snapshotStageRootDirPath(id, tmpID), "contents")
}

func (sn *snapshotter) snapshotRmDirPath(id string) string {
	return filepath.Join(sn.rmDirPath(), id)
}

// TODO: explain
// TODO: could be more robust too
func denamespaceKey(key string) string {
	// we get keys from containerd in the form of "<namespace>/<incrementing int>/<actual key>", return <actual key>
	split := strings.Split(key, "/")
	return split[len(split)-1]
}
