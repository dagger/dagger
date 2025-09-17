package workers

import (
	"path/filepath"
	"strings"
	"syscall"
)

func applyBuildkitdPlatformFlags(args []string) []string {
	return args
}

func requireRoot() error {
	return nil
}

func getSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{}
}

func getBuildkitdAddr(tmpdir string) string {
	return "npipe:////./pipe/buildkitd-" + filepath.Base(tmpdir)
}

func getTraceSocketPath(tmpdir string) string {
	return `\\.\pipe\buildkit-otel-grpc-` + filepath.Base(tmpdir)
}

func getContainerdSock(tmpdir string) string {
	return `\\.\pipe\containerd-` + filepath.Base(tmpdir)
}

func getContainerdDebugSock(tmpdir string) string {
	return `\\.\pipe\containerd-` + filepath.Base(tmpdir) + `debug`
}

// no-op for parity with unix
func mountInfo(_ string) error {
	return nil
}

func chown(_ string, _, _ int) error {
	// Chown not supported on Windows
	return nil
}

func normalizeAddress(address string) string {
	address = filepath.ToSlash(address)
	if !strings.HasPrefix(address, "npipe://") {
		address = "npipe://" + address
	}
	return address
}
