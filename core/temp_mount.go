package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/containerd/containerd/v2/core/mount"

	"github.com/dagger/dagger/engine/slog"
	bkcache "github.com/dagger/dagger/engine/snapshots"
)

// DiskTempMountDir holds the disk-backed temp mounts of running processes,
// under the engine's root, outside its snapshots. The engine moves what a
// previous engine left there aside when it starts, and removes it in the
// background.
const DiskTempMountDir = "temp-mounts"

// removeDiskTempDir removes a disk-backed temp mount's directory once its
// process has ended.
var removeDiskTempDir = os.RemoveAll

func prepareExecDiskTempMount(ctx context.Context) (bkcache.Mountable, error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, fmt.Errorf("get current query for disk temp mount: %w", err)
	}
	root := query.EngineVolumeState().RootDir
	if root == "" {
		return nil, fmt.Errorf("engine root is not configured")
	}
	return &execDiskTempMount{parent: filepath.Join(root, DiskTempMountDir)}, nil
}

type execDiskTempMount struct {
	parent string
}

// Mount makes an empty directory for the process and binds it writable, as a
// tmpfs would be. Releasing the mount, when the process has ended, removes
// the directory in the background: a nested engine's state can hold millions
// of files, and nothing waits for them to go.
func (mnt *execDiskTempMount) Mount(_ context.Context, readonly bool) (bkcache.MountableRef, error) {
	if err := os.MkdirAll(mnt.parent, 0o700); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(mnt.parent, "")
	if err != nil {
		return nil, err
	}
	// A tmpfs is world-writable with the sticky bit; so is this.
	if err := os.Chmod(dir, os.ModeSticky|0o777); err != nil {
		return nil, errors.Join(err, os.Remove(dir))
	}
	options := []string{"rbind"}
	if readonly {
		options = append(options, "ro")
	}
	release := func() error {
		go func() {
			if err := removeDiskTempDir(dir); err != nil {
				slog.Warn("failed to remove a disk temp mount; the engine's next start removes it", "dir", dir, "error", err)
			}
		}()
		return nil
	}
	return &execVolumeMountInstance{mounts: []mount.Mount{{
		Type:    "bind",
		Source:  dir,
		Options: options,
	}}, release: release}, nil
}
