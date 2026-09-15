package copy

import (
	"fmt"
	"io"
	"math"
	"os"
	"syscall"

	"github.com/dagger/dagger/engine/slog"
	"github.com/pkg/errors"
	"golang.org/x/sys/unix"
)

func getUIDGID(fi os.FileInfo) (uid, gid int) {
	st := fi.Sys().(*syscall.Stat_t)
	return int(st.Uid), int(st.Gid)
}

func (c *copier) copyFileInfo(fi os.FileInfo, name string) error {
	chown := c.chown
	uid, gid := getUIDGID(fi)
	old := &User{UID: uid, GID: gid}
	if chown == nil {
		chown = func(u *User) (*User, error) {
			return u, nil
		}
	}
	if err := Chown(name, old, chown); err != nil {
		return errors.Wrapf(err, "failed to chown %s", name)
	}

	m := fi.Mode()
	if c.mode != nil {
		m = os.FileMode(*c.mode).Perm()
		if *c.mode&syscall.S_ISGID != 0 {
			m |= os.ModeSetgid
		}
		if *c.mode&syscall.S_ISUID != 0 {
			m |= os.ModeSetuid
		}
		if *c.mode&syscall.S_ISVTX != 0 {
			m |= os.ModeSticky
		}
	}
	if (fi.Mode() & os.ModeSymlink) != os.ModeSymlink {
		if err := os.Chmod(name, m); err != nil {
			return errors.Wrapf(err, "failed to chmod %s", name)
		}
	}

	if err := c.copyFileTimestamp(fi, name); err != nil {
		return err
	}

	return nil
}

func (c *copier) copyFileTimestamp(fi os.FileInfo, name string) error {
	if c.utime != nil {
		return Utimes(name, c.utime)
	}

	st := fi.Sys().(*syscall.Stat_t)
	timespec := []unix.Timespec{unix.Timespec(StatAtime(st)), unix.Timespec(StatMtime(st))}
	if err := unix.UtimesNanoAt(unix.AT_FDCWD, name, timespec, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return errors.Wrapf(err, "failed to utime %s", name)
	}
	return nil
}

func (c *copier) copyFile(source, target string) (didHardlink bool, rerr error) {
	if c.enableHardlinkOptimization {
		_, err := os.Lstat(target)
		switch {
		case err == nil:
			// destination already exists, remove it
			if removeErr := os.Remove(target); removeErr != nil {
				return false, fmt.Errorf("failed to remove existing destination file %s: %w", target, removeErr)
			}
		case errors.Is(err, os.ErrNotExist):
			// destination does not exist, proceed
		default:
			return false, fmt.Errorf("failed to stat destination file %s: %w", target, err)
		}

		realSource, err := c.sourcePathResolver(source)
		if err != nil {
			return false, fmt.Errorf("failed to resolve source path %s: %w", source, err)
		}
		realTarget, err := c.destPathResolver(target)
		if err != nil {
			return false, fmt.Errorf("failed to resolve target path %s: %w", target, err)
		}

		err = os.Link(realSource, realTarget)
		switch {
		case err == nil:
			return true, nil
		case errors.Is(err, unix.EXDEV), errors.Is(err, syscall.EMLINK):
			// either crossing filesystem boundary or too many links, fallback to copy
			slog.ExtraDebug("hardlink failed, falling back to copy", "source", source, "target", target, "error", err)
		default:
			return false, fmt.Errorf("failed to create hardlink from %s to %s: %w", source, target, err)
		}
	}

	src, err := os.Open(source)
	if err != nil {
		return false, errors.Wrapf(err, "failed to open source %s", source)
	}
	defer src.Close()
	tgt, err := os.Create(target)
	if err != nil {
		return false, errors.Wrapf(err, "failed to open target %s", target)
	}
	defer tgt.Close()

	return false, CopyFileContent(tgt, src)
}

func CopyFileContent(dst, src *os.File) error {
	st, err := src.Stat()
	if err != nil {
		return errors.Wrap(err, "unable to stat source")
	}

	var written int64
	size := st.Size()
	first := true

	for written < size {
		var desired int
		if size-written > math.MaxInt32 {
			desired = int(math.MaxInt32)
		} else {
			desired = int(size - written)
		}

		n, err := unix.CopyFileRange(int(src.Fd()), nil, int(dst.Fd()), nil, desired, 0)
		if err != nil {
			// matches go/src/internal/poll/copy_file_range_linux.go
			if (err != unix.ENOSYS && err != unix.EXDEV && err != unix.EPERM && err != syscall.EIO && err != unix.EOPNOTSUPP && err != syscall.EINVAL) || !first {
				return errors.Wrap(err, "copy file range failed")
			}

			buf := bufferPool.Get().(*[]byte)
			_, err = io.CopyBuffer(dst, src, *buf)
			bufferPool.Put(buf)
			if err != nil {
				return errors.Wrap(err, "userspace copy failed")
			}
			return nil
		}

		first = false
		written += int64(n)
	}
	return nil
}

func mknod(dst string, mode uint32, rDev int) error {
	return unix.Mknod(dst, mode, rDev)
}
