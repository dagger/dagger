//go:build !windows

package snapshots

import (
	"os"
	"syscall"
)

// copyDirMetadata gives the directory dst src's owner, mode and modification
// time.
func copyDirMetadata(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		if err := os.Lchown(dst, int(stat.Uid), int(stat.Gid)); err != nil {
			return err
		}
	}
	if err := os.Chmod(dst, info.Mode()&(os.ModePerm|os.ModeSetuid|os.ModeSetgid|os.ModeSticky)); err != nil {
		return err
	}
	return os.Chtimes(dst, info.ModTime(), info.ModTime())
}
