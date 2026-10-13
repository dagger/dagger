package engineutil

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	runc "github.com/containerd/go-runc"
	"github.com/stretchr/testify/require"
)

var errBusyCgroup = errors.New("unable to remove container's cgroup: rmdir /sys/fs/cgroup/buildkit/test/init: device or resource busy")

func TestValidContainerCgroupPath(t *testing.T) {
	for _, tc := range []struct {
		id, path string
		valid    bool
	}{
		{"run-id", "/sys/fs/cgroup/exec/run-id", false},
		{"run-id", "/sys/fs/cgroup/tenant/exec/run-id/container", true},
		{"container", "/sys/fs/cgroup/exec/container", false},
		{"container", "/sys/fs/cgroup/exec/container/container", true},
		{"run-id", "/sys/fs/cgroup/exec/run-id/sshfs/mount-id", false},
		{"run-id", "/sys/fs/cgroup/exec/run-id/sshfs/container", false},
		{"run-id", "/sys/fs/cgroup/exec/run-id/container/child", false},
		{"run-id", "/sys/fs/cgroup/exec/other/container", false},
		{"run-id", "/sys/fs/cgroup/not-exec/run-id/container", false},
		{"run-id", "/sys/fs/cgroup/exec/prefix-run-id", false},
		{"run-id", "/sys/fs/cgroup/exec/run-id/../container", false},
		{"run-id", "exec/run-id/container", false},
		{"../run-id", "/sys/fs/cgroup/exec/run-id/container", false},
		{"", "/", false},
	} {
		t.Run(tc.path+":"+tc.id, func(t *testing.T) {
			require.Equal(t, tc.valid, validContainerCgroupPath(tc.id, tc.path))
		})
	}
}

func TestDeleteContainerUnifiedDrain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		root := t.TempDir()
		write := func(dir, file, value string) error {
			return os.WriteFile(filepath.Join(dir, file), []byte(value), 0600)
		}
		read := func(dir, file string) (string, error) {
			b, err := os.ReadFile(filepath.Join(dir, file))
			return string(b), err
		}
		require.NoError(t, write(root, "cgroup.procs", ""))
		require.NoError(t, write(root, "cgroup.events", "populated 1\nfrozen 0\n"))
		calls := 0
		result := make(chan error, 1)
		go func() {
			result <- deleteContainer(t.Context(), "test", true, func(_ context.Context, _ string, opts *runc.DeleteOpts) error {
				calls++
				if calls == 1 {
					return errBusyCgroup
				}
				if !opts.Force {
					return errors.New("retry must be forced")
				}
				events, err := read(root, "cgroup.events")
				if err != nil {
					return err
				}
				if events != "populated 0\n" {
					return fmt.Errorf("delete before descendant drain: %q", events)
				}
				return nil
			}, func(ctx context.Context) error {
				return killAndDrainCgroup(ctx, root, func(dir, file, value string) error {
					if err := write(dir, file, value); err != nil {
						return err
					}
					// The fake kernel still has a late descendant after the recursive
					// kill request. Empty root procs must not permit an early delete.
					child := filepath.Join(root, "init")
					if err := os.Mkdir(child, 0700); err != nil {
						return err
					}
					return write(child, "cgroup.procs", "123\n")
				}, read)
			})
		}()
		synctest.Wait()
		require.Equal(t, 1, calls, "delete must wait for the late child to drain")
		kill, err := read(root, "cgroup.kill")
		require.NoError(t, err)
		require.Equal(t, "1", kill)
		require.NoError(t, write(filepath.Join(root, "init"), "cgroup.procs", ""))
		require.NoError(t, write(root, "cgroup.events", "populated 0\n"))
		require.NoError(t, <-result)
		require.Equal(t, 2, calls)
	})
}

func TestDeleteContainerRecoveryLimits(t *testing.T) {
	for _, tc := range []struct {
		name    string
		unified bool
		first   error
	}{
		{"normal delete", true, nil},
		{"unrelated failure", true, errors.New("runtime unavailable")},
		{"legacy or hybrid", false, errBusyCgroup},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			err := deleteContainer(t.Context(), "test", tc.unified, func(ctx context.Context, _ string, opts *runc.DeleteOpts) error {
				calls++
				require.False(t, opts.Force)
				require.NoError(t, ctx.Err())
				return tc.first
			}, func(context.Context) error { t.Fatal("unexpected recovery"); return nil })
			require.True(t, err == tc.first, "ordinary or unsupported deletion must preserve the original error")
			require.Equal(t, 1, calls)
		})
	}
	for _, mode := range []string{"kill error", "events read error", "invalid events", "never drains", "retry still busy"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cause := errors.New(mode)
				calls := 0
				started := time.Now()
				err := deleteContainer(t.Context(), "test", true, func(ctx context.Context, _ string, opts *runc.DeleteOpts) error {
					calls++
					if calls == 1 {
						return errBusyCgroup
					}
					require.True(t, opts.Force)
					require.NoError(t, ctx.Err())
					return cause
				}, func(ctx context.Context) error {
					return killAndDrainCgroup(ctx, "unused", func(string, string, string) error {
						if mode == "kill error" {
							return cause
						}
						return nil
					}, func(string, string) (string, error) {
						switch mode {
						case "events read error":
							return "", cause
						case "invalid events":
							return "frozen 0\n", nil
						case "never drains":
							return "populated 1\n", nil
						default:
							return "populated 0\n", nil
						}
					})
				})
				require.ErrorIs(t, err, errBusyCgroup)
				if mode == "never drains" {
					require.ErrorIs(t, err, context.DeadlineExceeded)
					require.Equal(t, containerDeleteRecoveryTimeout, time.Since(started))
				}
				if mode == "kill error" || mode == "events read error" || mode == "retry still busy" {
					require.ErrorIs(t, err, cause)
				}
				if mode == "retry still busy" {
					require.Equal(t, 2, calls)
				} else {
					require.Equal(t, 1, calls)
				}
			})
		})
	}
}

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
