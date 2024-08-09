package volume

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/containerd/continuity/fs"
	"github.com/gofrs/flock"
	"github.com/moby/buildkit/identity"
	"github.com/moby/buildkit/util/bklog"
	"golang.org/x/sys/unix"
)

type state string

const (
	// TODO: comments on each one
	unknown   state = ""
	notExists state = "notExists"
	active    state = "active"
	committed state = "committed"
	removing  state = "removing"
)

type InvalidStateTransitionError struct {
	From state
	To   state
}

func (e InvalidStateTransitionError) Error() string {
	return fmt.Sprintf("invalid state transition from %s to %s", e.From, e.To)
}

type snapshot struct {
	id        string
	curState  state
	stateLock *lock // nil if curState is unknown or notExists
}

func (sn *snapshotter) loadSnapshot(ctx context.Context, id string) (*snapshot, error) {
	sn.mu.Lock()
	defer sn.mu.Unlock()

	snap, ok := sn.snapshots[id]
	if ok {
		return snap, nil
	}

	stateLock := &lock{flock: flock.New(sn.snapshotStateFilePath(id))}
	ok, err := stateLock.ShareLock()
	switch {
	case ok:
		// got the shared lock, read the current state
		curState, err := sn.readSnapshotState(ctx, id)
		if err != nil {
			stateLock.Unlock()
			return nil, fmt.Errorf("failed to read snapshot state: %w", err)
		}

		snap := &snapshot{
			id:        id,
			curState:  curState,
			stateLock: stateLock,
		}

		sn.snapshots[id] = snap
		return snap, nil

	case err == nil:
		// someone else is removing the snapshot
		return &snapshot{
			id:       id,
			curState: removing,
		}, nil

	case errors.Is(err, os.ErrNotExist):
		// snapshot does not exist
		return &snapshot{
			id:       id,
			curState: notExists,
		}, nil

	default:
		return nil, fmt.Errorf("failed to get state file: %w", err)
	}
}

func (sn *snapshotter) toActive(ctx context.Context, id, parentID string) (rerr error) {
	snap, err := sn.loadSnapshot(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to load snapshot: %w", err)
	}

	switch snap.curState {
	case notExists:
		tmpID := identity.NewID()

		stageRootDirPath := sn.snapshotStageRootDirPath(snap.id, tmpID)
		if err := os.Mkdir(stageRootDirPath, 0755); err != nil {
			return fmt.Errorf("failed to create stage dir: %w", err)
		}
		defer os.RemoveAll(stageRootDirPath) // cleanup in all cases if rename doesn't work or we return before it

		sf := stateFile{State: active}
		bs, err := json.Marshal(sf)
		if err != nil {
			return fmt.Errorf("failed to marshal state file: %w", err)
		}
		if err := os.WriteFile(sn.snapshotStageStateFilePath(snap.id, tmpID), bs, 0644); err != nil {
			return fmt.Errorf("failed to create stage state file: %w", err)
		}
		if err := os.WriteFile(sn.snapshotStageSharingLockPath(snap.id, tmpID), []byte{}, 0644); err != nil {
			return fmt.Errorf("failed to create stage sharing lock: %w", err)
		}
		if err := os.Mkdir(sn.snapshotStageContentsDirPath(snap.id, tmpID), 0755); err != nil {
			return fmt.Errorf("failed to create stage contents dir: %w", err)
		}

		if parentID != "" {
			if err := fs.CopyDir(sn.snapshotStageContentsDirPath(snap.id, tmpID), sn.snapshotContentsDirPath(parentID)); err != nil {
				return fmt.Errorf("failed to copy parent contents: %w", err)
			}
		}

		stateLock := &lock{flock: flock.New(sn.snapshotStageStateFilePath(snap.id, tmpID))}
		ok, err := stateLock.ShareLock()
		if err != nil || !ok {
			return fmt.Errorf("failed to get state lock: %w", err)
		}

		err = os.Rename(stageRootDirPath, sn.snapshotRootDirPath(snap.id))
		switch {
		case err == nil:
			// success
			sn.mu.Lock()
			defer sn.mu.Unlock()
			_, ok = sn.snapshots[snap.id]
			if ok {
				// someone else in this process already created it, which is fine
				stateLock.Unlock()
			} else {
				sn.snapshots[snap.id] = &snapshot{
					id:        snap.id,
					curState:  active,
					stateLock: stateLock,
				}
			}

			return nil

		case errors.Is(err, os.ErrExist) || errors.Is(err, unix.ENOTEMPTY):
			// someone beat us to creating it
			stateLock.Unlock()
			newSnap, err := sn.loadSnapshot(ctx, snap.id)
			if err != nil {
				return fmt.Errorf("failed to load new snapshot: %w", err)
			}
			if newSnap.curState != active {
				return fmt.Errorf("new snapshot %s is not active", snap.id)
			}

			return nil

		default:
			// something else went wrong
			return fmt.Errorf("failed to rename stage dir: %w", err)
		}

	case active:
		return nil

	case removing:
		// TODO:???? Try again a few times with a timeout?
		return InvalidStateTransitionError{From: snap.curState, To: active}

	default:
		return InvalidStateTransitionError{From: snap.curState, To: active}
	}
}

func (sn *snapshotter) toCommitted(ctx context.Context, oldID, newID string) (rerr error) {
	snap, err := sn.loadSnapshot(ctx, oldID)
	if err != nil {
		return fmt.Errorf("failed to load snapshot: %w", err)
	}

	switch snap.curState {
	case active:
		// try to upgrade shared lock to an exclusive one
		ok, err := snap.stateLock.ExclusiveLock()
		switch {
		case ok:
			// handled below

		case err == nil:
			// someone else is removing or committing the snapshot
			return fmt.Errorf("snapshot %s is being removed or committed", snap.id)

		default:
			return fmt.Errorf("failed to get exclusive lock: %w", err)
		}

		tmpID := identity.NewID()

		stageRootDirPath := sn.snapshotStageRootDirPath(snap.id, tmpID)
		if err := os.Rename(sn.snapshotRootDirPath(snap.id), stageRootDirPath); err != nil {
			return fmt.Errorf("failed to move committing snapshot to stage: %w", err)
		}
		defer os.RemoveAll(stageRootDirPath) // cleanup in all cases if rename doesn't work or we return before it

		if err := os.Remove(sn.snapshotStageStateFilePath(snap.id, tmpID)); err != nil {
			return fmt.Errorf("failed to remove stage state file: %w", err)
		}
		if err := snap.stateLock.Unlock(); err != nil {
			return fmt.Errorf("failed to unlock state file: %w", err)
		}

		sf := stateFile{State: committed}
		bs, err := json.Marshal(sf)
		if err != nil {
			return fmt.Errorf("failed to marshal state file: %w", err)
		}
		if err := os.WriteFile(sn.snapshotStageStateFilePath(snap.id, tmpID), bs, 0644); err != nil {
			return fmt.Errorf("failed to create stage state file: %w", err)
		}
		newLock := &lock{flock: flock.New(sn.snapshotStageStateFilePath(snap.id, tmpID))}
		ok, err = newLock.ShareLock()
		if err != nil || !ok {
			return fmt.Errorf("failed to get new state lock: %w", err)
		}

		err = os.Rename(stageRootDirPath, sn.snapshotRootDirPath(newID))
		switch {
		case err == nil:
			// success
			newSnap := &snapshot{
				id:        newID,
				curState:  committed,
				stateLock: newLock,
			}

			sn.mu.Lock()
			defer sn.mu.Unlock()
			_, ok = sn.snapshots[newSnap.id]
			if ok {
				// someone else in this process already created it, which is fine
				newSnap.stateLock.Unlock()
			} else {
				sn.snapshots[newSnap.id] = newSnap
			}

			return nil

		case errors.Is(err, os.ErrExist) || errors.Is(err, unix.ENOTEMPTY):
			// someone beat us to creating it
			newLock.Unlock()
			newSnap, err := sn.loadSnapshot(ctx, newID)
			if err != nil {
				return fmt.Errorf("failed to load new snapshot: %w", err)
			}
			if newSnap.curState != committed {
				return fmt.Errorf("new snapshot %s is not committed", newID)
			}

			return nil

		default:
			// something else went wrong
			newLock.Unlock()
			return fmt.Errorf("failed to rename stage dir: %w", err)
		}

	case committed:
		return nil

	default:
		return InvalidStateTransitionError{From: snap.curState, To: committed}
	}
}

func (sn *snapshotter) toRemoved(ctx context.Context, id string) (rerr error) {
	snap, err := sn.loadSnapshot(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to load snapshot: %w", err)
	}
	defer func() {
		if rerr == nil {
			snap.stateLock.Unlock()
			sn.mu.Lock()
			defer sn.mu.Unlock()
			delete(sn.snapshots, id)
		}
	}()

	switch snap.curState {
	case notExists:
		return nil

	case active:
		// handled below

	case committed:
		// handled below

	case removing:
		return nil
	}

	ok, err := snap.stateLock.ExclusiveLock()
	switch {
	case ok:
		// handled below
	case err == nil:
		// someone else is removing/committing the snapshot
		return nil
	case errors.Is(err, os.ErrNotExist):
		// someone else is removing/committing the snapshot
		return nil
	default:
		return fmt.Errorf("failed to get exclusive lock: %w", err)
	}

	if err := os.Rename(sn.snapshotRootDirPath(id), sn.snapshotRmDirPath(id)); err != nil {
		return fmt.Errorf("failed to move removing snapshot: %w", err)
	}
	if err := os.RemoveAll(sn.snapshotRmDirPath(id)); err != nil {
		bklog.G(ctx).WithError(err).Error("failed to remove snapshot from rm dir")
	}

	return nil
}

type stateFile struct {
	State state `json:"state"`
}

func (sn *snapshotter) readSnapshotState(ctx context.Context, id string) (state, error) {
	bs, err := os.ReadFile(sn.snapshotStateFilePath(id))
	if err != nil {
		return "", fmt.Errorf("failed to read state file: %w", err)
	}
	var sf stateFile
	if err := json.Unmarshal(bs, &sf); err != nil {
		return "", fmt.Errorf("failed to unmarshal state file: %w", err)
	}
	return sf.State, nil
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

func (sn *snapshotter) snapshotStateFilePath(id string) string {
	return filepath.Join(sn.snapshotRootDirPath(id), "state.json")
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

func (sn *snapshotter) snapshotStageStateFilePath(id, tmpID string) string {
	return filepath.Join(sn.snapshotStageRootDirPath(id, tmpID), "state.json")
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

// TODO: explain why wrapper helps
type lock struct {
	flock    *flock.Flock
	mu       sync.Mutex
	released bool
}

func (l *lock) ShareLock() (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	switch {
	case l.released:
		return false, fmt.Errorf("lock already released")
	case l.flock.RLocked():
		return true, nil
	case l.flock.Locked():
		return false, fmt.Errorf("cannot downgrade lock")
	default:
		return l.flock.TryRLock()
	}
}

func (l *lock) ExclusiveLock() (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	switch {
	case l.released:
		return false, fmt.Errorf("lock already released")
	case l.flock.RLocked():
		return l.flock.TryLock()
	case l.flock.Locked():
		return true, nil
	default:
		return l.flock.TryLock()
	}
}

func (l *lock) Unlock() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	err := l.flock.Unlock()
	l.released = true
	return err
}

type snapshotTree struct {
	// key -> snapshotVtx
	vtxs map[string]*snapshotVtx
}

func (st *snapshotTree) add(key, parentKey string) error {
	existing, ok := st.vtxs[key]
	if !ok {
		existing = &snapshotVtx{
			key:          key,
			childrenKeys: map[string]struct{}{},
		}
		st.vtxs[key] = existing
	}
	if existing.parentKey != "" && existing.parentKey != parentKey {
		return fmt.Errorf("snapshot %s already has parent %s", key, existing.parentKey)
	}
	existing.parentKey = parentKey

	if parentKey != "" {
		existingParent, ok := st.vtxs[parentKey]
		if !ok {
			existingParent = &snapshotVtx{
				key:          parentKey,
				childrenKeys: map[string]struct{}{},
			}
			st.vtxs[parentKey] = existingParent
		}
		existingParent.childrenKeys[key] = struct{}{}
	}

	return nil
}

func (st *snapshotTree) keysUnder(rootKey string) []string {
	var keys []string

	curKeys := []string{rootKey}
	for len(curKeys) > 0 {
		keys = append(keys, curKeys...)
		var nextKeys []string
		for _, key := range curKeys {
			vtx := st.vtxs[key]
			for childKey := range vtx.childrenKeys {
				nextKeys = append(nextKeys, childKey)
			}
		}
		curKeys = nextKeys
	}

	return keys
}

type snapshotVtx struct {
	key          string
	parentKey    string
	childrenKeys map[string]struct{}
}
