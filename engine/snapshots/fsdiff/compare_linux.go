//go:build linux
// +build linux

package fsdiff

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"sync"
	"syscall"

	"github.com/containerd/continuity/sysx"
)

func samePathInfo(
	f1, f2 os.FileInfo,
	p1, p2 string,
	comparison Comparison,
) (bool, error) {
	if os.SameFile(f1, f2) {
		return true, nil
	}

	if !compareSysStat(f1.Sys(), f2.Sys()) {
		return false, nil
	}

	if eq, err := compareCapabilities(p1, p2); err != nil || !eq {
		return eq, err
	}

	if f1.IsDir() {
		return true, nil
	}

	if f1.Size() != f2.Size() {
		return false, nil
	}

	t1 := f1.ModTime()
	t2 := f2.ModTime()
	if t1.Unix() != t2.Unix() {
		return false, nil
	}
	if t1.Nanosecond() != t2.Nanosecond() {
		return false, nil
	}

	switch comparison {
	case CompareCompat:
		if t1.Nanosecond() == 0 && t2.Nanosecond() == 0 {
			if (f1.Mode() & os.ModeSymlink) == os.ModeSymlink {
				return compareSymlinkTarget(p1, p2)
			}
			if f1.Size() == 0 {
				return true, nil
			}
			return compareFileContent(p1, p2)
		}
		return true, nil
	case CompareContentOnMetadataMatch:
		if (f1.Mode() & os.ModeSymlink) == os.ModeSymlink {
			return compareSymlinkTarget(p1, p2)
		}
		if f1.Size() == 0 {
			return true, nil
		}
		return compareFileContent(p1, p2)
	default:
		return false, fmt.Errorf("unknown diff comparison mode %d", comparison)
	}
}

func compareSysStat(s1, s2 interface{}) bool {
	ls1, ok := s1.(*syscall.Stat_t)
	if !ok {
		return false
	}
	ls2, ok := s2.(*syscall.Stat_t)
	if !ok {
		return false
	}

	return ls1.Mode == ls2.Mode && ls1.Uid == ls2.Uid && ls1.Gid == ls2.Gid && ls1.Rdev == ls2.Rdev
}

func compareCapabilities(p1, p2 string) (bool, error) {
	c1, err := sysx.LGetxattr(p1, "security.capability")
	if err != nil && err != sysx.ENODATA {
		return false, fmt.Errorf("failed to get xattr for %s: %w", p1, err)
	}
	c2, err := sysx.LGetxattr(p2, "security.capability")
	if err != nil && err != sysx.ENODATA {
		return false, fmt.Errorf("failed to get xattr for %s: %w", p2, err)
	}
	return bytes.Equal(c1, c2), nil
}

func compareSymlinkTarget(p1, p2 string) (bool, error) {
	t1, err := os.Readlink(p1)
	if err != nil {
		return false, err
	}
	t2, err := os.Readlink(p2)
	if err != nil {
		return false, err
	}
	return t1 == t2, nil
}

var compareBufPool = sync.Pool{
	New: func() any {
		buf := make([]byte, 32*1024)
		return &buf
	},
}

func compareFileContent(p1, p2 string) (bool, error) {
	f1, err := os.Open(p1)
	if err != nil {
		return false, err
	}
	defer f1.Close()

	f2, err := os.Open(p2)
	if err != nil {
		return false, err
	}
	defer f2.Close()

	b1 := compareBufPool.Get().(*[]byte)
	defer compareBufPool.Put(b1)
	b2 := compareBufPool.Get().(*[]byte)
	defer compareBufPool.Put(b2)
	for {
		n1, err1 := io.ReadFull(f1, *b1)
		if err1 == io.ErrUnexpectedEOF {
			err1 = io.EOF
		}
		if err1 != nil && err1 != io.EOF {
			return false, err1
		}

		n2, err2 := io.ReadFull(f2, *b2)
		if err2 == io.ErrUnexpectedEOF {
			err2 = io.EOF
		}
		if err2 != nil && err2 != io.EOF {
			return false, err2
		}

		if n1 != n2 || !bytes.Equal((*b1)[:n1], (*b2)[:n2]) {
			return false, nil
		}
		if err1 == io.EOF && err2 == io.EOF {
			return true, nil
		}
	}
}

func isLinked(f os.FileInfo) bool {
	s, ok := f.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	return !f.IsDir() && s.Nlink > 1
}
