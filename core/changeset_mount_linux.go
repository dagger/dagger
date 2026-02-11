//go:build !darwin && !windows

package core

import "syscall"

func bindMountDir(source, target string) error {
	return syscall.Mount(source, target, "", syscall.MS_BIND, "")
}

func unmountDir(target string) error {
	return syscall.Unmount(target, syscall.MNT_DETACH)
}
