package host

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/snapshots"
	"github.com/containerd/continuity/fs"
	"github.com/docker/docker/pkg/idtools"
	"github.com/gofrs/flock"
	bksnapshot "github.com/moby/buildkit/snapshot"
)

const flockRetryInterval = 100 * time.Millisecond

// TODO: doc, also name sucks, not really host specific (VolumeSnapshotter?)
type HostSnapshotter struct {
	rootDir       string
	metadataStore *metadataStore
}

func NewHostSnapshotter(ctx context.Context, rootDir string) (*HostSnapshotter, error) {
}

var _ bksnapshot.MergeSnapshotter = (*HostSnapshotter)(nil)

func (sn *HostSnapshotter) Name() string {
	return "host"
}

func (sn *HostSnapshotter) AcquireShared(ctx context.Context, key string) (func() error, error) {
	lock, err := sn.getFlock(key)
	if err != nil {
		return nil, err
	}
	ok, err := lock.TryRLockContext(ctx, flockRetryInterval)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire shared lock: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("failed to acquire shared lock")
	}
	return lock.Unlock, nil
}

func (sn *HostSnapshotter) AcquireLocked(ctx context.Context, key string) (func() error, error) {
	lock, err := sn.getFlock(key)
	if err != nil {
		return nil, err
	}
	ok, err := lock.TryLockContext(ctx, flockRetryInterval)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire exclusive lock: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("failed to acquire exclusive lock")
	}
	return lock.Unlock, nil
}

func (sn *HostSnapshotter) Mounts(ctx context.Context, key string) (bksnapshot.Mountable, error) {
	dirPath, err := sn.dirPath(key)
	if err != nil {
		return nil, err
	}
	return volumeMountable{hostSrcPath: dirPath, readonly: true}, nil
}

func (sn *HostSnapshotter) Prepare(ctx context.Context, key, parent string, opts ...snapshots.Opt) (rerr error) {
	// TODO: support parent
	if parent != "" {
		return fmt.Errorf("parent snapshot is not supported")
	}
	dirPath, err := sn.dirPath(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dirPath, 0700); err != nil {
		return fmt.Errorf("failed to create dir %s: %w", dirPath, err)
	}
	defer func() {
		if rerr != nil {
			if err := os.RemoveAll(dirPath); err != nil {
				rerr = fmt.Errorf("failed to remove dir %s: %w", dirPath, err)
			}
		}
	}()

	return sn.metadataStore.newSnapshot(key)
}

func (sn *HostSnapshotter) Update(ctx context.Context, info snapshots.Info, fieldpaths ...string) (snapshots.Info, error) {
}

func (sn *HostSnapshotter) Usage(ctx context.Context, key string) (snapshots.Usage, error) {
	dirPath, err := sn.dirPath(key)
	if err != nil {
		return snapshots.Usage{}, err
	}
	usage, err := fs.DiskUsage(ctx, dirPath)
	if err != nil {
		return snapshots.Usage{}, fmt.Errorf("failed to get disk usage: %w", err)
	}
	return snapshots.Usage(usage), nil
}

func (sn *HostSnapshotter) Stat(ctx context.Context, key string) (snapshots.Info, error) {
	panic("implement me")
}

func (sn *HostSnapshotter) Remove(ctx context.Context, key string) error {
	panic("implement me")
}

func (sn *HostSnapshotter) Close() error {
	panic("implement me")
}

func (sn *HostSnapshotter) Walk(ctx context.Context, fn snapshots.WalkFunc, filters ...string) error {
	panic("implement me")
}

func (sn *HostSnapshotter) View(ctx context.Context, key, parent string, opts ...snapshots.Opt) (bksnapshot.Mountable, error) {
	return nil, fmt.Errorf("view not supported")
}

func (sn *HostSnapshotter) Commit(ctx context.Context, name, key string, opts ...snapshots.Opt) error {
	return fmt.Errorf("commit not supported")
}

func (sn *HostSnapshotter) IdentityMapping() *idtools.IdentityMapping {
	return nil
}

func (sn *HostSnapshotter) dirPath(key string) (string, error) {
	// fs.RootPath is perhaps overkill, but prevents key from ever doing any path shenanigans
	p, err := fs.RootPath(sn.rootDir, key)
	if err != nil {
		return "", fmt.Errorf("failed to get root path: %w", err)
	}
	relPath, err := filepath.Rel(sn.rootDir, p)
	if err != nil {
		return "", fmt.Errorf("failed to get relative path: %w", err)
	}
	if relPath == "." || !filepath.IsLocal(relPath) {
		return "", fmt.Errorf("snapshot key %s is not under root dir %s", key, sn.rootDir)
	}
	return p, nil
}

func (sn *HostSnapshotter) getFlock(key string) (*flock.Flock, error) {
	dirPath, err := sn.dirPath(key)
	if err != nil {
		return nil, err
	}
	return flock.New(filepath.Join(dirPath, ".lock")), nil
}

// TODO: use sqlite
type metadataStore struct {
	filePath string
}

func (ms *metadataStore) newSnapshot(key string) error {
}

type volumeMountable struct {
	hostSrcPath string
	readonly    bool
}

var _ bksnapshot.Mountable = (*volumeMountable)(nil)

func (m volumeMountable) Mount() ([]mount.Mount, func() error, error) {
	options := []string{"bind"}
	if m.readonly {
		options = append(options, "ro")
	}

	// TODO: double check we don't need a cleanup callback here
	return []mount.Mount{{
		Type:    "bind",
		Source:  m.hostSrcPath,
		Options: options,
	}}, func() error { return nil }, nil
}

func (m volumeMountable) IdentityMapping() *idtools.IdentityMapping {
	return nil
}
