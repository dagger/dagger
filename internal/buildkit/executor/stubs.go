package executor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"

	"github.com/dagger/dagger/internal/buildkit/util/bklog"
	"github.com/dagger/dagger/internal/buildkit/util/system"
	"github.com/dagger/dagger/util/fsxutil"
)

func MountStubsCleaner(ctx context.Context, dir string, mounts []Mount, recursive bool) func() {
	names := []string{"/etc/resolv.conf", "/etc/hosts"}

	for _, m := range mounts {
		names = append(names, m.Dest)
	}

	paths := make([]string, 0, len(names))

	for _, p := range names {
		p = filepath.Join("/", p)
		if p == "/" {
			continue
		}
		realPath, err := fsxutil.RootPath(dir, p)
		if err != nil {
			continue
		}

		for {
			_, err = os.Lstat(realPath)
			if !(errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)) {
				break
			}
			paths = append(paths, realPath)

			if !recursive {
				break
			}

			realPathNext := filepath.Dir(realPath)
			if realPath == realPathNext || realPathNext == dir {
				break
			}
			realPath = realPathNext
		}
	}

	return func() {
		// realDirs records, for each directory under dir checked so far,
		// whether it and every directory above it up to dir is a real
		// directory, not a symlink, so each is checked once.
		realDirs := map[string]bool{}
		var isRealDir func(p string) bool
		isRealDir = func(p string) bool {
			if len(p) <= len(dir) {
				return p == dir
			}
			if real, ok := realDirs[p]; ok {
				return real
			}
			st, err := os.Lstat(p)
			real := err == nil && st.IsDir() && isRealDir(filepath.Dir(p))
			realDirs[p] = real
			return real
		}

		for _, p := range paths {
			// Only a stub reached without following a symlink is removed: the
			// container may have replaced one of its parents with a symlink.
			parent := filepath.Dir(p)
			if !isRealDir(parent) {
				continue
			}

			st, err := os.Lstat(p)
			if err != nil {
				continue
			}
			if st.Mode()&os.ModeSymlink != 0 || !st.IsDir() && st.Size() != 0 {
				continue
			}

			// Back up the timestamps of the dir for reproducible builds
			// https://github.com/dagger/dagger/internal/buildkit/issues/3148
			dirSt, err := os.Stat(parent)
			if err != nil {
				bklog.G(ctx).WithError(err).Warnf("Failed to stat %q (parent of mount stub %q)", dir, p)
				continue
			}
			mtime := dirSt.ModTime()
			atime, err := system.Atime(dirSt)
			if err != nil {
				bklog.G(ctx).WithError(err).Warnf("Failed to stat atime of %q (parent of mount stub %q)", dir, p)
				atime = mtime
			}

			// A directory that isn't empty fails to remove: it isn't only a stub.
			if err := os.Remove(p); err != nil {
				if !errors.Is(err, syscall.ENOTEMPTY) {
					bklog.G(ctx).WithError(err).Warnf("Failed to remove mount stub %q", p)
				}
				continue
			}

			// Restore the timestamps of the dir
			if err := os.Chtimes(parent, atime, mtime); err != nil {
				bklog.G(ctx).WithError(err).Warnf("Failed to restore time time mount stub timestamp (os.Chtimes(%q, %v, %v))", dir, atime, mtime)
			}
		}
	}
}
