//go:build linux && (386 || amd64 || arm64)

package nettracer

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestMain(m *testing.M) {
	// Helpers are placed in their cgroups by the test that starts them.
	if os.Getenv("DAGGER_TEST_EBPF") == "1" && os.Getenv("NETTRACER_HELPER") == "" {
		if err := leaveRootCgroup(); err != nil {
			fmt.Fprintln(os.Stderr, "moving the test out of the root cgroup:", err)
			os.Exit(1)
		}
	}
	os.Exit(m.Run())
}

// leaveRootCgroup moves this process out of the root of its cgroup namespace,
// as the engine's entrypoint moves the engine to /engine, so the tests can
// create the engine's sibling cgroups.
func leaveRootCgroup() error {
	path, err := currentCgroupPath()
	if err != nil {
		return err
	}
	if path != "/sys/fs/cgroup" {
		return nil
	}
	child := filepath.Join(path, "nettracer-test")
	if err := os.Mkdir(child, 0o755); err != nil && !os.IsExist(err) {
		return err
	}
	return os.WriteFile(filepath.Join(child, "cgroup.procs"), []byte(strconv.Itoa(os.Getpid())), 0o644)
}
