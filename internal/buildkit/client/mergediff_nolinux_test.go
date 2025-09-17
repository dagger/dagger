//go:build !linux
// +build !linux

package client

import (
	"os"

	"github.com/containerd/continuity/fs/fstest"
	"github.com/pkg/errors"
)

func mkfifo(_ string, _ os.FileMode) fstest.Applier {
	return applyFn(func(string) error {
		return errors.New("mkfifo applier not implemented yet on this platform")
	})
}

func mkchardev(_ string, _ os.FileMode, _, _ uint32) fstest.Applier {
	return applyFn(func(string) error {
		return errors.New("mkchardev applier not implemented yet on this platform")
	})
}
