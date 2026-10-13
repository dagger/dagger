package server

import (
	"fmt"
	"log/slog"

	"github.com/moby/sys/mountinfo"
)

// logStateFilesystem records which filesystem holds the engine state. A
// filesystem-wide flush, or a journal commit for an fsync, waits for every
// other writer on that filesystem, so whether it is shared explains slow
// syncs.
func logStateFilesystem(path string) {
	mounts, err := mountinfo.GetMounts(mountinfo.ParentsFilter(path))
	if err != nil || len(mounts) == 0 {
		slog.Warn("engine state filesystem unknown", "path", path, "error", err)
		return
	}
	m := mounts[0]
	for _, candidate := range mounts[1:] {
		if len(candidate.Mountpoint) > len(m.Mountpoint) {
			m = candidate
		}
	}
	slog.Info("engine state filesystem", "path", path, "mountpoint", m.Mountpoint,
		"fstype", m.FSType, "source", m.Source, "device", fmt.Sprintf("%d:%d", m.Major, m.Minor),
		"root", m.Root, "options", m.Options, "superOptions", m.VFSOptions)
}
