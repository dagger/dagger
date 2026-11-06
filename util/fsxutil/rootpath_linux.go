package fsxutil

import "golang.org/x/sys/unix"

// resolvesWithoutSymlinks reports whether a lookup of full that refuses symlinks
// finds it, or finds every component but a missing one.
func resolvesWithoutSymlinks(full string, followFinalSymlink bool) bool {
	how := unix.OpenHow{
		Flags:   unix.O_PATH | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_NO_SYMLINKS,
	}
	if !followFinalSymlink {
		how.Flags |= unix.O_NOFOLLOW
	}
	fd, err := unix.Openat2(unix.AT_FDCWD, full, &how)
	switch err {
	case nil:
		unix.Close(fd)
		return true
	case unix.ENOENT:
		return true
	}
	return false
}
