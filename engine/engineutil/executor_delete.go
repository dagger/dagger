package engineutil

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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
	if err := killAndDrainCgroup(ctx, path, cgroups.WriteFile, cgroups.ReadFile); err != nil {
		return fmt.Errorf("%w (still in cgroup: %s)", err, describeCgroupTasks(path))
	}
	return nil
}

// describeCgroupTasks names what survived a cgroup kill: a task only
// outlives SIGKILL while it sleeps uninterruptibly in the kernel, so each
// such thread is reported with its kernel stack.
func describeCgroupTasks(path string) string {
	const maxProcs, maxStackLines = 8, 12
	var procs []string
	_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "cgroup.procs" {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		procs = append(procs, strings.Fields(string(data))...)
		return nil
	})
	if len(procs) == 0 {
		return "no processes"
	}
	var out []string
	for i, pid := range procs {
		if i == maxProcs {
			out = append(out, fmt.Sprintf("%d more", len(procs)-maxProcs))
			break
		}
		comm, _ := os.ReadFile(filepath.Join("/proc", pid, "comm"))
		desc := fmt.Sprintf("pid %s (%s)", pid, strings.TrimSpace(string(comm)))
		tids, _ := os.ReadDir(filepath.Join("/proc", pid, "task"))
		for _, tid := range tids {
			taskDir := filepath.Join("/proc", pid, "task", tid.Name())
			if procTaskState(taskDir) != "D" {
				continue
			}
			stack, _ := os.ReadFile(filepath.Join(taskDir, "stack"))
			lines := strings.Split(strings.TrimSpace(string(stack)), "\n")
			if len(lines) > maxStackLines {
				lines = lines[:maxStackLines]
			}
			desc += fmt.Sprintf("; thread %s uninterruptible in [%s]", tid.Name(), strings.Join(lines, " <- "))
		}
		out = append(out, desc)
	}
	return strings.Join(out, ", ")
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
