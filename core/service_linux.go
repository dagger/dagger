//go:build !darwin && !windows

package core

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/dagger/dagger/engine/buildkit"
	"github.com/opencontainers/runtime-spec/specs-go"
	"golang.org/x/sys/unix"
)

func mountIntoContainer(ctx context.Context, containerID, sourcePath, targetPath string) error {
	fdMnt, err := unix.OpenTree(unix.AT_FDCWD, sourcePath, unix.OPEN_TREE_CLONE|unix.OPEN_TREE_CLOEXEC)
	if err != nil {
		return fmt.Errorf("open tree %s: %w", sourcePath, err)
	}
	defer unix.Close(fdMnt)
	return buildkit.GetGlobalNamespaceWorkerPool().RunInNamespaces(ctx, containerID, []specs.LinuxNamespace{
		{Type: specs.MountNamespace},
	}, func() error {
		// Create target directory if it doesn't exist
		if _, err := os.Stat(targetPath); os.IsNotExist(err) {
			if err := os.MkdirAll(targetPath, 0755); err != nil && !os.IsExist(err) {
				return fmt.Errorf("mkdir %s: %w", targetPath, err)
			}
		}

		// Unmount any existing mount at the target path
		err = unix.Unmount(targetPath, unix.MNT_DETACH)
		if err != nil && err != unix.EINVAL && err != unix.ENOENT {
			slog.Warn("unmount failed during container remount", "path", targetPath, "error", err)
			// Continue anyway, might not be mounted
		}

		err = unix.MoveMount(fdMnt, "", unix.AT_FDCWD, targetPath, unix.MOVE_MOUNT_F_EMPTY_PATH)
		if err != nil {
			return fmt.Errorf("move mount to %s: %w", targetPath, err)
		}

		return nil
	})
}
