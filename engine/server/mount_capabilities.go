package server

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"golang.org/x/sys/unix"
)

type recursiveReadOnlyProbeOps struct {
	mount        func(source, target, fstype string, flags uintptr, data string) error
	mountSetattr func(dirfd int, path string, flags uint, attr *unix.MountAttr) error
	unmount      func(target string, flags int) error
}

var systemRecursiveReadOnlyProbeOps = recursiveReadOnlyProbeOps{
	mount:        unix.Mount,
	mountSetattr: unix.MountSetattr,
	unmount:      unix.Unmount,
}

// probeRecursiveReadOnlyMounts tests mount_setattr(AT_RECURSIVE) once in a
// private mount namespace. The locked OS thread is intentionally not unlocked:
// when its goroutine exits, Go terminates the thread and the kernel tears down
// the whole probe namespace even if a best-effort unmount failed.
func probeRecursiveReadOnlyMounts() (bool, error) {
	probeRoot, err := os.MkdirTemp("", "dagger-rro-probe-")
	if err != nil {
		return false, fmt.Errorf("create recursive read-only probe directory: %w", err)
	}
	defer os.RemoveAll(probeRoot)

	result := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		if err := unix.Unshare(unix.CLONE_NEWNS); err != nil {
			result <- fmt.Errorf("unshare recursive read-only probe mount namespace: %w", err)
			return
		}
		if err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""); err != nil {
			result <- fmt.Errorf("make recursive read-only probe namespace private: %w", err)
			return
		}
		result <- runRecursiveReadOnlyProbe(probeRoot, systemRecursiveReadOnlyProbeOps)
	}()

	err = <-result
	return err == nil, err
}

func runRecursiveReadOnlyProbe(root string, ops recursiveReadOnlyProbeOps) error {
	source := filepath.Join(root, "source")
	nested := filepath.Join(source, "nested")
	target := filepath.Join(root, "target")
	for _, dir := range []string{source, target} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			return fmt.Errorf("create recursive read-only probe path %q: %w", dir, err)
		}
	}

	if err := ops.mount("tmpfs", source, "tmpfs", 0, "mode=0755"); err != nil {
		return fmt.Errorf("mount recursive read-only probe source: %w", err)
	}
	defer ops.unmount(source, unix.MNT_DETACH)

	// Create the child mountpoint inside the now-mounted source tmpfs before
	// mounting the child filesystem that proves the operation is recursive.
	if err := os.Mkdir(nested, 0o755); err != nil {
		return fmt.Errorf("create recursive read-only probe nested mountpoint: %w", err)
	}
	if err := ops.mount("tmpfs", nested, "tmpfs", 0, "mode=0755"); err != nil {
		return fmt.Errorf("mount recursive read-only probe nested source: %w", err)
	}
	defer ops.unmount(nested, unix.MNT_DETACH)

	if err := ops.mount(source, target, "", unix.MS_BIND|unix.MS_REC, ""); err != nil {
		return fmt.Errorf("recursive bind probe source: %w", err)
	}
	defer ops.unmount(target, unix.MNT_DETACH)

	if err := ops.mountSetattr(unix.AT_FDCWD, target, unix.AT_RECURSIVE, &unix.MountAttr{
		Attr_set: unix.MOUNT_ATTR_RDONLY,
	}); err != nil {
		return fmt.Errorf("apply recursive read-only mount attribute: %w", err)
	}
	return nil
}
