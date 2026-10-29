//go:build linux

package layercopy

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
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

func (s *source) selectRootedBase(name string, follow bool) error {
	name = cleanContainerPath(name)
	flags := unix.O_PATH
	if !follow {
		flags |= unix.O_NOFOLLOW
	}
	f, err := s.openRooted(name, flags)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	s.baseRel = strings.TrimPrefix(cleanContainerPath(name), "/")
	s.baseView = filepath.Join(s.root, s.baseRel)
	s.baseReal = s.baseView
	s.baseInfo = info
	s.baseCached = true
	return nil
}

func (s *source) pinEntry(ent sourceEntry) (sourceEntry, error) {
	f, err := s.openRooted(filepath.Join(s.baseRel, ent.Rel), unix.O_PATH)
	if err != nil {
		return ent, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return ent, err
	}
	ent.Info = info
	ent.file = f
	// This is an internally constructed procfs reference to a held descriptor,
	// not an untrusted path. Content and xattrs use the same pinned inode.
	ent.RealPath = fmt.Sprintf("/proc/self/fd/%d", f.Fd())
	return ent, nil
}

// pinCopyEntry skips excluded links before it resolves their targets.
func (s *source) pinCopyEntry(ent sourceEntry, include bool, matcher *matcher, state matchState) (sourceEntry, bool, error) {
	isSymlink := ent.Info.Mode()&os.ModeSymlink != 0
	if isSymlink {
		if !include && !matcher.shouldDescend(ent.Rel, state) {
			return ent, true, nil
		}
		if state.excludeMatched && !matcher.exclude.Exclusions() {
			return ent, true, nil
		}
	}
	pinned, err := s.pinEntry(ent)
	if err != nil {
		if isSymlink && !include {
			return ent, true, nil
		}
		return ent, false, fmt.Errorf("open confined source %q: %w", ent.Rel, err)
	}
	return pinned, false, nil
}

func (s *source) readRootedDir(rel string) ([]sourceEntry, error) {
	name := filepath.Join(s.baseRel, rel)
	f, err := s.openRooted(name, unix.O_RDONLY|unix.O_DIRECTORY)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	dirents, err := f.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(dirents, func(a, b os.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	entries := make([]sourceEntry, 0, len(dirents))
	for _, de := range dirents {
		child := filepath.Join(rel, de.Name())
		ent := sourceEntry{Rel: child, ViewPath: filepath.Join(s.baseView, child)}
		file, err := s.openRooted(filepath.Join(s.baseRel, child), unix.O_PATH|unix.O_NOFOLLOW)
		if err == nil {
			ent.Info, err = file.Stat()
			file.Close()
		}
		if err != nil {
			if !os.IsNotExist(err) {
				return nil, err
			}
			ent.StatErr = err
		}
		entries = append(entries, ent)
	}
	return entries, nil
}

// GitignoreMatcher only needs Open, but accepts the broader fsutil.FS interface.
// Its rule files must be opened through the same boundary as directory contents.
type rootedIgnoreFS struct{ source *source }

func (r rootedIgnoreFS) Open(name string) (io.ReadCloser, error) {
	f, err := r.source.openRooted(name, unix.O_PATH)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("gitignore %q is not a regular file", name)
	}
	// Check the pinned inode before opening it for reading: opening an attacker-
	// supplied device or FIFO for reading may have side effects or block.
	return os.Open(fmt.Sprintf("/proc/self/fd/%d", f.Fd()))
}

func (rootedIgnoreFS) Walk(context.Context, string, fs.WalkDirFunc) error {
	return fmt.Errorf("walking the gitignore source is unsupported")
}
