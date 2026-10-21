package engineutil

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestProcTaskState(t *testing.T) {
	dir := t.TempDir()
	// The command name may itself contain spaces and parentheses.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stat"), []byte("42 (dagger (eng) x) D 1 42 42 0 -1 4194560\n"), 0o600))
	state, err := procTaskState(dir)
	require.NoError(t, err)
	require.Equal(t, "D", state)
	_, err = procTaskState(filepath.Join(dir, "missing"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestDescribeCgroupTasks(t *testing.T) {
	writeFile := func(path, data string) {
		t.Helper()
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
		require.NoError(t, os.WriteFile(path, []byte(data), 0o600))
	}
	proc := func(root, pid string, states map[string]string) {
		writeFile(filepath.Join(root, pid, "comm"), "dagger-engine\n")
		for tid, state := range states {
			writeFile(filepath.Join(root, pid, "task", tid, "stat"), tid+" (dagger-engine) "+state+" 1 1 1\n")
			writeFile(filepath.Join(root, pid, "task", tid, "stack"), "[<0>] wb_wait_for_completion+0x5a/0x90\n[<0>] sync_inodes_sb+0xd4/0x2b0\n")
		}
	}

	t.Run("uninterruptible threads with their stacks", func(t *testing.T) {
		cgroup, procRoot := t.TempDir(), t.TempDir()
		writeFile(filepath.Join(cgroup, "cgroup.procs"), "")
		writeFile(filepath.Join(cgroup, "engine", "cgroup.procs"), "42\n")
		proc(procRoot, "42", map[string]string{"42": "S", "43": "D"})
		require.Equal(t,
			"pid 42 (dagger-engine), thread 43 uninterruptible in [[<0>] wb_wait_for_completion+0x5a/0x90 <- [<0>] sync_inodes_sb+0xd4/0x2b0]",
			describeCgroupTasks(cgroup, procRoot))
	})

	t.Run("empty", func(t *testing.T) {
		cgroup := t.TempDir()
		writeFile(filepath.Join(cgroup, "cgroup.procs"), "")
		require.Equal(t, "no processes", describeCgroupTasks(cgroup, t.TempDir()))
	})

	t.Run("unreadable is not empty", func(t *testing.T) {
		got := describeCgroupTasks(filepath.Join(t.TempDir(), "gone"), t.TempDir())
		require.NotContains(t, got, "no processes")
		require.Contains(t, got, "unreadable")
	})

	t.Run("unreadable thread state and long stacks are reported", func(t *testing.T) {
		cgroup, procRoot := t.TempDir(), t.TempDir()
		writeFile(filepath.Join(cgroup, "cgroup.procs"), "7\n")
		writeFile(filepath.Join(procRoot, "7", "comm"), "sh\n")
		require.NoError(t, os.MkdirAll(filepath.Join(procRoot, "7", "task", "7"), 0o700))
		writeFile(filepath.Join(procRoot, "7", "task", "8", "stat"), "8 (sh) D 1\n")
		writeFile(filepath.Join(procRoot, "7", "task", "8", "stack"), strings.Repeat("[<0>] frame\n", 20))
		got := describeCgroupTasks(cgroup, procRoot)
		require.Contains(t, got, "thread 7 state unreadable")
		require.Equal(t, maxDescribedStackLines, strings.Count(got, "frame"))
		require.Contains(t, got, "frame <- ...]")
	})

	t.Run("cgroup walk is capped", func(t *testing.T) {
		cgroup := t.TempDir()
		dir := cgroup
		for range maxDescribedCgroups + 8 {
			writeFile(filepath.Join(dir, "cgroup.procs"), "")
			dir = filepath.Join(dir, "child")
		}
		require.Equal(t, "only the first 32 cgroups read", describeCgroupTasks(cgroup, t.TempDir()))
	})

	t.Run("process list is capped", func(t *testing.T) {
		cgroup, procRoot := t.TempDir(), t.TempDir()
		pids := ""
		for pid := 100; pid < 120; pid++ {
			pids += fmt.Sprintf("%d\n", pid)
			proc(procRoot, fmt.Sprint(pid), map[string]string{fmt.Sprint(pid): "S"})
		}
		writeFile(filepath.Join(cgroup, "cgroup.procs"), pids)
		got := describeCgroupTasks(cgroup, procRoot)
		require.Equal(t, maxDescribedProcs, strings.Count(got, "pid "))
		require.Contains(t, got, "only the first 8 processes shown")
	})
}

func TestDescribeUninterruptibleThreads(t *testing.T) {
	writeFile := func(path, data string) {
		t.Helper()
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
		require.NoError(t, os.WriteFile(path, []byte(data), 0o600))
	}
	self := filepath.Join(t.TempDir(), "self")
	writeFile(filepath.Join(self, "comm"), "dagger-engine\n")
	writeFile(filepath.Join(self, "task", "1", "stat"), "1 (dagger-engine) S 0\n")
	require.Equal(t, "pid self (dagger-engine), no uninterruptible threads", DescribeUninterruptibleThreads(self))

	writeFile(filepath.Join(self, "task", "2", "stat"), "2 (dagger-engine) D 0\n")
	writeFile(filepath.Join(self, "task", "2", "stack"), "[<0>] sync_inodes_sb+0xd4/0x2b0\n")
	require.Equal(t, "pid self (dagger-engine), thread 2 uninterruptible in [[<0>] sync_inodes_sb+0xd4/0x2b0]", DescribeUninterruptibleThreads(self))
}

func TestDescribeUninterruptibleThreadsClaimsAbsenceOnlyWhenComplete(t *testing.T) {
	writeFile := func(path, data string) {
		t.Helper()
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
		require.NoError(t, os.WriteFile(path, []byte(data), 0o600))
	}

	noComm := filepath.Join(t.TempDir(), "self")
	writeFile(filepath.Join(noComm, "task", "1", "stat"), "1 (x) S 0\n")
	require.Contains(t, DescribeUninterruptibleThreads(noComm), "no uninterruptible threads among those read")

	noTasks := filepath.Join(t.TempDir(), "self")
	writeFile(filepath.Join(noTasks, "comm"), "dagger-engine\n")
	require.Contains(t, DescribeUninterruptibleThreads(noTasks), "no uninterruptible threads among those read")

	truncated := filepath.Join(t.TempDir(), "self")
	writeFile(filepath.Join(truncated, "comm"), "dagger-engine\n")
	writeFile(filepath.Join(truncated, "task", "1", "stat"), "1 (dagger-engine) S 0\n")
	writeFile(filepath.Join(truncated, "task", "2", "stat"), "2 (dagger-engine) S 0\n")
	d := describeProc(truncated, "self", 1)
	require.False(t, d.complete)
	require.Contains(t, d.text, "only the first 1 threads read")
}

func TestRemoveExecCgroup(t *testing.T) {
	describe := func(path, _ string) string { return "described " + path }
	removeErr := func(err error) func(string) error {
		return func(path string) error { return &os.PathError{Op: "remove", Path: path, Err: err} }
	}

	require.NoError(t, removeExecCgroup("/sys/fs/cgroup/exec/x", func(string) error { return nil }, describe))
	require.NoError(t, removeExecCgroup("/sys/fs/cgroup/exec/x", removeErr(unix.ENOENT), describe))

	// A container cgroup that crun couldn't remove keeps its parent busy.
	err := removeExecCgroup("/sys/fs/cgroup/exec/x", removeErr(unix.EBUSY), describe)
	require.ErrorIs(t, err, unix.EBUSY)
	require.ErrorContains(t, err, "left in cgroup: described /sys/fs/cgroup/exec/x")

	err = removeExecCgroup("/sys/fs/cgroup/exec/x", removeErr(unix.EACCES), describe)
	require.ErrorIs(t, err, unix.EACCES)
	require.NotContains(t, err.Error(), "left in cgroup")
}
