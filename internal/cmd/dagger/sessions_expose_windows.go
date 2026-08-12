//go:build windows

package daggercmd

import (
	"context"
	"errors"
	"os"
)

var errExposeWindowsUnsupported = errors.New("detached up and sessions expose are not yet supported on Windows")

func exposePlatformSupported() bool { return false }

func inspectLocalExpose(context.Context, exposePaths) (*os.File, *exposeStatus, error) {
	return nil, nil, errExposeWindowsUnsupported
}

func stopLocalExpose(context.Context, exposePaths) error {
	return errExposeWindowsUnsupported
}

func stopAndAcquireLocalExpose(context.Context, exposePaths) (*os.File, error) {
	return nil, errExposeWindowsUnsupported
}

func spawnExposeServer(context.Context, exposeServerConfig, exposePaths, *os.File) (*exposeStartup, error) {
	return nil, errExposeWindowsUnsupported
}

func runExposePortServer(context.Context, string) error {
	return errExposeWindowsUnsupported
}
