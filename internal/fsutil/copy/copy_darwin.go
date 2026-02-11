//go:build darwin
// +build darwin

package copy

import (
	"io"
	"os"

	"github.com/pkg/errors"
	"golang.org/x/sys/unix"
)

func (c *copier) copyFile(source, target string) (didHardlink bool, rerr error) {
	if err := unix.Clonefileat(unix.AT_FDCWD, source, unix.AT_FDCWD, target, unix.CLONE_NOFOLLOW); err != nil {
		if err != unix.EINVAL && err != unix.EXDEV {
			return false, err
		}
	} else {
		return false, nil
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
	buf := bufferPool.Get().(*[]byte)
	_, err := io.CopyBuffer(dst, src, *buf)
	bufferPool.Put(buf)

	return err
}

func mknod(dst string, mode uint32, rDev int) error {
	return unix.Mknod(dst, uint32(mode), rDev)
}
