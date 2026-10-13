package server

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/moby/sys/mountinfo"
)

// logStateFilesystem records which filesystem holds the engine state. A
// filesystem-wide flush waits for every writer on that filesystem, and an
// fsync can wait on journal and device work shared with them, so whether it
// is shared helps explain slow syncs.
func logStateFilesystem(path string) {
	mounts, err := mountinfo.GetMounts(nil)
	if err != nil {
		slog.Warn("engine state filesystem unknown", "path", path, "error", err)
		return
	}
	m := containingMount(mounts, path)
	if m == nil {
		slog.Warn("engine state filesystem unknown", "path", path, "error", "no mount contains it")
		return
	}
	slog.Info("engine state filesystem", "path", path, "mountpoint", m.Mountpoint,
		"fstype", m.FSType, "source", m.Source, "device", fmt.Sprintf("%d:%d", m.Major, m.Minor),
		"root", m.Root, "options", m.Options, "superOptions", m.VFSOptions)
}

// containingMount returns the deepest mount whose mountpoint is path or one of
// its ancestors, comparing whole path components. When several mounts share
// a mountpoint, the last one listed is the one on top.
func containingMount(mounts []*mountinfo.Info, path string) *mountinfo.Info {
	path = filepath.Clean(path)
	var best *mountinfo.Info
	for _, m := range mounts {
		mp := filepath.Clean(m.Mountpoint)
		if mp != "/" && path != mp && !strings.HasPrefix(path, mp+"/") {
			continue
		}
		if best == nil || len(mp) >= len(filepath.Clean(best.Mountpoint)) {
			best = m
		}
	}
	return best
}
