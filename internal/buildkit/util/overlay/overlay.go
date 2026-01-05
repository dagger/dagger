package overlay

import (
	"path/filepath"
	"strings"

	"github.com/containerd/containerd/v2/core/mount"
)

// IsOverlayMountType returns true if the mount type is overlay-based
func IsOverlayMountType(mnt mount.Mount) bool {
	return mnt.Type == "overlay"
}

/*
We use the "volatile" overlayfs mount option to improve performance via less
fsync'ing: https://docs.kernel.org/filesystems/overlayfs.html#volatile-mount

This results in the kernel creating a `incompat` dir inside the overlay's
workdir when the mount is created. That directory is *not* removed when the
mount is unmounted. If the mount is remounted later and the incompat dir still
exists, the mount syscall will error out. This is the kernel's attempt to tell
us the mount might have not be fsync'd after use and thus have inconsistent data
(i.e. in the case of a hard machine crash).

However, we often mount+unmount the same overlay mount multiple times (e.g. cache
mounts, mounts after an errored exec, etc.). Thus we need to find these incompat
dirs and remove them by hand (as the kernel docs say we should).
*/

// VolatileIncompatDir returns the overlayfs incompat directory for a mount that
// uses the "volatile" option. An empty string means the mount doesn't use
// volatile overlayfs or doesn't include a workdir option.
func VolatileIncompatDir(mnt mount.Mount) string {
	if !IsOverlayMountType(mnt) {
		return ""
	}
	var hasVolatile bool
	var workDir string
	for _, opt := range mnt.Options {
		if opt == "volatile" {
			hasVolatile = true
			continue
		}
		if strings.HasPrefix(opt, "workdir=") {
			workDir = strings.TrimPrefix(opt, "workdir=")
			continue
		}
	}
	if !hasVolatile || workDir == "" {
		return ""
	}
	return filepath.Join(workDir, "work", "incompat")
}

// VolatileIncompatDirs returns de-duplicated incompat directories for mounts
// that use the "volatile" option.
func VolatileIncompatDirs(mounts []mount.Mount) []string {
	if len(mounts) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, 1)
	for _, m := range mounts {
		if dir := VolatileIncompatDir(m); dir != "" {
			if _, ok := seen[dir]; ok {
				continue
			}
			seen[dir] = struct{}{}
			out = append(out, dir)
		}
	}
	return out
}
