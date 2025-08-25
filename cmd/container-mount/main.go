package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

func usage(prog string) {
	fmt.Fprintf(os.Stderr, "Usage: %s <container-pid> <source-path> <target-path-in-container>\n", prog)
	fmt.Fprintf(os.Stderr, "\nExample:\n")
	fmt.Fprintf(os.Stderr, "  %s $(runc state mycontainer | jq -r .pid) /host/directory /container/mount/point\n", prog)
	os.Exit(1)
}

// Linux syscall constants that may not be available in older golang.org/x/sys/unix versions
const (
	OPEN_TREE_CLONE   = 0x01
	OPEN_TREE_CLOEXEC = 0x80000

	MOVE_MOUNT_F_EMPTY_PATH = 0x04

	MNT_DETACH = 0x02
)

// openTree wraps the open_tree syscall
func openTree(dirfd int, pathname string, flags uint) (int, error) {
	pathBytes, err := syscall.BytePtrFromString(pathname)
	if err != nil {
		return -1, err
	}
	fd, _, errno := syscall.Syscall(unix.SYS_OPEN_TREE, uintptr(dirfd), uintptr(unsafe.Pointer(pathBytes)), uintptr(flags))
	if errno != 0 {
		return -1, errno
	}
	return int(fd), nil
}

// moveMount wraps the move_mount syscall
func moveMount(fromDirfd int, fromPathname string, toDirfd int, toPathname string, flags uint) error {
	// Always convert strings to byte pointers, even if they're empty
	fromPath, err := syscall.BytePtrFromString(fromPathname)
	if err != nil {
		return err
	}

	toPath, err := syscall.BytePtrFromString(toPathname)
	if err != nil {
		return err
	}

	_, _, errno := syscall.Syscall6(unix.SYS_MOVE_MOUNT,
		uintptr(fromDirfd), uintptr(unsafe.Pointer(fromPath)),
		uintptr(toDirfd), uintptr(unsafe.Pointer(toPath)),
		uintptr(flags), 0)
	if errno != 0 {
		return errno
	}
	return nil
}

func main() {
	if len(os.Args) != 4 {
		usage(os.Args[0])
	}

	os.Setenv("DEBUG", "1")

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	containerPidStr := os.Args[1]
	sourcePath := os.Args[2]
	targetPath := os.Args[3]

	containerPid, err := strconv.Atoi(containerPidStr)
	if err != nil || containerPid <= 0 {
		fmt.Fprintf(os.Stderr, "Error: Invalid container PID: %s\n", containerPidStr)
		os.Exit(1)
	}

	if err := unix.Unshare(unix.CLONE_NEWNS); err != nil {
		panic(err)
	}

	// Step 1: Create a detached clone of the source mount
	if os.Getenv("DEBUG") != "" {
		fmt.Printf("Creating detached mount of %s...\n", sourcePath)
	}

	fdMnt, err := openTree(unix.AT_FDCWD, sourcePath, OPEN_TREE_CLONE|OPEN_TREE_CLOEXEC)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open_tree failed: %v\n", err)
		os.Exit(1)
	}
	defer unix.Close(fdMnt)

	if os.Getenv("DEBUG") != "" {
		fmt.Printf("Created detached mount, fd=%d\n", fdMnt)
	}

	// Step 2: Get file descriptors for the container's namespaces
	// Mount namespace
	mntNsPath := fmt.Sprintf("/proc/%d/ns/mnt", containerPid)
	fdMntNs, err := unix.Open(mntNsPath, unix.O_RDONLY, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open mount namespace: %v\n", err)
		os.Exit(1)
	}
	defer unix.Close(fdMntNs)

	if os.Getenv("DEBUG") != "" {
		fmt.Printf("Entering mount namespace of PID %d...\n", containerPid)
	}

	err = unix.Setns(fdMntNs, unix.CLONE_NEWNS)
	if err != nil {
		fmt.Fprintf(os.Stderr, "setns(CLONE_NEWNS) failed: %v\n", err)
		os.Exit(1)
	}

	// Step 3: Create target directory if it doesn't exist
	if _, err := os.Stat(targetPath); os.IsNotExist(err) {
		if os.Getenv("DEBUG") != "" {
			fmt.Printf("Creating target directory %s...\n", targetPath)
		}
		err = os.MkdirAll(targetPath, 0755)
		if err != nil && !os.IsExist(err) {
			fmt.Fprintf(os.Stderr, "mkdir failed: %v\n", err)
			// Continue anyway, might already exist
		}
	}

	// Step 4: Unmount any existing mount at the target path
	if os.Getenv("DEBUG") != "" {
		fmt.Printf("Unmounting any existing mount at %s...\n", targetPath)
	}
	err = unix.Unmount(targetPath, MNT_DETACH)
	if err != nil && err != unix.EINVAL && err != unix.ENOENT {
		fmt.Fprintf(os.Stderr, "umount2 failed: %v\n", err)
		// Continue anyway, might not be mounted
	}

	// Step 5: Attach the detached mount to the target path in the container
	if os.Getenv("DEBUG") != "" {
		fmt.Printf("Attaching mount to %s in container...\n", targetPath)
	}

	err = moveMount(fdMnt, "", unix.AT_FDCWD, targetPath, MOVE_MOUNT_F_EMPTY_PATH)
	if err != nil {
		fmt.Fprintf(os.Stderr, "move_mount failed: %v\n", err)
		os.Exit(1)
	}

	if os.Getenv("DEBUG") != "" {
		fmt.Printf("Successfully mounted %s to %s in container PID %d\n",
			sourcePath, targetPath, containerPid)
	}
}
