//go:build linux

package layercopy

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// confine pins the mounted snapshot, including overlay visibility rules. All
// untrusted source paths in follow mode are accessed through the same os.Root.
func (s *source) confine() error {
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return err
	}
	s.rootHandle = root
	return nil
}

// openRooted preserves snapshot-relative absolute symlinks and clamps .. at the
// snapshot root. os.Root provides confinement for both link expansion and the
// final open: a concurrent replacement can fail or select another in-root inode,
// but cannot redirect an operation to the host filesystem.
func (s *source) openRooted(name string, flags int) (*os.File, error) {
	resolved, err := s.resolveRooted(name, flags&unix.O_NOFOLLOW == 0)
	if err != nil {
		return nil, err
	}
	return s.openResolvedRooted(name, resolved, flags)
}

// openResolvedRooted keeps the final open confined even if a resolved pathname
// changes after expansion. The pathname is not a capability; os.Root remains the
// security boundary until a descriptor has been opened and checked.
func (s *source) openResolvedRooted(name, resolved string, flags int) (*os.File, error) {
	f, err := s.rootHandle.OpenFile(resolved, flags, 0)
	if err != nil {
		return nil, err
	}
	// O_PATH|O_NOFOLLOW pins a symlink itself. Reject a final component replaced
	// with a symlink after expansion when the caller requested its target.
	if flags&unix.O_PATH != 0 && flags&unix.O_NOFOLLOW == 0 {
		info, err := f.Stat()
		if err != nil {
			f.Close()
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			f.Close()
			return nil, &os.PathError{Op: "open in source root", Path: name, Err: unix.ELOOP}
		}
	}
	return f, nil
}

// resolveRooted interprets link text without letting the kernel follow absolute
// or magic links. The result is only used with os.Root, never an ordinary open.
func (s *source) resolveRooted(name string, followFinal bool) (string, error) {
	pending := strings.Split(name, "/")
	var resolved []string
	links := 0
	for len(pending) != 0 {
		component := pending[0]
		pending = pending[1:]
		switch component {
		case "", ".":
			continue
		case "..":
			if len(resolved) != 0 {
				resolved = resolved[:len(resolved)-1]
			}
			continue
		}
		candidate := filepath.Join(strings.Join(resolved, "/"), component)
		info, err := s.rootHandle.Lstat(candidate)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 && (followFinal || len(pending) != 0) {
			links++
			if links > 40 {
				return "", &os.PathError{Op: "resolve in source root", Path: name, Err: unix.ELOOP}
			}
			target, err := s.rootHandle.Readlink(candidate)
			if err != nil {
				return "", err
			}
			if filepath.IsAbs(target) {
				resolved = nil
			}
			pending = append(strings.Split(target, "/"), pending...)
			continue
		}
		if len(pending) != 0 && !info.IsDir() {
			return "", &os.PathError{Op: "resolve in source root", Path: name, Err: unix.ENOTDIR}
		}
		resolved = append(resolved, component)
	}
	result := strings.Join(resolved, "/")
	if result == "" {
		result = "."
	}
	return result, nil
}
