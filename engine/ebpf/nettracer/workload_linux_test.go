//go:build linux && (386 || amd64 || arm64)

package nettracer

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// isPublic applies nonPublicPrefixes as the LPM tries do: the longest
// matching prefix decides.
func isPublic(addr netip.Addr) bool {
	bits, public := -1, true
	for _, entry := range nonPublicPrefixes {
		if entry.prefix.Contains(addr) && entry.prefix.Bits() > bits {
			bits, public = entry.prefix.Bits(), entry.public
		}
	}
	return public
}

func TestNonPublicPrefixes(t *testing.T) {
	v4, v6 := 0, 0
	for _, entry := range nonPublicPrefixes {
		require.Equal(t, entry.prefix.Masked(), entry.prefix, "prefix %s must be masked", entry.prefix)
		if entry.prefix.Addr().Is4() {
			v4++
		} else {
			v6++
		}
	}
	// Each family's trie holds at most 32 prefixes.
	require.LessOrEqual(t, v4, 32)
	require.LessOrEqual(t, v6, 32)
	for _, public := range []string{
		"1.1.1.1", "8.8.8.8", "192.0.0.9", "192.0.0.10",
		"2606:4700:4700::1111", "64:ff9b::808:808", "2001:1::1", "2001:4:112::1", "2001:20::1",
	} {
		require.True(t, isPublic(netip.MustParseAddr(public)), "%s is globally reachable", public)
	}
	for _, private := range []string{
		"10.89.3.2", "172.17.0.2", "192.168.1.1", "100.64.0.1", "192.0.0.8", "192.0.2.1",
		"198.51.100.1", "203.0.113.1", "224.0.0.1", "255.255.255.255",
		"fd00::1", "fe80::1", "2001:db8::1", "2001:2::1", "2001::1", "3fff::1", "ff02::1",
	} {
		require.False(t, isPublic(netip.MustParseAddr(private)), "%s is not globally reachable", private)
	}
}

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

	// Sockets in another network namespace never count toward a workload.
	otherPath := filepath.Join(parent, "other-netns")
	other, err := tracer.Workload(otherPath, cookie+1)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, other.Close())
		require.NoError(t, removeWhenEmpty(otherPath))
	})
	otherFD, err := os.Open(otherPath)
	require.NoError(t, err)
	defer otherFD.Close()
	cmd = exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestWorkloadNetworkHelper$")
	cmd.Env = append(os.Environ(), "NETTRACER_HELPER=workload", "NETTRACER_PEER="+peer)
	cmd.SysProcAttr = &syscall.SysProcAttr{UseCgroupFD: true, CgroupFD: int(otherFD.Fd())}
	output.Reset()
	cmd.Stdout, cmd.Stderr = &output, &output
	require.NoError(t, cmd.Run(), output.String())
	otherSample, err := other.Sample()
	require.NoError(t, err)
	require.Equal(t, Sample{}, otherSample, "a nested namespace's private traffic is not counted")

	// Its traffic to a public address counts as external. Sending a UDP
	// datagram needs only a route, not a reply. 192.0.2.1 is a documentation
	// address; mark it public here so no internet access is needed.
	require.NoError(t, tracer.setNonPublicPrefix(netip.MustParsePrefix("192.0.2.1/32"), true))
	cmd = exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestWorkloadNetworkHelper$")
	cmd.Env = append(os.Environ(), "NETTRACER_HELPER=public-udp")
	cmd.SysProcAttr = &syscall.SysProcAttr{UseCgroupFD: true, CgroupFD: int(otherFD.Fd())}
	output.Reset()
	cmd.Stdout, cmd.Stderr = &output, &output
	require.NoError(t, cmd.Run(), output.String())
	otherSample, err = other.Sample()
	require.NoError(t, err)
	require.Greater(t, otherSample.ExternalTX, uint64(0))
	require.Zero(t, otherSample.InternalTX)
	require.Zero(t, otherSample.InternalRX)

	require.NoError(t, workload.Close())
	_, err = workload.Sample()
	require.Error(t, err, "closed workloads release their counters")
}

func TestWorkloadNetworkHelper(t *testing.T) {
	switch os.Getenv("NETTRACER_HELPER") {
	case "workload":
	case "public-udp":
		// The test marks this documentation address public; no reply comes
		// back, and only the send is counted.
		conn, err := net.Dial("udp", "192.0.2.1:9")
		require.NoError(t, err)
		defer conn.Close()
		_, err = conn.Write(make([]byte, 512))
		require.NoError(t, err)
		return
	default:
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
