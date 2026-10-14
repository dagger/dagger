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

	runc "github.com/containerd/go-runc"
	"github.com/opencontainers/cgroups"
	cgroupmanager "github.com/opencontainers/cgroups/manager"
	"golang.org/x/sys/unix"
)

const containerDeleteRecoveryTimeout = 5 * time.Second

func deleteContainer(ctx context.Context, id string, unified bool, deleteFn func(context.Context, string, *runc.DeleteOpts) error, recoverFn func(context.Context) error) error {
	firstErr := deleteFn(context.WithoutCancel(ctx), id, &runc.DeleteOpts{})
	if !unified || !isBusyCgroupDelete(firstErr) {
		return firstErr
	}
	recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), containerDeleteRecoveryTimeout)
	defer cancel()
	if err := recoverFn(recoveryCtx); err != nil {
		return errors.Join(firstErr, fmt.Errorf("drain container %s cgroup: %w", id, err))
	}
	if err := deleteFn(recoveryCtx, id, &runc.DeleteOpts{Force: true}); err != nil {
		return errors.Join(firstErr, fmt.Errorf("delete container %s after cgroup recovery: %w", id, err))
	}
	return nil
}

func isBusyCgroupDelete(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "cgroup") &&
		strings.Contains(message, "rmdir") &&
		strings.Contains(message, "device or resource busy")
}

func killContainerCgroup(ctx context.Context, id, cgroupPath string) error {
	manager, err := cgroupmanager.New(&cgroups.Cgroup{Path: cgroupPath, Resources: &cgroups.Resources{}})
	if err != nil {
		return err
	}
	path := manager.GetPaths()[""]
	if !validContainerCgroupPath(id, path) {
		return fmt.Errorf("refusing cgroup recovery for container %q at %q", id, path)
	}
	err = killAndDrainCgroup(ctx, path, cgroups.WriteFile, cgroups.ReadFile)
	if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
		// The kill was written but the cgroup never emptied.
		return fmt.Errorf("%w (left in cgroup after kill: %s)", err, describeCgroupTasks(path, "/proc"))
	}
	return err
}

// removeExecCgroup removes an execution's accounting cgroup once its
// container is deleted. crun's delete only warns, into its log file, when it
// can't remove a container cgroup that still has tasks, and exits 0; the
// leftover child then makes this removal fail with EBUSY, so that is where
// the tasks still inside get named.
func removeExecCgroup(path string, remove func(string) error, describe func(path, procRoot string) string) error {
	err := remove(path)
	switch {
	case err == nil, errors.Is(err, os.ErrNotExist):
		return nil
	case errors.Is(err, unix.EBUSY):
		return fmt.Errorf("%w (left in cgroup: %s)", err, describe(path, "/proc"))
	default:
		return err
	}
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
// each such thread is reported with its kernel stack. Every list it reads is
// capped, and truncation and read failures are reported as such.
func describeCgroupTasks(path, procRoot string) string {
	var procs, notes []string
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
			continue
		}
		procs = append(procs, pids...)
		if len(procs) > maxDescribedProcs {
			notes = append(notes, fmt.Sprintf("only the first %d processes shown", maxDescribedProcs))
			procs = procs[:maxDescribedProcs]
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
	out := make([]string, 0, len(procs)+len(notes))
	if len(procs) == 0 && len(notes) == 0 {
		out = append(out, "no processes")
	}
	for _, pid := range procs {
		out = append(out, describeProc(filepath.Join(procRoot, pid), pid, maxDescribedTasksPerProc).text)
	}
	return strings.Join(append(out, notes...), "; ")
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

// Only accept an execution's container child. Matching an ID
// anywhere in the path could target the exec parent or an SSHFS helper.
func validContainerCgroupPath(id, path string) bool {
	if id == "" || id == "." || id == ".." || filepath.Base(id) != id ||
		!filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
		return false
	}
	execPath := filepath.Dir(path)
	return filepath.Base(path) == "container" && filepath.Base(execPath) == id &&
		filepath.Base(filepath.Dir(execPath)) == "exec"
}

// cgroup.kill is recursive, but signals complete asynchronously. The root's
// populated bit includes descendants, including children created after the kill.
func killAndDrainCgroup(ctx context.Context, path string, write func(string, string, string) error, read func(string, string) (string, error)) error {
	if err := write(path, "cgroup.kill", "1"); err != nil {
		return err
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		events, err := read(path, "cgroup.events")
		if err != nil {
			return err
		}
		populated := ""
		for _, line := range strings.Split(events, "\n") {
			if strings.HasPrefix(line, "populated ") {
				populated = strings.TrimPrefix(line, "populated ")
				break
			}
		}
		switch populated {
		case "0":
			return ctx.Err()
		case "1":
		default:
			return fmt.Errorf("invalid cgroup.events populated value: %q", populated)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
