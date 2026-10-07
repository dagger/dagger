//go:build linux && (386 || amd64 || arm64)

package nettracer

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWorkloadParentPath(t *testing.T) {
	for _, tc := range []struct {
		parent string
		path   string
	}{
		{"", "/sys/fs/cgroup/exec"},
		{"/", "/sys/fs/cgroup/exec"},
		{"/tenant", "/sys/fs/cgroup/tenant/exec"},
		{"tenant/engine", "/sys/fs/cgroup/tenant/engine/exec"},
		{"system.slice:dagger:", ""},
	} {
		t.Run(tc.parent, func(t *testing.T) {
			path, ok := WorkloadParentPath(tc.parent)
			require.Equal(t, tc.path != "", ok)
			require.Equal(t, tc.path, path)
		})
	}
}

// Opt in on a privileged Linux 6.15+ runner, like TestCommandAccounting.
// Loopback traffic is never workload network use, so the helper talks to the
// dev engine's debug server over the test network.
func TestWorkloadAccounting(t *testing.T) {
	if os.Getenv("DAGGER_TEST_EBPF") != "1" {
		t.Skip("set DAGGER_TEST_EBPF=1 on a privileged Linux 6.15+ runner")
	}
	// The dev engine shares the test container's bridge, so it is internal.
	peer := "daggerengine:6060"
	_, err := net.LookupHost("daggerengine")
	require.NoError(t, err, "the dev engine is the network peer")
	tracer, err := New()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, tracer.Close()) })
	require.NoError(t, tracer.cgroupErr)

	enginePath, err := currentCgroupPath()
	require.NoError(t, err)
	parent := filepath.Join(filepath.Dir(enginePath), fmt.Sprintf("workloads-test-%d", os.Getpid()))
	require.NoError(t, tracer.AttachWorkloads(parent))
	t.Cleanup(func() { require.NoError(t, os.Remove(parent)) })
	require.Error(t, tracer.AttachWorkloads(parent), "attaching twice must fail")

	cookie, err := CurrentNetnsCookie()
	require.NoError(t, err)
	_, err = tracer.Workload(filepath.Join(parent, "a", "b"), cookie)
	require.Error(t, err, "only direct children of the parent are workloads")

	path := filepath.Join(parent, "workload")
	workload, err := tracer.Workload(path, cookie)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, workload.Close())
		require.NoError(t, os.Remove(path))
	})
	before, err := workload.Sample()
	require.NoError(t, err)
	require.Equal(t, Sample{}, before)

	// A process in a cgroup nested below the workload, in the workload's
	// network namespace (like a nested engine), counts toward the workload.
	nested := filepath.Join(path, "nested")
	require.NoError(t, os.Mkdir(nested, 0o755))
	t.Cleanup(func() { require.NoError(t, removeWhenEmpty(nested)) })
	fd, err := os.Open(nested)
	require.NoError(t, err)
	defer fd.Close()
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestWorkloadNetworkHelper$")
	cmd.Env = append(os.Environ(), "NETTRACER_HELPER=workload", "NETTRACER_PEER="+peer)
	cmd.SysProcAttr = &syscall.SysProcAttr{UseCgroupFD: true, CgroupFD: int(fd.Fd())}
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	require.NoError(t, cmd.Run(), output.String())

	sample, err := workload.Sample()
	require.NoError(t, err)
	require.Greater(t, sample.InternalTX, uint64(0))
	require.Greater(t, sample.InternalRX, uint64(1024))
	require.Zero(t, sample.ExternalTX)
	require.Zero(t, sample.ExternalRX)

	require.NoError(t, workload.Close())
	_, err = workload.Sample()
	require.Error(t, err, "closed workloads release their counters")
}

func TestWorkloadNetworkHelper(t *testing.T) {
	if os.Getenv("NETTRACER_HELPER") != "workload" {
		t.Skip("subprocess fixture")
	}
	conn, err := net.Dial("tcp", os.Getenv("NETTRACER_PEER"))
	require.NoError(t, err)
	defer conn.Close()
	_, err = io.WriteString(conn, "GET /debug/pprof/goroutine?debug=1 HTTP/1.0\r\n\r\n")
	require.NoError(t, err)
	n, err := io.Copy(io.Discard, conn)
	require.NoError(t, err)
	require.Greater(t, n, int64(1024))
}

// removeWhenEmpty removes a cgroup once its exited processes are gone.
func removeWhenEmpty(path string) error {
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := os.Remove(path)
		if err == nil || os.IsNotExist(err) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(50 * time.Millisecond)
	}
}
