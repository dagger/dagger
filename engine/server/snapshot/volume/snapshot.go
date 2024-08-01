package volume

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/snapshots"
	"github.com/containerd/continuity/fs"
	"github.com/containerd/errdefs"
	"github.com/gofrs/flock"
	"github.com/moby/buildkit/solver/pb"
)

const (
	// TODO: reconsider value, seems too long for at least some ops
	flockRetryInterval = 100 * time.Millisecond

	removeLockWaitDuration = 1 * time.Second // TODO: ? made up

	metadataFilePerms = 0644
)

/* Filesystem layout:
<rootDir>/<snapshotid>/metadata.json - snapshot metadata, flocked for concurrent access
<rootDir>/<snapshotid>/sharing.lock - flocked to enforce shared/locked access and safe removal
<rootDir>/<snapshotid>/contents - snapshot contents

<rootDir>/stage/<snapshotid> - staging dir when creating the above files, atomically renamed to final location once initialized
<rootDir>/rm/<snapshotid> - dir used when removing snapshot, atomically renamed to here from original location
*/

// TODO: MAKE SURE ALL ABOVE BASE DIRS ARE INITIALIZED WITH SNAPSHOTTER, CONCURRENT SAFE

func (sn *VolumeSnapshotter) getOrInitSnapshot(ctx context.Context, key string, opts ...snapshots.Opt) (*snapshot, error) {
	// TODO: do checks for dir escape here

	snap := &snapshot{sn: sn, id: key}

	checkAlreadyExists := func() (bool, error) {
		stat, err := os.Lstat(snap.rootDir())
		switch {
		case err == nil:
			if !stat.IsDir() {
				return false, fmt.Errorf("snapshot root is not a directory")
			}
			return true, nil

		case errors.Is(err, os.ErrNotExist):
			return false, nil

		default:
			return false, fmt.Errorf("failed to stat snapshot root: %w", err)
		}
	}

	alreadyExists, err := checkAlreadyExists()
	if err != nil {
		return nil, err
	}
	if alreadyExists {
		return snap, nil
	}

	// TODO: fix perms here and elsewhere
	if err := os.MkdirAll(snap.stageRootDir(), 0755); err != nil {
		return nil, fmt.Errorf("failed to create stage root: %w", err)
	}
	// ensure cleanup on failure or race with parallel creation
	defer os.RemoveAll(snap.stageRootDir())

	sharingLock := flock.New(snap.stageSharingLockFilePath())
	// TODO: smells off, do we want a timeout here? lots of corner cases to think through potentialy
	ok, err := sharingLock.TryLockContext(ctx, flockRetryInterval)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire exclusive lock: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("failed to acquire exclusive lock")
	}
	defer sharingLock.Unlock()

	// need to recheck if it already exists again to see if someone else created it in parallel already
	alreadyExists, err = checkAlreadyExists()
	if err != nil {
		return nil, err
	}
	if alreadyExists {
		return snap, nil
	}

	if err := os.Mkdir(snap.stageContentsDirPath(), 0755); err != nil {
		return nil, fmt.Errorf("failed to create stage contents dir: %w", err)
	}

	now := time.Now().UTC()
	initInfo := snapshots.Info{
		Kind:    snapshots.KindActive,
		Name:    key,
		Created: now,
		Updated: now,
	}
	for _, opt := range opts {
		if err := opt(&initInfo); err != nil {
			return nil, fmt.Errorf("failed to apply snapshot option: %w", err)
		}
	}
	infoBytes, err := json.Marshal(initInfo)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal snapshot metadata: %w", err)
	}
	if err := os.WriteFile(snap.stageMetadataFilePath(), infoBytes, metadataFilePerms); err != nil {
		return nil, fmt.Errorf("failed to write snapshot metadata: %w", err)
	}

	if err := os.Rename(snap.stageRootDir(), snap.rootDir()); err != nil {
		return nil, fmt.Errorf("failed to rename snapshot dir: %w", err)
	}

	return snap, nil
}

func (sn *VolumeSnapshotter) getSnapshot(ctx context.Context, key string) (*snapshot, error) {
	// TODO: do checks for dir escape here

	snap := &snapshot{sn: sn, id: key}

	// TODO: dedupe with getOrInitSnapshot
	stat, err := os.Lstat(snap.rootDir())
	switch {
	case err == nil:
		if !stat.IsDir() {
			return nil, fmt.Errorf("snapshot root is not a directory")
		}
		return snap, nil

	case errors.Is(err, os.ErrNotExist):
		return nil, fmt.Errorf("snapshot %s does not exist: %w", key, errdefs.ErrNotFound)

	default:
		return nil, fmt.Errorf("failed to stat snapshot root: %w", err)
	}
}

func (sn *VolumeSnapshotter) currentSnapshots(ctx context.Context) ([]*snapshot, error) {
	dirEnts, err := os.ReadDir(sn.rootDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read snapshot root: %w", err)
	}
	snaps := make([]*snapshot, 0, len(dirEnts))
	for _, dirEnt := range dirEnts {
		if !dirEnt.IsDir() {
			continue
		}
		switch dirEnt.Name() {
		case "stage", "rm":
			// TODO: use consts above
			continue
		default:
			snaps = append(snaps, &snapshot{sn: sn, id: dirEnt.Name()})
		}
	}
	return snaps, nil
}

type snapshot struct {
	sn *VolumeSnapshotter
	id string
}

func (s *snapshot) acquire(ctx context.Context, sharingMode pb.CacheSharingOpt) (func() error, error) {
	lockFilePath := s.sharingLockFilePath()

	lock := flock.New(lockFilePath)
	var ok bool
	var err error
	switch sharingMode {
	case pb.CacheSharingOpt_SHARED:
		ok, err = lock.TryRLockContext(ctx, flockRetryInterval)
	case pb.CacheSharingOpt_LOCKED:
		ok, err = lock.TryLockContext(ctx, flockRetryInterval)
	case pb.CacheSharingOpt_PRIVATE:
		// TODO: handle differently than locked? Maybe return err and have caller create new throwaway one?
		ok, err = lock.TryLockContext(ctx, flockRetryInterval)
	default:
		return nil, fmt.Errorf("unknown sharing mode: %v", sharingMode)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to acquire shared lock: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("failed to acquire shared lock")
	}

	return lock.Unlock, nil
}

// assumes caller has done an acquire call
func (s *snapshot) mounts(ctx context.Context) ([]mount.Mount, error) {
	return []mount.Mount{{
		Type:    "bind", // not a real type, but shows up in mountinfo so it's nice to set
		Source:  s.contentsDirPath(),
		Options: []string{"bind"},
	}}, nil
}

// will error if snapshot can't be exclusively acquired within a short timeout
// TODO: make sure concurrent removes work, could be error for one or both succeed
func (s *snapshot) tryRemove(ctx context.Context) error {
	lockFilePath := s.sharingLockFilePath()

	lock := flock.New(lockFilePath)

	timeoutCtx, cancel := context.WithTimeout(ctx, removeLockWaitDuration)
	defer cancel()
	ok, err := lock.TryLockContext(timeoutCtx, flockRetryInterval)
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("failed to acquire exclusive lock: %w", err)
	}
	if !ok {
		return fmt.Errorf("failed to acquire exclusive lock: %w", errdefs.ErrFailedPrecondition)
	}
	defer lock.Unlock()

	if err := os.Rename(s.rootDir(), s.rmDirPath()); err != nil {
		return fmt.Errorf("failed to rename snapshot dir: %w", err)
	}
	if err := os.RemoveAll(s.rmDirPath()); err != nil {
		return fmt.Errorf("failed to remove snapshot dir: %w", err)
	}
	return nil
}

// may error if snapshot is removed while called
func (s *snapshot) usage(ctx context.Context) (snapshots.Usage, error) {
	usage, err := fs.DiskUsage(ctx, s.contentsDirPath())
	if err != nil {
		return snapshots.Usage{}, fmt.Errorf("failed to get disk usage: %w", err)
	}
	return snapshots.Usage(usage), nil
}

// may error if snapshot is removed while called
func (s *snapshot) info(ctx context.Context) (snapshots.Info, error) {
	metadataFilePath := s.metadataFilePath()

	f, err := os.Open(metadataFilePath)
	if err != nil {
		return snapshots.Info{}, fmt.Errorf("failed to open metadata: %w", err)
	}
	defer f.Close()

	lock := flock.New(metadataFilePath,
		// avoid the default O_CREATE, we want the file to exist or otherwise error
		flock.SetFlag(os.O_RDONLY),
	)
	ok, err := lock.TryRLockContext(ctx, flockRetryInterval)
	if err != nil {
		return snapshots.Info{}, fmt.Errorf("failed to acquire shared lock: %w", err)
	}
	if !ok {
		return snapshots.Info{}, fmt.Errorf("failed to acquire shared lock")
	}
	defer lock.Unlock()

	bs, err := io.ReadAll(f)
	if err != nil {
		return snapshots.Info{}, fmt.Errorf("failed to read metadata: %w", err)
	}

	var info snapshots.Info
	if err := json.Unmarshal(bs, &info); err != nil {
		return snapshots.Info{}, fmt.Errorf("failed to unmarshal metadata: %w", err)
	}

	return info, nil
}

// may error if snapshot is removed while called
func (s *snapshot) updateInfo(ctx context.Context, info snapshots.Info, fieldpaths ...string) (snapshots.Info, error) {
	metadataFilePath := s.metadataFilePath()

	f, err := os.Open(metadataFilePath)
	if err != nil {
		return snapshots.Info{}, fmt.Errorf("failed to open metadata: %w", err)
	}
	defer func() {
		if f != nil {
			f.Close()
		}
	}()

	lock := flock.New(metadataFilePath,
		// avoid the default O_CREATE, we want the file to exist or otherwise error
		flock.SetFlag(os.O_RDONLY),
	)
	ok, err := lock.TryLockContext(ctx, flockRetryInterval)
	if err != nil {
		return snapshots.Info{}, fmt.Errorf("failed to acquire exclusive lock: %w", err)
	}
	if !ok {
		return snapshots.Info{}, fmt.Errorf("failed to acquire exclusive lock")
	}
	defer lock.Unlock()

	bs, err := io.ReadAll(f)
	if err != nil {
		return snapshots.Info{}, fmt.Errorf("failed to read metadata: %w", err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		return snapshots.Info{}, fmt.Errorf("failed to seek metadata: %w", err)
	}

	var updatedInfo snapshots.Info
	if err := json.Unmarshal(bs, &updatedInfo); err != nil {
		return snapshots.Info{}, fmt.Errorf("failed to unmarshal metadata: %w", err)
	}

	// ref: https://github.com/containerd/containerd/blob/8fc6bcff51318944179630522a095cc9dbf9f353/snapshots/storage/bolt.go#L109-L132
	if len(fieldpaths) > 0 {
		for _, path := range fieldpaths {
			if strings.HasPrefix(path, "labels.") {
				if updatedInfo.Labels == nil {
					updatedInfo.Labels = map[string]string{}
				}

				key := strings.TrimPrefix(path, "labels.")
				updatedInfo.Labels[key] = info.Labels[key]
				continue
			}

			switch path {
			case "labels":
				updatedInfo.Labels = info.Labels
			default:
				return snapshots.Info{}, fmt.Errorf("cannot update %q field on snapshot %q: %w", path, info.Name, errdefs.ErrInvalidArgument)
			}
		}
	} else {
		// Set mutable fields
		updatedInfo.Labels = info.Labels
	}
	updatedInfo.Updated = time.Now().UTC()

	bs, err = json.Marshal(updatedInfo)
	if err != nil {
		return snapshots.Info{}, fmt.Errorf("failed to marshal metadata: %w", err)
	}
	if _, err := f.Write(bs); err != nil {
		return snapshots.Info{}, fmt.Errorf("failed to write metadata: %w", err)
	}
	if err := f.Close(); err != nil {
		return snapshots.Info{}, fmt.Errorf("failed to close metadata: %w", err)
	}
	f = nil

	return updatedInfo, nil
}

// TODO: consolidate these somehow

func (s *snapshot) rootDir() string {
	return filepath.Join(s.sn.rootDir, s.id)
}

func (s *snapshot) contentsDirPath() string {
	return filepath.Join(s.rootDir(), "contents")
}

func (s *snapshot) metadataFilePath() string {
	return filepath.Join(s.rootDir(), "metadata.json")
}

func (s *snapshot) sharingLockFilePath() string {
	return filepath.Join(s.rootDir(), "sharing.lock")
}

func (s *snapshot) stageRootDir() string {
	return filepath.Join(s.sn.rootDir, "stage", s.id)
}

func (s *snapshot) stageContentsDirPath() string {
	return filepath.Join(s.stageRootDir(), "contents")
}

func (s *snapshot) stageMetadataFilePath() string {
	return filepath.Join(s.stageRootDir(), "metadata.json")
}

func (s *snapshot) stageSharingLockFilePath() string {
	return filepath.Join(s.stageRootDir(), "sharing.lock")
}

func (s *snapshot) rmDirPath() string {
	return filepath.Join(s.sn.rootDir, "rm", s.id)
}
