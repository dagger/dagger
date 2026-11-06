package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	containerdfs "github.com/containerd/continuity/fs"
	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/util/layercopy"
	"golang.org/x/sys/unix"
)

// materializeSelection reads through the mount table. Opening a mount evaluates
// its part once; unrelated mounts contribute names, never filesystem contents.
func (lazy *ContainerDirectoryLazy) materializeSelection(ctx context.Context, dir *Directory) error {
	parent := lazy.Parent.Self()
	source, err := newContainerSelectionSource(ctx, lazy.Parent)
	if err != nil {
		return err
	}
	defer source.Close()
	dir.Platform = parent.Platform
	dir.Services = slices.Clone(parent.Services)
	return dir.materializeSelection(ctx, func(copier *layercopy.Copier) error {
		err := copier.CopySelection(ctx, "", absPath(parent.Config.WorkingDir, lazy.Path), layercopy.SelectionOptions{Source: source})
		if errors.Is(err, unix.ENOTDIR) {
			return notADirectoryError{err}
		}
		return err
	})
}

type containerSelectionBacking struct {
	root *os.Root
	path string // "." for directories, basename for a mounted file
}

type containerSelectionMount struct {
	target  string
	load    func() (*containerSelectionBacking, error)
	backing *containerSelectionBacking
}

func (m *containerSelectionMount) get() (*containerSelectionBacking, error) {
	if m.load == nil {
		return nil, fmt.Errorf("resolveBrokenSymlinks: cannot retrieve path from non-snapshot mount %s", m.target)
	}
	if m.backing == nil {
		var err error
		m.backing, err = m.load()
		if err != nil {
			return nil, err
		}
	}
	return m.backing, nil
}

type containerSelectionSource struct {
	ctx     context.Context
	base    *containerSelectionMount
	mounts  []*containerSelectionMount
	closers []func() error
	stubDir string
	ready   bool
}

func newContainerSelectionSource(ctx context.Context, parent dagql.ObjectResult[*Container]) (*containerSelectionSource, error) {
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return nil, err
	}
	s := &containerSelectionSource{ctx: ctx}
	s.base = &containerSelectionMount{target: "/", load: func() (*containerSelectionBacking, error) {
		if err := cache.EvaluateParts(ctx, parent, ContainerPartFS); err != nil {
			return nil, err
		}
		dir, ok := parent.Self().FS.Peek()
		if !ok || dir == nil {
			return nil, fmt.Errorf("container directory selection: missing rootfs")
		}
		return s.openDirectory(dir)
	}}
	mounts := slices.Clone(parent.Self().Mounts)
	for _, secret := range parent.Self().Secrets {
		if secret.MountPath != "" {
			mounts = append(mounts, ContainerMount{Target: secret.MountPath})
		}
	}
	for _, socket := range parent.Self().Sockets {
		mounts = append(mounts, ContainerMount{Target: socket.ContainerPath})
	}
	slices.SortStableFunc(mounts, func(a, b ContainerMount) int { return strings.Compare(a.Target, b.Target) })
	for _, mnt := range mounts {
		entry := &containerSelectionMount{target: filepath.Join("/", mnt.Target)}
		switch {
		case mnt.DirectorySource != nil:
			entry.load = func() (*containerSelectionBacking, error) {
				if err := cache.EvaluateParts(ctx, parent, ContainerPartMount(mnt.Target)); err != nil {
					return nil, err
				}
				dir, ok := mnt.DirectorySource.Peek()
				if !ok || dir == nil {
					return nil, fmt.Errorf("mount %s has no directory source", mnt.Target)
				}
				return s.openDirectory(dir)
			}
		case mnt.FileSource != nil:
			entry.load = func() (*containerSelectionBacking, error) {
				if err := cache.EvaluateParts(ctx, parent, ContainerPartMount(mnt.Target)); err != nil {
					return nil, err
				}
				file, ok := mnt.FileSource.Peek()
				if !ok || file == nil {
					return nil, fmt.Errorf("mount %s has no file source", mnt.Target)
				}
				return s.openFile(file)
			}
		case mnt.FilesSource != nil:
			entry.load = func() (*containerSelectionBacking, error) { return s.stub(entry.target) }
		}
		if entry.target == "/" {
			s.base = entry
		} else {
			s.mounts = append(s.mounts, entry)
		}
		if mnt.FilesSource != nil {
			for _, file := range mnt.FilesSource.Files {
				s.mounts = append(s.mounts, &containerSelectionMount{
					target: filepath.Join(entry.target, file.Name),
					load: func() (*containerSelectionBacking, error) {
						if err := cache.Evaluate(ctx, file.File); err != nil {
							return nil, err
						}
						return s.openFile(file.File.Self())
					},
				})
			}
		}
	}
	return s, nil
}

// Resolve rootfs aliases without opening mount sources. Later mounts hide an
// earlier mount at the same resolved target or below it, as they do in exec.
func (s *containerSelectionSource) prepare() error {
	if s.ready {
		return nil
	}
	base, err := s.base.get()
	if err != nil {
		return err
	}
	pinned, err := base.root.OpenFile(".", unix.O_PATH, 0)
	if err != nil {
		return err
	}
	defer pinned.Close()
	boundary := fmt.Sprintf("/proc/self/fd/%d", pinned.Fd())
	var visible []*containerSelectionMount
	for _, mnt := range s.mounts {
		target, err := containerdfs.RootPath(boundary, mnt.target)
		if err != nil {
			// A bad target must not reject an unrelated selection.
			mnt.load = func() (*containerSelectionBacking, error) { return nil, err }
		} else {
			rel, err := filepath.Rel(boundary, target)
			if err != nil {
				return err
			}
			mnt.target = filepath.Join("/", rel)
		}
		visible = slices.DeleteFunc(visible, func(previous *containerSelectionMount) bool {
			return selectionPathUnder(previous.target, mnt.target)
		})
		visible = append(visible, mnt)
	}
	s.mounts = visible
	s.ready = true
	return nil
}

func selectionPathUnder(name, parent string) bool {
	return parent == "/" || name == parent || strings.HasPrefix(name, parent+"/")
}

func (s *containerSelectionSource) Open(name string, parent *os.Root) (*os.File, error) {
	name = filepath.Join("/", name)
	selected := s.mountFor(name)
	if selected == s.base && !s.ready {
		if err := s.prepare(); err != nil {
			return nil, err
		}
		selected = s.mountFor(name)
	}
	return s.open(name, parent, selected)
}

func (s *containerSelectionSource) mountFor(name string) *containerSelectionMount {
	selected := s.base
	for _, mnt := range s.mounts {
		if selectionPathUnder(name, mnt.target) && len(mnt.target) >= len(selected.target) {
			selected = mnt
		}
	}
	return selected
}

func (s *containerSelectionSource) open(name string, parent *os.Root, selected *containerSelectionMount) (*os.File, error) {
	if selected.load == nil {
		if s.hasChildren(name) {
			stub, err := s.stub(name)
			if err != nil {
				return nil, err
			}
			return stub.root.OpenFile(".", unix.O_PATH|unix.O_NOFOLLOW, 0)
		}
		return nil, fmt.Errorf("resolveBrokenSymlinks: cannot retrieve path from non-snapshot mount %s", selected.target)
	}
	var f *os.File
	var err error
	if parent != nil && name != selected.target {
		f, err = parent.OpenFile(filepath.Base(name), unix.O_PATH|unix.O_NOFOLLOW, 0)
	} else {
		backing, loadErr := selected.get()
		if loadErr != nil {
			return nil, loadErr
		}
		rel, relErr := filepath.Rel(selected.target, name)
		if relErr != nil {
			return nil, relErr
		}
		f, err = backing.root.OpenFile(filepath.Join(backing.path, rel), unix.O_PATH|unix.O_NOFOLLOW, 0)
	}
	if !errors.Is(err, os.ErrNotExist) || !s.hasChildren(name) {
		return f, err
	}
	stub, err := s.stub(name)
	if err != nil {
		return nil, err
	}
	return stub.root.OpenFile(".", unix.O_PATH|unix.O_NOFOLLOW, 0)
}

func (s *containerSelectionSource) hasChildren(name string) bool {
	return slices.ContainsFunc(s.mounts, func(mnt *containerSelectionMount) bool {
		return mnt.target != name && selectionPathUnder(mnt.target, name)
	})
}

func (s *containerSelectionSource) Children(name string, names []string) ([]string, error) {
	name = filepath.Join("/", name)
	if mnt := s.mountFor(name); mnt.load == nil {
		return nil, fmt.Errorf("resolveBrokenSymlinks: cannot retrieve path from non-snapshot mount %s", mnt.target)
	}
	seen := map[string]bool{}
	for _, child := range names {
		seen[child] = true
	}
	for _, mnt := range s.mounts {
		if mnt.target == name || !selectionPathUnder(mnt.target, name) {
			continue
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(mnt.target, name), "/")
		child := strings.SplitN(rel, "/", 2)[0]
		if !seen[child] {
			names = append(names, child)
			seen[child] = true
		}
	}
	return names, nil
}

func (s *containerSelectionSource) openDirectory(dir *Directory) (*containerSelectionBacking, error) {
	ref, path, err := materializedDirectorySnapshotAndPath(dir)
	if err != nil {
		return nil, err
	}
	return s.openSnapshot(ref, path, false)
}

func (s *containerSelectionSource) openFile(file *File) (*containerSelectionBacking, error) {
	ref, ok := file.Snapshot.Peek()
	if !ok {
		return nil, fmt.Errorf("mount file has no snapshot")
	}
	path, ok := file.File.Peek()
	if !ok {
		return nil, fmt.Errorf("mount file has no path")
	}
	return s.openSnapshot(ref, path, true)
}

func (s *containerSelectionSource) openSnapshot(ref bkcache.ImmutableRef, path string, file bool) (*containerSelectionBacking, error) {
	root, _, release, err := MountRefCloser(s.ctx, ref, mountRefAsReadOnly)
	if err != nil {
		return nil, err
	}
	resolved, err := containerdfs.RootPath(root, path)
	if err != nil {
		release()
		return nil, err
	}
	base := "."
	if file {
		base, resolved = filepath.Base(resolved), filepath.Dir(resolved)
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil {
		release()
		return nil, err
	}
	pinned, err := os.OpenInRoot(root, rel)
	if err != nil {
		release()
		return nil, err
	}
	defer pinned.Close()
	handle, err := os.OpenRoot(fmt.Sprintf("/proc/self/fd/%d", pinned.Fd()))
	if err != nil {
		release()
		return nil, err
	}
	s.closers = append(s.closers, release, handle.Close)
	return &containerSelectionBacking{root: handle, path: base}, nil
}

func (s *containerSelectionSource) stub(name string) (*containerSelectionBacking, error) {
	if s.stubDir == "" {
		var err error
		s.stubDir, err = os.MkdirTemp("", "dagger-selection-mounts-")
		if err != nil {
			return nil, err
		}
		if err := os.Chmod(s.stubDir, 0755); err != nil {
			return nil, err
		}
	}
	path := filepath.Join(s.stubDir, filepath.Join("/", name))
	if err := os.MkdirAll(path, 0755); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	s.closers = append(s.closers, root.Close)
	return &containerSelectionBacking{root: root, path: "."}, nil
}

func (s *containerSelectionSource) Close() {
	for _, close := range slices.Backward(s.closers) {
		_ = close()
	}
	if s.stubDir != "" {
		_ = os.RemoveAll(s.stubDir)
	}
}
