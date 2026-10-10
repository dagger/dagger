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

const (
	maxDescribedCgroups      = 32
	maxDescribedProcs        = 8
	maxDescribedTasksPerProc = 64
	maxDescribedStackLines   = 12
)

// describeCgroupTasks names the processes left in a killed cgroup. A task
// outlives SIGKILL only while it sleeps uninterruptibly in the kernel, so
// each such thread is reported with its kernel stack. Every list it reads is
// capped, and truncation and read failures are reported as such.
func describeCgroupTasks(path, procRoot string) string {
	var procs, notes []string
	cgroupsSeen := 0
	walkErr := filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if cgroupsSeen == maxDescribedCgroups {
			notes = append(notes, fmt.Sprintf("only the first %d cgroups read", maxDescribedCgroups))
			return fs.SkipAll
		}
		cgroupsSeen++
		pids, err := readCgroupProcs(filepath.Join(p, "cgroup.procs"))
		if err != nil {
			notes = append(notes, fmt.Sprintf("%s unreadable: %v", p, err))
			return nil
		}
		procs = append(procs, pids...)
		if len(procs) > maxDescribedProcs {
			notes = append(notes, fmt.Sprintf("only the first %d processes shown", maxDescribedProcs))
			procs = procs[:maxDescribedProcs]
			return fs.SkipAll
		}
		return nil
	})
	if walkErr != nil {
		notes = append(notes, fmt.Sprintf("cgroup walk failed: %v", walkErr))
	}
	out := make([]string, 0, len(procs)+len(notes))
	if len(procs) == 0 && len(notes) == 0 {
		out = append(out, "no processes")
	}
	for _, pid := range procs {
		out = append(out, describeProc(filepath.Join(procRoot, pid), pid))
	}
	return strings.Join(append(out, notes...), "; ")
}

func describeProc(procDir, pid string) string {
	comm, err := os.ReadFile(filepath.Join(procDir, "comm"))
	if err != nil {
		return fmt.Sprintf("pid %s (unreadable: %v)", pid, err)
	}
	desc := fmt.Sprintf("pid %s (%s)", pid, strings.TrimSpace(string(comm)))
	tasks, err := readDirHead(filepath.Join(procDir, "task"), maxDescribedTasksPerProc+1)
	if err != nil {
		return desc + fmt.Sprintf(", threads unreadable: %v", err)
	}
	if len(tasks) > maxDescribedTasksPerProc {
		desc += fmt.Sprintf(", only the first %d threads read", maxDescribedTasksPerProc)
		tasks = tasks[:maxDescribedTasksPerProc]
	}
	for _, task := range tasks {
		taskDir := filepath.Join(procDir, "task", task.Name())
		if procTaskState(taskDir) != "D" {
			continue
		}
		stack, err := os.ReadFile(filepath.Join(taskDir, "stack"))
		if err != nil {
			desc += fmt.Sprintf(", thread %s uninterruptible (stack unreadable: %v)", task.Name(), err)
			continue
		}
		lines := strings.Split(strings.TrimSpace(string(stack)), "\n")
		if len(lines) > maxDescribedStackLines {
			lines = append(lines[:maxDescribedStackLines], "...")
		}
		desc += fmt.Sprintf(", thread %s uninterruptible in [%s]", task.Name(), strings.Join(lines, " <- "))
	}
	return desc
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
func procTaskState(taskDir string) string {
	stat, err := os.ReadFile(filepath.Join(taskDir, "stat"))
	if err != nil {
		return ""
	}
	fields := strings.Fields(string(stat[bytes.LastIndexByte(stat, ')')+1:]))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
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
