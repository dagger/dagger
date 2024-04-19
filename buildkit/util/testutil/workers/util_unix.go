//go:build !windows
// +build !windows

package workers

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/moby/buildkit/util/testutil/integration"
	"github.com/pkg/errors"
)

func applyBuildkitdPlatformFlags(args []string) []string {
	return append(args, "--oci-worker=false")
}

func requireRoot() error {
	if os.Getuid() != 0 {
		return errors.Wrap(integration.ErrRequirements, "requires root")
	}
	return nil
}

func getSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		Setsid: true, // stretch sudo needs this for sigterm
	}
}

func getBuildkitdAddr(tmpdir string) string {
	return "unix://" + filepath.Join(tmpdir, "buildkitd.sock")
}

func getTraceSocketPath(tmpdir string) string {
	return filepath.Join(tmpdir, "otel-grpc.sock")
}

func getContainerdSock(tmpdir string) string {
	return filepath.Join(tmpdir, "containerd.sock")
}

func getContainerdDebugSock(tmpdir string) string {
	return filepath.Join(tmpdir, "debug.sock")
}

func mountInfo(tmpdir string) error {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return errors.Wrap(err, "failed to open mountinfo")
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		if strings.Contains(s.Text(), tmpdir) {
			return errors.Errorf("leaked mountpoint for %s", tmpdir)
		}
	}
	return s.Err()
}

// moved here since os.Chown is not supported on Windows.
// see no-op counterpart in util_windows.go
func chown(name string, uid, gid int) error {
	return os.Chown(name, uid, gid)
}

func normalizeAddress(address string) string {
	// for parity with windows, no effect for unix
	return address
}
