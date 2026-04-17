package fsdiff

import (
	"path/filepath"
	"strings"

	"github.com/containerd/containerd/v2/core/mount"
)

func IsOverlayMountType(mnt mount.Mount) bool {
	return mnt.Type == "overlay"
}

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
