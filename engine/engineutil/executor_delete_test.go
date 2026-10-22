package engineutil

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	resourcestypes "github.com/dagger/dagger/internal/buildkit/executor/resources/types"
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
	ops := func(remove func(string) error) cgroupOps {
		return cgroupOps{
			remove:    remove,
			readFile:  func(path string) (string, error) { return "", &os.PathError{Op: "read", Path: path, Err: unix.ENOENT} },
			writeFile: func(path, _ string) error { return &os.PathError{Op: "write", Path: path, Err: unix.ENOENT} },
			subdirs:   func(string) ([]string, error) { return nil, nil },
			describe:  describe,
		}
	}
	ctx := context.Background()

	require.NoError(t, removeExecCgroup(ctx, "/sys/fs/cgroup/exec/x", ops(func(string) error { return nil })))
	require.NoError(t, removeExecCgroup(ctx, "/sys/fs/cgroup/exec/x", ops(removeErr(unix.ENOENT))))

	// A cgroup the recovery can't kill stays busy, and its tasks are named.
	err := removeExecCgroup(ctx, "/sys/fs/cgroup/exec/x", ops(removeErr(unix.EBUSY)))
	require.ErrorIs(t, err, unix.EBUSY)
	require.ErrorContains(t, err, "recovery: kill: write /sys/fs/cgroup/exec/x/cgroup.kill")
	require.ErrorContains(t, err, "left in cgroup: described /sys/fs/cgroup/exec/x")

	err = removeExecCgroup(ctx, "/sys/fs/cgroup/exec/x", ops(removeErr(unix.EACCES)))
	require.ErrorIs(t, err, unix.EACCES)
	require.NotContains(t, err.Error(), "left in cgroup")
}

// fakeCgroupFS follows the kernel's cgroup v2 rules that matter here: rmdir
// fails with EBUSY while a cgroup has child cgroups or tasks, a threaded
// cgroup refuses cgroup.kill writes with EOPNOTSUPP, and cgroup.kill on a
// cgroup kills the tasks of its whole subtree, which then exit after a few
// polls of cgroup.events.
type fakeCgroupFS struct {
	dirs     map[string]bool
	threaded map[string]bool
	tasks    map[string]bool
	// stuck tasks don't exit when killed.
	stuck      bool
	killed     string
	exitPolls  int
	killWrites []string
}

func (f *fakeCgroupFS) inSubtree(path, root string) bool {
	return path == root || strings.HasPrefix(path, root+"/")
}

func (f *fakeCgroupFS) ops() cgroupOps {
	return cgroupOps{
		remove: func(path string) error {
			if !f.dirs[path] {
				return &os.PathError{Op: "remove", Path: path, Err: unix.ENOENT}
			}
			for dir := range f.dirs {
				if dir != path && f.inSubtree(dir, path) {
					return &os.PathError{Op: "remove", Path: path, Err: unix.EBUSY}
				}
			}
			if f.tasks[path] {
				return &os.PathError{Op: "remove", Path: path, Err: unix.EBUSY}
			}
			delete(f.dirs, path)
			return nil
		},
		writeFile: func(path, data string) error {
			dir, file := filepath.Split(path)
			dir = filepath.Clean(dir)
			if file != "cgroup.kill" || data != "1" || !f.dirs[dir] {
				return &os.PathError{Op: "write", Path: path, Err: unix.EINVAL}
			}
			if f.threaded[dir] {
				return &os.PathError{Op: "write", Path: path, Err: unix.EOPNOTSUPP}
			}
			f.killWrites = append(f.killWrites, dir)
			f.killed = dir
			f.exitPolls = 2
			return nil
		},
		readFile: func(path string) (string, error) {
			dir, file := filepath.Split(path)
			dir = filepath.Clean(dir)
			if file != "cgroup.events" || !f.dirs[dir] {
				return "", &os.PathError{Op: "read", Path: path, Err: unix.ENOENT}
			}
			if f.killed != "" && !f.stuck {
				if f.exitPolls > 0 {
					f.exitPolls--
				} else {
					for task := range f.tasks {
						if f.inSubtree(task, f.killed) {
							delete(f.tasks, task)
						}
					}
				}
			}
			populated := "0"
			for task := range f.tasks {
				if f.inSubtree(task, dir) {
					populated = "1"
				}
			}
			return "populated " + populated + "\nfrozen 0\n", nil
		},
		subdirs: func(path string) ([]string, error) {
			if !f.dirs[path] {
				return nil, &os.PathError{Op: "open", Path: path, Err: unix.ENOENT}
			}
			var names []string
			for dir := range f.dirs {
				if filepath.Dir(dir) == path {
					names = append(names, filepath.Base(dir))
				}
			}
			sort.Strings(names)
			return names, nil
		},
		describe: func(path, _ string) string { return "described " + path },
	}
}

// crunsLeftovers is what crun's delete leaves under an execution cgroup when
// the nested container runtime made its cgroups threaded: crun reads only
// cgroup.procs, which a threaded cgroup refuses, so it skips the busy
// threaded child and everything below it.
func crunsLeftovers() *fakeCgroupFS {
	const exec = "/sys/fs/cgroup/exec/x"
	return &fakeCgroupFS{
		dirs: map[string]bool{
			exec:                            true,
			exec + "/container":             true,
			exec + "/container/default":     true,
			exec + "/container/default/ctr": true,
		},
		threaded: map[string]bool{
			exec + "/container/default":     true,
			exec + "/container/default/ctr": true,
		},
		tasks: map[string]bool{exec + "/container/default/ctr": true},
	}
}

func TestRemoveExecCgroupRecoversBusyThreadedSubtree(t *testing.T) {
	fake := crunsLeftovers()
	require.NoError(t, removeExecCgroup(context.Background(), "/sys/fs/cgroup/exec/x", fake.ops()))
	require.Empty(t, fake.dirs, "the execution cgroup and everything below it are removed")
	// Killed through the execution cgroup, the top of what the execution
	// owns; the threaded cgroups below it refuse cgroup.kill.
	require.Equal(t, []string{"/sys/fs/cgroup/exec/x"}, fake.killWrites)
}

// fakeMountResources stands in for active mount accounting, whose Close
// removes only its own cgroup.
type fakeMountResources struct{ closed bool }

func (*fakeMountResources) Prepare(*exec.Cmd) (func() error, error) {
	return func() error { return nil }, nil
}
func (*fakeMountResources) Active() bool    { return true }
func (*fakeMountResources) Available() bool { return true }
func (*fakeMountResources) Sample() (*resourcestypes.NetworkSample, error) {
	return &resourcestypes.NetworkSample{}, nil
}
func (r *fakeMountResources) Close() error { r.closed = true; return nil }

func TestReleaseExecCgroupWithMountAccountingRecovers(t *testing.T) {
	fake := crunsLeftovers()
	mounts := &fakeMountResources{}
	require.NoError(t, releaseExecCgroup(context.Background(), mounts, "/exec/x", fake.ops()))
	require.True(t, mounts.closed)
	require.Empty(t, fake.dirs)

	// Systemd cgroup paths aren't touched.
	fake = crunsLeftovers()
	require.NoError(t, releaseExecCgroup(context.Background(), nil, "system.slice:dagger:x", fake.ops()))
	require.Len(t, fake.dirs, 4)
}

func TestRemoveExecCgroupKeepsFailureWhenTasksDontExit(t *testing.T) {
	timeout := execCgroupRecoveryTimeout
	execCgroupRecoveryTimeout = 50 * time.Millisecond
	t.Cleanup(func() { execCgroupRecoveryTimeout = timeout })

	fake := crunsLeftovers()
	fake.stuck = true
	err := removeExecCgroup(context.Background(), "/sys/fs/cgroup/exec/x", fake.ops())
	require.ErrorIs(t, err, unix.EBUSY)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorContains(t, err, "recovery: tasks still running after kill")
	require.ErrorContains(t, err, "left in cgroup: described /sys/fs/cgroup/exec/x")
	require.True(t, fake.dirs["/sys/fs/cgroup/exec/x/container/default/ctr"], "nothing is removed while tasks remain")
}

func TestDescribeCgroupTasksThreadedSubtree(t *testing.T) {
	writeFile := func(path, data string) {
		t.Helper()
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
		require.NoError(t, os.WriteFile(path, []byte(data), 0o600))
	}
	cgroup, procRoot := t.TempDir(), t.TempDir()
	writeFile(filepath.Join(cgroup, "cgroup.procs"), "")
	// An unreadable cgroup.procs stands in for a threaded cgroup's.
	require.NoError(t, os.MkdirAll(filepath.Join(cgroup, "default", "cgroup.procs"), 0o700))
	writeFile(filepath.Join(cgroup, "default", "cgroup.threads"), "77\n")
	writeFile(filepath.Join(procRoot, "77", "stat"), "77 (containerd) D 1\n")
	writeFile(filepath.Join(procRoot, "77", "stack"), "[<0>] do_exit+0x1/0x2\n")
	// The task below the unreadable cgroup is still found.
	writeFile(filepath.Join(cgroup, "default", "ctr", "cgroup.procs"), "42\n")
	writeFile(filepath.Join(procRoot, "42", "comm"), "runc\n")
	writeFile(filepath.Join(procRoot, "42", "task", "42", "stat"), "42 (runc) S 1\n")
	// A cgroup whose cgroup.procs and cgroup.threads are both unreadable.
	require.NoError(t, os.MkdirAll(filepath.Join(cgroup, "other", "cgroup.procs"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(cgroup, "other", "cgroup.threads"), 0o700))

	got := describeCgroupTasks(cgroup, procRoot)
	require.Contains(t, got, "thread 77 uninterruptible in [[<0>] do_exit+0x1/0x2]")
	require.Contains(t, got, filepath.Join(cgroup, "default")+" unreadable")
	require.Contains(t, got, "pid 42 (runc)", "it keeps descending past the unreadable cgroup")
	require.Contains(t, got, filepath.Join(cgroup, "other")+" threads unreadable")
}
