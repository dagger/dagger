//go:build !windows

package main

import (
	"net"
	"os"
	"path/filepath"

	"github.com/adrg/xdg"
	"golang.org/x/sys/unix"
)

func runtimeDir() string {
	// Try to use the proper runtime dir
	runtimeDir := xdg.RuntimeDir
	if err := os.MkdirAll(runtimeDir, 0700); err == nil {
		return runtimeDir
	}
	// Sometimes systems are misconfigured such that the runtime dir
	// doesn't exist but also can't be created by non-root users, so
	// fallback to a tmp dir
	return os.TempDir()
}

func createListener(sessionID string) (net.Listener, string, func() error, error) {
	sockPath := filepath.Join(runtimeDir(), "dagger-session-"+sessionID+".sock")

	// the permissions of the socket file are governed by umask, so we assume
	// that nothing else is writing files right now and set umask such that
	// the socket starts without any group or other permissions
	oldMask := unix.Umask(0077)
	defer unix.Umask(oldMask)

	l, err := net.Listen("unix", sockPath)
	if err != nil {
		return nil, "", nil, err
	}
	return l, sockPath, func() error {
		return os.Remove(sockPath)
	}, nil
}
