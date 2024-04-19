//go:build !linux
// +build !linux

package client

import (
	"os"

	"github.com/containerd/continuity/fs/fstest"
	"github.com/pkg/errors"
)

func mkfifo(path string, mode os.FileMode) fstest.Applier {
	return applyFn(func(string) error {
		return errors.New("mkfifo applier not implemented yet on this platform")
	})
}

func mkchardev(path string, mode os.FileMode, maj, min uint32) fstest.Applier {
	return applyFn(func(string) error {
		return errors.New("mkchardev applier not implemented yet on this platform")
	})
}
