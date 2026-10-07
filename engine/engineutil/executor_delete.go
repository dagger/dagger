package engineutil

import (
	"context"
	"errors"
	"fmt"
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
	return killAndDrainCgroup(ctx, path, cgroups.WriteFile, cgroups.ReadFile)
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
