package engineutil

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	enginetel "github.com/dagger/dagger/engine/telemetry"
	"golang.org/x/sys/unix"
)

// cgroupOps are the cgroupfs operations removeExecCgroup uses; tests replace
// them.
type cgroupOps struct {
	remove    func(path string) error
	readFile  func(path string) (string, error)
	writeFile func(path, data string) error
	subdirs   func(path string) ([]string, error)
	describe  func(path, procRoot string) string
}

var hostCgroupOps = cgroupOps{
	remove: os.Remove,
	readFile: func(path string) (string, error) {
		data, err := os.ReadFile(path)
		return string(data), err
	},
	writeFile: func(path, data string) error {
		f, err := os.OpenFile(path, os.O_WRONLY, 0)
		if err != nil {
			return err
		}
		_, err = f.WriteString(data)
		return errors.Join(err, f.Close())
	},
	subdirs: func(path string) ([]string, error) {
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil, err
		}
		var dirs []string
		for _, entry := range entries {
			if entry.IsDir() {
				dirs = append(dirs, entry.Name())
			}
		}
		return dirs, nil
	},
	describe: describeCgroupTasks,
}

// execCgroupRecoveryTimeout is the deadline for killing, draining and
// removing what's left under a busy execution cgroup. It's checked between
// cgroupfs operations, which themselves can't be interrupted.
var execCgroupRecoveryTimeout = 5 * time.Second

// releaseExecCgroup closes the execution's mount accounting, which removes
// its own cgroup, then removes the execution cgroup. The runtime removes only
// the container's cgroup below it.
func releaseExecCgroup(ctx context.Context, mountResources enginetel.ExecMountResources, cgroupPath string, ops cgroupOps) error {
	var err error
	if mountResources != nil {
		err = mountResources.Close()
	}
	// Systemd cgroup paths aren't filesystem paths.
	if filepath.IsAbs(cgroupPath) && !strings.Contains(cgroupPath, ":") {
		err = errors.Join(err, removeExecCgroup(ctx, filepath.Join("/sys/fs/cgroup", cgroupPath), ops))
	}
	return err
}

// removeExecCgroup removes an execution's accounting cgroup once its
// container is deleted. crun's delete suppresses its error when it can't
// remove the container's cgroup, and still succeeds. To remove a busy child
// cgroup it reads the child's cgroup.procs, and skips the child when that
// fails, as it does for every cgroup of a threaded subtree, so a busy
// threaded subtree survives it and keeps this cgroup busy. On EBUSY the
// execution's whole subtree is killed through this cgroup, which unlike a
// threaded one accepts cgroup.kill, then drained and removed bottom-up. Only
// this cgroup and its descendants are touched. If that fails, for example
// because the tasks don't exit before the deadline, this cgroup refuses
// cgroup.kill, or cgroup.kill isn't supported (before Linux 5.14), the EBUSY
// error is kept, with the recovery's outcome and the tasks still inside.
func removeExecCgroup(ctx context.Context, path string, ops cgroupOps) error {
	err := ops.remove(path)
	switch {
	case err == nil, errors.Is(err, os.ErrNotExist):
		return nil
	case !errors.Is(err, unix.EBUSY):
		return err
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), execCgroupRecoveryTimeout)
	defer cancel()
	if recoverErr := killAndRemoveCgroup(ctx, path, ops); recoverErr != nil {
		return fmt.Errorf("%w (recovery: %w; left in cgroup: %s)", err, recoverErr, ops.describe(path, "/proc"))
	}
	return nil
}

// killAndRemoveCgroup kills every task under path, waits for them to exit,
// and removes path's cgroups bottom-up.
func killAndRemoveCgroup(ctx context.Context, path string, ops cgroupOps) error {
	if err := ops.writeFile(filepath.Join(path, "cgroup.kill"), "1"); err != nil {
		return fmt.Errorf("kill: %w", err)
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("tasks still running after kill: %w", err)
		}
		events, err := ops.readFile(filepath.Join(path, "cgroup.events"))
		if err != nil {
			return err
		}
		populated := ""
		for _, line := range strings.Split(events, "\n") {
			if value, ok := strings.CutPrefix(line, "populated "); ok {
				populated = value
				break
			}
		}
		if populated == "0" {
			break
		}
		if populated != "1" {
			return fmt.Errorf("invalid cgroup.events populated value: %q", populated)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("tasks still running after kill: %w", ctx.Err())
		case <-ticker.C:
		}
	}
	return removeCgroupTree(ctx, path, ops)
}

// removeCgroupTree removes path's descendant cgroups bottom-up, then path.
// They're empty by now, so removing them reads no cgroup.procs, which a
// threaded cgroup refuses.
func removeCgroupTree(ctx context.Context, path string, ops cgroupOps) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	children, err := ops.subdirs(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, child := range children {
		if err := removeCgroupTree(ctx, filepath.Join(path, child), ops); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := ops.remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

const (
	maxDescribedCgroups       = 32
	maxDescribedCgroupEntries = 1024
	maxDescribedProcs         = 8
	maxDescribedTasksPerProc  = 64
	maxDescribedOwnTasks      = 1024
	maxDescribedStackLines    = 12
)

// describeCgroupTasks names the processes left in a killed cgroup. A task
// outlives SIGKILL only while it sleeps uninterruptibly in the kernel, so
// each such thread is reported with its kernel stack. A threaded cgroup
// refuses cgroup.procs reads, so its threads are read from cgroup.threads
// instead. Every list it reads is capped, and truncation and read failures
// are reported as such.
func describeCgroupTasks(path, procRoot string) string {
	var procs, threads, notes []string
	// Breadth-first, reading each directory a bounded batch at a time, so
	// neither the number of cgroups nor their fan-out is unbounded.
	queue := []string{path}
	entryBudget := maxDescribedCgroupEntries
	for cgroups := 0; len(queue) > 0; cgroups++ {
		if cgroups == maxDescribedCgroups {
			notes = append(notes, fmt.Sprintf("only the first %d cgroups read", cgroups))
			break
		}
		dir := queue[0]
		queue = queue[1:]
		pids, err := readCgroupProcs(filepath.Join(dir, "cgroup.procs"))
		if err != nil {
			notes = append(notes, fmt.Sprintf("%s unreadable: %v", dir, err))
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			// Keep descending: the cgroup's children may hold its tasks.
			tids, err := readCgroupProcs(filepath.Join(dir, "cgroup.threads"))
			if err != nil {
				notes = append(notes, fmt.Sprintf("%s threads unreadable: %v", dir, err))
			}
			threads = append(threads, tids...)
		}
		procs = append(procs, pids...)
		if len(procs) > maxDescribedProcs {
			notes = append(notes, fmt.Sprintf("only the first %d processes shown", maxDescribedProcs))
			procs = procs[:maxDescribedProcs]
			break
		}
		if len(threads) > maxDescribedProcs {
			notes = append(notes, fmt.Sprintf("only the first %d threads shown", maxDescribedProcs))
			threads = threads[:maxDescribedProcs]
			break
		}
		if entryBudget == 0 {
			continue
		}
		entries, err := readDirHead(dir, entryBudget)
		if err != nil {
			notes = append(notes, fmt.Sprintf("%s unreadable: %v", dir, err))
			continue
		}
		entryBudget -= len(entries)
		if entryBudget == 0 {
			notes = append(notes, fmt.Sprintf("only the first %d cgroup entries listed", maxDescribedCgroupEntries))
		}
		for _, entry := range entries {
			if entry.IsDir() {
				queue = append(queue, filepath.Join(dir, entry.Name()))
			}
		}
	}
	out := make([]string, 0, len(procs)+len(threads)+len(notes))
	if len(procs) == 0 && len(threads) == 0 && len(notes) == 0 {
		out = append(out, "no processes")
	}
	for _, pid := range procs {
		out = append(out, describeProc(filepath.Join(procRoot, pid), pid, maxDescribedTasksPerProc).text)
	}
	for _, tid := range threads {
		out = append(out, describeThread(filepath.Join(procRoot, tid), tid))
	}
	return strings.Join(append(out, notes...), "; ")
}

// describeThread names a thread from a threaded cgroup's cgroup.threads,
// with its kernel stack if it sleeps uninterruptibly.
func describeThread(taskDir, tid string) string {
	state, err := procTaskState(taskDir)
	if err != nil {
		return fmt.Sprintf("thread %s (state unreadable: %v)", tid, err)
	}
	if state != "D" {
		return fmt.Sprintf("thread %s (state %s)", tid, state)
	}
	stack, err := os.ReadFile(filepath.Join(taskDir, "stack"))
	if err != nil {
		return fmt.Sprintf("thread %s uninterruptible (stack unreadable: %v)", tid, err)
	}
	lines := strings.Split(strings.TrimSpace(string(stack)), "\n")
	if len(lines) > maxDescribedStackLines {
		lines = append(lines[:maxDescribedStackLines], "...")
	}
	return fmt.Sprintf("thread %s uninterruptible in [%s]", tid, strings.Join(lines, " <- "))
}

// DescribeUninterruptibleThreads reports the threads of the process at
// procDir (such as /proc/self) that sleep uninterruptibly in the kernel, each
// with its kernel stack. Absence is claimed only for a complete read.
func DescribeUninterruptibleThreads(procDir string) string {
	d := describeProc(procDir, filepath.Base(procDir), maxDescribedOwnTasks)
	switch {
	case d.uninterruptible > 0:
	case d.complete:
		d.text += ", no uninterruptible threads"
	default:
		d.text += ", no uninterruptible threads among those read"
	}
	return d.text
}

type procDescription struct {
	text string
	// uninterruptible counts the threads seen in state D.
	uninterruptible int
	// complete is false when anything was unreadable or truncated.
	complete bool
}

func describeProc(procDir, pid string, maxTasks int) procDescription {
	comm, err := os.ReadFile(filepath.Join(procDir, "comm"))
	if err != nil {
		return procDescription{text: fmt.Sprintf("pid %s (unreadable: %v)", pid, err)}
	}
	d := procDescription{text: fmt.Sprintf("pid %s (%s)", pid, strings.TrimSpace(string(comm))), complete: true}
	tasks, err := readDirHead(filepath.Join(procDir, "task"), maxTasks+1)
	if err != nil {
		d.text += fmt.Sprintf(", threads unreadable: %v", err)
		d.complete = false
		return d
	}
	if len(tasks) > maxTasks {
		d.text += fmt.Sprintf(", only the first %d threads read", maxTasks)
		d.complete = false
		tasks = tasks[:maxTasks]
	}
	for _, task := range tasks {
		taskDir := filepath.Join(procDir, "task", task.Name())
		state, err := procTaskState(taskDir)
		if err != nil {
			d.text += fmt.Sprintf(", thread %s state unreadable: %v", task.Name(), err)
			d.complete = false
			continue
		}
		if state != "D" {
			continue
		}
		d.uninterruptible++
		stack, err := os.ReadFile(filepath.Join(taskDir, "stack"))
		if err != nil {
			d.text += fmt.Sprintf(", thread %s uninterruptible (stack unreadable: %v)", task.Name(), err)
			continue
		}
		lines := strings.Split(strings.TrimSpace(string(stack)), "\n")
		if len(lines) > maxDescribedStackLines {
			lines = append(lines[:maxDescribedStackLines], "...")
		}
		d.text += fmt.Sprintf(", thread %s uninterruptible in [%s]", task.Name(), strings.Join(lines, " <- "))
	}
	return d
}

// readCgroupProcs reads the PIDs from a cgroup.procs file, at most enough
// for one more than the described processes.
func readCgroupProcs(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var pids []string
	scanner := bufio.NewScanner(f)
	for len(pids) <= maxDescribedProcs && scanner.Scan() {
		if pid := strings.TrimSpace(scanner.Text()); pid != "" {
			pids = append(pids, pid)
		}
	}
	return pids, scanner.Err()
}

// readDirHead reads at most n entries of a directory.
func readDirHead(path string, n int) ([]fs.DirEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	entries, err := f.ReadDir(n)
	if errors.Is(err, io.EOF) {
		err = nil
	}
	return entries, err
}

// procTaskState returns the one-letter state from a /proc task's stat. The
// command name is parenthesized and may contain spaces, so parse after it.
func procTaskState(taskDir string) (string, error) {
	stat, err := os.ReadFile(filepath.Join(taskDir, "stat"))
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(stat[bytes.LastIndexByte(stat, ')')+1:]))
	if len(fields) == 0 {
		return "", fmt.Errorf("malformed %s", filepath.Join(taskDir, "stat"))
	}
	return fields[0], nil
}
