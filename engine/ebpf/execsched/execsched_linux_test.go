//go:build linux && (amd64 || arm64)

package execsched

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine/ebpf/internal/ebpfutil"
	"github.com/dagger/dagger/engine/ebpf/wcbpf"
)

func TestMain(m *testing.M) {
	if os.Getenv("DAGGER_TEST_EBPF") == "1" {
		if err := leaveRootCgroup(); err != nil {
			fmt.Fprintln(os.Stderr, "moving the test out of the root cgroup:", err)
			os.Exit(1)
		}
	}
	os.Exit(m.Run())
}

// leaveRootCgroup moves this process out of the root of its cgroup namespace,
// as the engine's entrypoint moves the engine to /engine, so the test can
// create sibling cgroups for its execs.
func leaveRootCgroup() error {
	path, err := currentCgroupPath()
	if err != nil {
		return err
	}
	if path != "/sys/fs/cgroup" {
		return nil
	}
	child := filepath.Join(path, "execsched-test")
	if err := os.Mkdir(child, 0o755); err != nil && !os.IsExist(err) {
		return err
	}
	return os.WriteFile(filepath.Join(child, "cgroup.procs"), []byte(strconv.Itoa(os.Getpid())), 0o644)
}

func currentCgroupPath() (string, error) {
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return "", err
	}
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		if rest, ok := strings.CutPrefix(line, "0::"); ok {
			return filepath.Join("/sys/fs/cgroup", rest), nil
		}
	}
	return "", fmt.Errorf("no unified cgroup in %q", data)
}

// newExec makes a resource cgroup for an exec beside the test's own, measured
// by l, and its container cgroup.
func newExec(t *testing.T, l *loaded, name string, init bool) (resource string, id uint64, w wcbpf.ExecWatch) {
	t.Helper()
	self, err := currentCgroupPath()
	require.NoError(t, err)
	resource = filepath.Join(filepath.Dir(self), fmt.Sprintf("execsched-%s-%d", name, os.Getpid()))
	require.NoError(t, os.Mkdir(resource, 0o755))
	t.Cleanup(func() { require.NoError(t, os.Remove(resource)) })
	id, err = ebpfutil.CgroupID(resource)
	require.NoError(t, err)
	w, err = l.WatchExec(wcbpf.ExecInfo{CgroupID: id, Init: init})
	require.NoError(t, err)
	mkdirCgroup(t, filepath.Join(resource, "container"))
	return resource, id, w
}

func mkdirCgroup(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, os.Mkdir(path, 0o755))
	t.Cleanup(func() { require.NoError(t, os.Remove(path)) })
}

// startIn starts a command in the cgroup at path.
func startIn(t *testing.T, path string, args ...string) (*exec.Cmd, *bytes.Buffer) {
	t.Helper()
	fd, err := os.Open(path)
	require.NoError(t, err)
	defer fd.Close()
	cmd := exec.CommandContext(t.Context(), args[0], args[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{UseCgroupFD: true, CgroupFD: int(fd.Fd())}
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	require.NoError(t, cmd.Start())
	return cmd, &output
}

// The program measures an exec laid out as the engine lays one out: its
// container's processes in the "container" child of its resource cgroup.
// The test's shell stands for the engine's init, and its first command for
// the workload; two busy loops pinned to one CPU wait for it in turn. A mount
// helper's leaf and a nested container's cgroup run work that counts toward
// the exec, without taking a container role.
func TestExecSched(t *testing.T) {
	if os.Getenv("DAGGER_TEST_EBPF") != "1" {
		t.Skip("set DAGGER_TEST_EBPF=1 on a privileged Linux runner")
	}
	loadedProgram, err := load()
	require.NoError(t, err)
	l := loadedProgram.(*loaded)
	t.Cleanup(func() { require.NoError(t, l.Close()) })

	resource, id, w := newExec(t, l, "exec", true)
	_, err = l.WatchExec(wcbpf.ExecInfo{CgroupID: id})
	require.Error(t, err, "an exec is measured once")
	helper := filepath.Join(resource, "sshfs")
	mkdirCgroup(t, helper)
	mkdirCgroup(t, filepath.Join(helper, "mount"))
	nested := filepath.Join(resource, "container", "nested")
	mkdirCgroup(t, nested)
	mkdirCgroup(t, filepath.Join(nested, "ctr"))

	// A fixed amount of work, whatever the CPU it gets.
	spin := `i=0; while [ $i -lt 300000 ]; do i=$((i+1)); done`
	helperCmd, helperOut := startIn(t, filepath.Join(helper, "mount"), "/bin/sh", "-c", spin)
	nestedCmd, nestedOut := startIn(t, filepath.Join(nested, "ctr"), "/bin/sh", "-c", spin)
	busy := `timeout 0.4 taskset -c 0 sh -c "while :; do :; done"`
	start := time.Now()
	cmd, output := startIn(t, filepath.Join(resource, "container"), "/bin/sh", "-c", "sleep 0.1; "+busy+" & "+busy+" & wait")
	require.NoError(t, cmd.Wait(), output.String())
	require.NoError(t, helperCmd.Wait(), helperOut.String())
	require.NoError(t, nestedCmd.Wait(), nestedOut.String())
	t.Logf("ran in %s", time.Since(start))

	var m execschedExecSched
	require.NoError(t, l.objs.Execs.Lookup(id, &m))
	t.Logf("measured %+v", m)
	require.NotZero(t, m.ContainerStartNs)
	require.LessOrEqual(t, m.ContainerStartNs, m.InitExecNs)
	require.Less(t, m.InitExecNs, m.WorkloadExecNs)
	require.Less(t, m.WorkloadExecNs, m.WorkloadExitNs)
	require.LessOrEqual(t, m.WorkloadExitNs, m.InitExitNs)
	// The kernel's process IDs, not the test's PID namespace's.
	require.NotZero(t, m.InitTgid)
	require.NotEqual(t, m.InitTgid, m.WorkloadTgid)
	sleep := time.Duration(m.WorkloadExitNs - m.WorkloadExecNs)
	require.Greater(t, sleep, 90*time.Millisecond, "the workload, sleep 0.1")
	require.Less(t, sleep, time.Second)

	// Two loops on one CPU for 0.4 s each: each waits for about half.
	require.Greater(t, time.Duration(m.RunqWaitNs), 200*time.Millisecond)
	require.NotZero(t, m.RunqWaits)
	// What the exec's threads ran is what its cgroup counted. The helper's
	// and the nested container's work each exceed the margin, so the two
	// differ if either is missed.
	usage := cgroupCPU(t, resource)
	margin := float64(usage)/20 + float64(5*time.Millisecond)
	require.Greater(t, float64(cgroupCPU(t, filepath.Join(helper, "mount"))), margin)
	require.Greater(t, float64(cgroupCPU(t, filepath.Join(nested, "ctr"))), margin)
	require.InDelta(t, float64(usage), float64(m.OncpuNs), margin,
		"on-CPU %s, cgroup CPU %s", time.Duration(m.OncpuNs), time.Duration(usage))

	w.Finish(t.Context(), 0)
	require.Error(t, l.objs.Execs.Lookup(id, &m), "a finished exec is forgotten")

	// Without the init, the container's first process is the workload.
	resource, id, w = newExec(t, l, "noinit", false)
	cmd, output = startIn(t, filepath.Join(resource, "container"), "/bin/sh", "-c", "sleep 0.1")
	require.NoError(t, cmd.Wait(), output.String())
	require.NoError(t, l.objs.Execs.Lookup(id, &m))
	t.Logf("measured without the init %+v", m)
	require.Zero(t, m.InitExecNs)
	require.Zero(t, m.InitExitNs)
	require.LessOrEqual(t, m.ContainerStartNs, m.WorkloadExecNs)
	require.Greater(t, time.Duration(m.WorkloadExitNs-m.WorkloadExecNs), 90*time.Millisecond)
	w.Finish(t.Context(), 0)
}

// cgroupCPU is the CPU time a cgroup's tasks used, in nanoseconds.
func cgroupCPU(t *testing.T, path string) uint64 {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(path, "cpu.stat"))
	require.NoError(t, err)
	for line := range strings.SplitSeq(string(data), "\n") {
		if v, ok := strings.CutPrefix(line, "usage_usec "); ok {
			us, err := strconv.ParseUint(v, 10, 64)
			require.NoError(t, err)
			return us * 1000
		}
	}
	t.Fatalf("no usage_usec in %s", data)
	return 0
}
