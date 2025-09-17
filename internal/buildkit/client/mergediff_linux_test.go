//go:build linux
// +build linux

package client

import (
	"os"
	"path/filepath"

	"github.com/containerd/continuity/fs/fstest"
	"golang.org/x/sys/unix"
)

func mknod(path string, mode os.FileMode, maj, min uint32) fstest.Applier {
	return applyFn(func(root string) error {
		return unix.Mknod(filepath.Join(root, path), uint32(mode), int(unix.Mkdev(maj, min)))
	})
}

func mkfifo(path string, mode os.FileMode) fstest.Applier {
	return mknod(path, mode|unix.S_IFIFO, 0, 0)
}

func mkchardev(path string, mode os.FileMode, maj, min uint32) fstest.Applier {
	return mknod(path, mode|unix.S_IFCHR, maj, min)
}
