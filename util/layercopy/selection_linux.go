//go:build linux

package layercopy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// CopySelection copies a directory to the destination root. It repairs links
// that would break when the parent is removed, and preserves other links,
// including links that already have no target. The source is a mounted view;
// this operation does not accept filters or raw overlay layers.
func (c *Copier) CopySelection(ctx context.Context, root, name string) error {
	s := &source{root: root}
	if err := s.confine(); err != nil {
		return err
	}
	defer s.rootHandle.Close()
	name, err := s.resolveRooted(name, true)
	if err != nil {
		return err
	}
	f, err := s.openResolvedRooted(name, name, unix.O_PATH)
	if err != nil {
		return err
	}
	defer f.Close()
	ent, err := pinnedSelectionEntry(f)
	if err != nil {
		return err
	}
	if !ent.Info.IsDir() {
		return fmt.Errorf("path %s is a file, not a directory", name)
	}
	copy := selectionCopy{copier: c, source: s, directories: map[inode]string{}}
	return copy.directory(ctx, ent, name, "/", name)
}

type selectionCopy struct {
	copier      *Copier
	source      *source
	directories map[inode]string
}

func pinnedSelectionEntry(f *os.File) (sourceEntry, error) {
	info, err := f.Stat()
	if err != nil {
		return sourceEntry{}, err
	}
	return sourceEntry{Info: info, file: f, RealPath: fmt.Sprintf("/proc/self/fd/%d", f.Fd())}, nil
}

func (s *selectionCopy) directory(ctx context.Context, ent sourceEntry, name, dest, boundary string) error {
	s.directories[statInode(ent.Info.Sys().(*syscall.Stat_t))] = dest
	opts := CopyOptions{DisableSourceHardlinks: true}
	rel, _, err := s.copier.dest.ensureDir(dest, &ent, opts, true)
	if err != nil {
		return err
	}
	// Enumeration and child opens use the held directory, never its old path.
	// Each child open has one component and does not follow links.
	dir, err := os.OpenRoot(ent.RealPath)
	if err != nil {
		return err
	}
	defer dir.Close()
	f, err := dir.Open(".")
	if err != nil {
		return err
	}
	defer f.Close()
	children, err := f.ReadDir(-1)
	if err != nil {
		return err
	}
	// Stable order determines which copy of a shared directory is kept.
	slices.SortFunc(children, func(a, b os.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	for _, child := range children {
		if err := ctx.Err(); err != nil {
			return context.Cause(ctx)
		}
		if err := s.child(ctx, dir, child.Name(), filepath.Join(name, child.Name()), filepath.Join(dest, child.Name()), boundary); err != nil {
			return err
		}
	}
	// Apply metadata after creating children, including for the snapshot root.
	return s.copier.dest.applyMetadataPath(filepath.Join(s.copier.dest.writeRoot, rel), &ent, opts)
}

func (s *selectionCopy) child(ctx context.Context, parent *os.Root, base, name, dest, boundary string) error {
	f, err := parent.OpenFile(base, unix.O_PATH|unix.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	ent, err := pinnedSelectionEntry(f)
	if err != nil {
		return err
	}
	if ent.Info.Mode()&os.ModeSymlink != 0 {
		return s.link(ctx, ent, name, dest, boundary)
	}
	if ent.Info.IsDir() {
		return s.directory(ctx, ent, name, dest, boundary)
	}
	return s.copier.copyNode(ent, dest, resolvedParent{}, CopyOptions{DisableSourceHardlinks: true})
}

func (s *selectionCopy) link(ctx context.Context, ent sourceEntry, name, dest, boundary string) error {
	// Read the pinned link. Its old path can change; /proc/self/fd names the fd.
	buf := make([]byte, 4096)
	n, err := unix.Readlinkat(int(ent.file.Fd()), "", buf)
	if err != nil {
		return err
	}
	if n == len(buf) {
		return fmt.Errorf("symlink target too long at %q", name)
	}
	target := string(buf[:n])
	if !filepath.IsAbs(target) {
		stays, err := s.linkStaysWithin(boundary, filepath.Dir(name), target)
		if err != nil {
			return err
		}
		if stays {
			return s.writeLink(ent, dest, target)
		}
	}
	lookup := target
	if !filepath.IsAbs(target) {
		// Do not clean .. before expanding intermediate symlinks.
		lookup = filepath.Dir(name) + "/" + target
	}
	resolved, err := s.source.resolveRooted(lookup, true)
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, unix.ENOTDIR) || errors.Is(err, unix.ELOOP) {
		return s.writeLink(ent, dest, target)
	}
	if err != nil {
		return err
	}
	f, err := s.source.openResolvedRooted(lookup, resolved, unix.O_PATH)
	if err != nil {
		return err
	}
	defer f.Close()
	pinned, err := pinnedSelectionEntry(f)
	if err != nil {
		return err
	}
	if pinned.Info.IsDir() {
		if previous, ok := s.directories[statInode(pinned.Info.Sys().(*syscall.Stat_t))]; ok {
			relative, err := filepath.Rel(filepath.Dir(dest), previous)
			if err != nil {
				return err
			}
			return s.writeLink(ent, dest, relative)
		}
		return s.directory(ctx, pinned, resolved, dest, resolved)
	}
	return s.copier.copyNode(pinned, dest, resolvedParent{}, CopyOptions{DisableSourceHardlinks: true})
}

// A relative link keeps working if its text stays within the copied tree.
// Check the resolved prefix before .. as well: a/../file can leave the tree
// when a points outside it, even though the cleaned path looks local.
func (s *selectionCopy) linkStaysWithin(boundary, parent, target string) (bool, error) {
	prefix := parent
	for _, part := range strings.Split(target, "/") {
		if part == ".." {
			resolved, err := s.source.resolveRooted(prefix, true)
			if errors.Is(err, os.ErrNotExist) || errors.Is(err, unix.ENOTDIR) || errors.Is(err, unix.ELOOP) {
				return true, nil // Already broken in the parent.
			}
			if err != nil {
				return false, err
			}
			relative, err := filepath.Rel(boundary, resolved)
			if err != nil {
				return false, err
			}
			if relative == "." || relative == ".." || strings.HasPrefix(relative, "../") {
				return false, nil
			}
		}
		prefix += "/" + part
	}
	return true, nil
}

func (s *selectionCopy) writeLink(ent sourceEntry, dest, target string) error {
	real, err := s.copier.dest.realPath(dest)
	if err != nil {
		return err
	}
	if err := os.Symlink(target, real); err != nil {
		return err
	}
	// Do not dereference the descriptor when applying link metadata.
	ent.RealPath = ""
	return s.copier.dest.applyMetadataPath(real, &ent, CopyOptions{})
}
