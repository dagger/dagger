//go:build linux

package layercopy

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// ResolveSelectionPath expands a path through the same confined entry source
// and symlink budget used by CopySelection. Missing mountpoint parents can be
// supplied as synthetic directories by the source.
func ResolveSelectionPath(entries SelectionSource, name string) (string, error) {
	return (&source{selection: entries}).resolveRooted(name, true)
}

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
	var f *os.File
	var err error
	if s.selection != nil {
		f, err = s.selection.Open(resolved, nil)
	} else {
		f, err = s.rootHandle.OpenFile(resolved, flags, 0)
	}
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
	return s.resolveRootedAfterLinks(name, followFinal, 0)
}

// links includes any pinned symlinks whose text the caller already expanded.
func (s *source) resolveRootedAfterLinks(name string, followFinal bool, links int) (string, error) {
	resolved, _, err := s.resolveRootedPath(name, followFinal, links, "")
	return resolved, err
}

// local reports whether expansion used only relative links and stayed within
// boundary, a canonical prefix of name (empty means the snapshot root).
// Classification shares the walk and traversal budget of target resolution;
// os.Root confines every filesystem access.
func (s *source) resolveRootedPath(name string, followFinal bool, links int, boundary string) (string, bool, error) {
	boundaryDepth := 0
	if boundary = cleanRel(boundary); boundary != "" {
		boundaryDepth = strings.Count(boundary, "/") + 1
	}
	local := true
	pending := strings.Split(name, "/")
	var resolved []string
	for len(pending) != 0 {
		component := pending[0]
		pending = pending[1:]
		switch component {
		case "", ".":
			continue
		case "..":
			if len(resolved) <= boundaryDepth {
				local = false
			}
			if len(resolved) != 0 {
				resolved = resolved[:len(resolved)-1]
			}
			continue
		}
		candidate := filepath.Join(strings.Join(resolved, "/"), component)
		info, target, err := s.rootedEntry(candidate)
		if err != nil {
			return "", false, err
		}
		if info.Mode()&os.ModeSymlink != 0 && (followFinal || len(pending) != 0) {
			links++
			if links > 40 {
				return "", false, &os.PathError{Op: "resolve in source root", Path: name, Err: unix.ELOOP}
			}
			if filepath.IsAbs(target) {
				local = false
				resolved = nil
			}
			pending = append(strings.Split(target, "/"), pending...)
			continue
		}
		if len(pending) != 0 && !info.IsDir() {
			return "", false, &os.PathError{Op: "resolve in source root", Path: name, Err: unix.ENOTDIR}
		}
		resolved = append(resolved, component)
	}
	result := strings.Join(resolved, "/")
	if result == "" {
		result = "."
	}
	return result, local, nil
}

func (s *source) rootedEntry(name string) (os.FileInfo, string, error) {
	if s.selection == nil {
		info, err := s.rootHandle.Lstat(name)
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			return info, "", err
		}
		target, err := s.rootHandle.Readlink(name)
		return info, target, err
	}
	f, err := s.selection.Open(name, nil)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return info, "", err
	}
	buf := make([]byte, 4096)
	n, err := unix.Readlinkat(int(f.Fd()), "", buf)
	if err != nil {
		return nil, "", err
	}
	if n == len(buf) {
		return nil, "", &os.PathError{Op: "readlink", Path: name, Err: unix.ENAMETOOLONG}
	}
	return info, string(buf[:n]), nil
}
