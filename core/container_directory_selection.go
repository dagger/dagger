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
	ctx        context.Context
	base       *containerSelectionMount
	mounts     []*containerSelectionMount
	closers    []func() error
	stubDir    string
	stubs      map[string]*containerSelectionBacking
	ready      bool
	prepareErr error
}

var errOpaqueSelectionParent = errors.New("resolveBrokenSymlinks: cannot resolve mount target through non-snapshot mount")

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
			entry.load = s.emptyFilesMount
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

// Resolve each target through the mounts installed before it. Only sources
// traversed while locating a mountpoint are opened; no contents are copied.
// Later mounts hide earlier mounts at or below their resolved targets.
func (s *containerSelectionSource) prepare() error {
	if s.ready {
		return s.prepareErr
	}
	pending := s.mounts
	s.mounts = nil
	for _, mnt := range pending {
		target, err := layercopy.ResolveSelectionPath(containerSelectionTargets{s}, mnt.target)
		if err != nil {
			// An unresolved target could cover any selected path. Do not
			// copy from a mount table whose locations are only partly known.
			s.prepareErr = fmt.Errorf("resolve mount target %s: %w", mnt.target, err)
			s.ready = true
			return s.prepareErr
		}
		mnt.target = filepath.Join("/", target)
		s.mounts = slices.DeleteFunc(s.mounts, func(previous *containerSelectionMount) bool {
			return selectionPathUnder(previous.target, mnt.target)
		})
		s.mounts = append(s.mounts, mnt)
	}
	s.ready = true
	return nil
}

// A mountpoint is located in the namespace before that mount exists. Missing
// parents are mount stubs. Opaque parents cannot be replaced by stubs: their
// contents may redirect the target through a symlink.
type containerSelectionTargets struct{ source *containerSelectionSource }

func (v containerSelectionTargets) Open(name string, parent *os.Root) (*os.File, error) {
	name = filepath.Join("/", name)
	mnt := v.source.mountFor(name)
	if mnt.load == nil {
		return nil, fmt.Errorf("%w %s", errOpaqueSelectionParent, mnt.target)
	}
	// A source load failure must not be treated as a missing mountpoint.
	if _, err := mnt.get(); err != nil {
		return nil, err
	}
	f, err := v.source.open(name, parent, mnt)
	if !errors.Is(err, os.ErrNotExist) {
		return f, err
	}
	stub, err := v.source.stub(name)
	if err != nil {
		return nil, err
	}
	return stub.root.OpenFile(".", unix.O_PATH|unix.O_NOFOLLOW, 0)
}

func (containerSelectionTargets) Children(_ string, names []string) ([]string, error) {
	return names, nil
}

func selectionPathUnder(name, parent string) bool {
	return parent == "/" || name == parent || strings.HasPrefix(name, parent+"/")
}

func (s *containerSelectionSource) Open(name string, parent *os.Root) (*os.File, error) {
	// Freeze mount locations before returning any source handles. An exact
	// match can itself be an alias, so it cannot bypass target expansion.
	if err := s.prepare(); err != nil {
		return nil, err
	}
	name = filepath.Join("/", name)
	selected := s.mountFor(name)
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
	if err := s.prepare(); err != nil {
		return nil, err
	}
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
	name = filepath.Join("/", name)
	if backing := s.stubs[name]; backing != nil {
		return backing, nil
	}
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
	backing := &containerSelectionBacking{root: root, path: "."}
	if s.stubs == nil {
		s.stubs = map[string]*containerSelectionBacking{}
	}
	s.stubs[name] = backing
	return backing, nil
}

// A files mount starts empty. Reusing a mountpoint stub here would expose
// directories created while locating earlier, now-hidden mount targets.
func (s *containerSelectionSource) emptyFilesMount() (*containerSelectionBacking, error) {
	path, err := os.MkdirTemp("", "dagger-selection-files-")
	if err != nil {
		return nil, err
	}
	s.closers = append(s.closers, func() error { return os.RemoveAll(path) })
	if err := os.Chmod(path, 0755); err != nil {
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
