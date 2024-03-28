//go:build !windows
// +build !windows

package idtui

import "syscall"

func sigquit() {
	syscall.Kill(syscall.Getpid(), syscall.SIGQUIT)
}
