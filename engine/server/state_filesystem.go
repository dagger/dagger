package server

import (
	"bufio"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"github.com/moby/sys/mountinfo"
)

// logStateFilesystem records which filesystem holds the engine state. A
// filesystem-wide flush waits for every writer on that filesystem, and an
// fsync can wait on journal and device work shared with them, so whether it
// is shared helps explain slow syncs.
func logStateFilesystem(path string) {
	m, err := stateMount(path)
	if err != nil {
		slog.Warn("engine state filesystem unknown", "path", path, "error", err)
		return
	}
	slog.Info("engine state filesystem", "path", path, "mountpoint", m.Mountpoint,
		"fstype", m.FSType, "source", m.Source, "device", fmt.Sprintf("%d:%d", m.Major, m.Minor),
		"root", m.Root, "options", m.Options, "superOptions", m.VFSOptions)
}

// stateMount asks the kernel which mount an open path is on, rather than
// guessing from mountpoint strings, which can't see overmounts.
func stateMount(path string) (*mountinfo.Info, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fdinfo, err := os.Open(fmt.Sprintf("/proc/self/fdinfo/%d", f.Fd()))
	if err != nil {
		return nil, err
	}
	defer fdinfo.Close()
	id, err := parseFdinfoMountID(fdinfo)
	if err != nil {
		return nil, err
	}
	mounts, err := mountinfo.GetMounts(func(m *mountinfo.Info) (skip, stop bool) {
		return m.ID != id, m.ID == id
	})
	if err != nil {
		return nil, err
	}
	if len(mounts) == 0 {
		return nil, fmt.Errorf("mount %d not in mountinfo", id)
	}
	return mounts[0], nil
}

// parseFdinfoMountID reads the mnt_id field of a /proc/<pid>/fdinfo/<fd> file.
func parseFdinfoMountID(fdinfo *os.File) (int, error) {
	scanner := bufio.NewScanner(fdinfo)
	for scanner.Scan() {
		if value, ok := strings.CutPrefix(scanner.Text(), "mnt_id:"); ok {
			return strconv.Atoi(strings.TrimSpace(value))
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	return 0, fmt.Errorf("no mnt_id in %s", fdinfo.Name())
}
