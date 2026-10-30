//go:build linux

package layercopy

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	continuityfs "github.com/containerd/continuity/fs"
	"golang.org/x/sys/unix"
)

type source struct {
	root         string
	realRoot     string
	layers       []string
	overlay      bool
	baseRel      string
	baseView     string
	baseReal     string
	baseInfo     os.FileInfo
	baseMinLayer int
	baseCached   bool
	cache        *sourceCache

	// onReadDir, if set, is called with each directory the copy reads. Tests
	// use it to check which directories a filtered copy walks.
	onReadDir func(rel string)
}

type sourceEntry struct {
	Rel      string
	ViewPath string
	RealPath string
	Info     os.FileInfo
	StatErr  error
	// minLayer is the lowest overlay layer visible below this entry. An opaque
	// directory or non-directory in a higher layer hides lower descendants.
	minLayer int
}

func newSource(m Mount) (*source, error) {
	if m.Root == "" {
		return nil, fmt.Errorf("source root is empty")
	}

	s := &source{root: m.Root, realRoot: m.Root}
	if m.Mount == nil {
		return s, nil
	}

	switch {
	case m.Mount.Type == "bind" || m.Mount.Type == "rbind":
		s.realRoot = m.Mount.Source
	case isOverlayMount(m.Mount):
		layers, err := overlayLayers(m.Mount)
		if err != nil {
			return nil, err
		}
		s.layers = layers
		s.overlay = true
	default:
		return nil, fmt.Errorf("unsupported source mount type %q", m.Mount.Type)
	}
	return s, nil
}

func (s *source) selectBase(srcPath string, followFinalSymlink bool) error {
	if s.baseCached {
		return nil
	}

	baseView, err := rootPath(s.root, srcPath, followFinalSymlink)
	if err != nil {
		return err
	}
	baseRel, err := filepath.Rel(s.root, baseView)
	if err != nil {
		return err
	}
	if baseRel == "." {
		baseRel = ""
	}
	baseRel = filepath.Clean(baseRel)
	if baseRel == "." {
		baseRel = ""
	}

	info, err := os.Lstat(baseView)
	if err != nil {
		return err
	}
	// Only a directory's walk needs the layers its ancestors hide. The view
	// shows a non-directory, so the topmost layer holding it is the one the
	// view shows: no ancestor hides it.
	baseMinLayer := 0
	if info.IsDir() {
		baseMinLayer, err = s.overlayAncestorMinLayer(baseRel)
		if err != nil {
			return err
		}
	}

	s.baseView = baseView
	s.baseRel = baseRel
	s.baseInfo = info
	s.baseMinLayer = baseMinLayer
	realPath, realInfo, err := s.realPath("", baseMinLayer)
	if err != nil {
		return err
	}
	s.baseReal = realPath
	s.baseInfo = realInfo
	s.baseCached = true
	return nil
}

func (s *source) readDir(rel string, minLayer int) ([]sourceEntry, error) {
	if err := s.selectBase(".", false); err != nil {
		return nil, err
	}
	rel = cleanRel(rel)
	if s.onReadDir != nil {
		s.onReadDir(rel)
	}
	if !s.overlay {
		return s.readBindDir(rel)
	}
	return s.readOverlayDir(rel, minLayer)
}

func (s *source) readBindDir(rel string) ([]sourceEntry, error) {
	viewDir := filepath.Join(s.baseView, rel)
	realDir := filepath.Join(s.baseReal, rel)
	dirents, err := os.ReadDir(viewDir)
	if err != nil {
		return nil, err
	}

	entries := make([]sourceEntry, 0, len(dirents))
	for _, de := range dirents {
		name := de.Name()
		viewPath := filepath.Join(viewDir, name)
		info, err := os.Lstat(viewPath)
		if err != nil {
			if !os.IsNotExist(err) {
				return nil, err
			}
			entries = append(entries, sourceEntry{
				Rel:      filepath.Join(rel, name),
				ViewPath: viewPath,
				RealPath: filepath.Join(realDir, name),
				StatErr:  err,
			})
			continue
		}
		entries = append(entries, sourceEntry{
			Rel:      filepath.Join(rel, name),
			ViewPath: viewPath,
			RealPath: filepath.Join(realDir, name),
			Info:     info,
		})
	}
	return entries, nil
}

func (s *source) readOverlayDir(rel string, minLayer int) ([]sourceEntry, error) {
	type visibleEntry struct {
		realPath string
		info     os.FileInfo
	}

	layerRel := cleanRel(filepath.Join(s.baseRel, rel))
	visible := map[string]visibleEntry{}
	hidden := map[string]struct{}{}

	visibleMinLayer := minLayer
	for i := len(s.layers) - 1; i >= minLayer; i-- {
		layerDir := filepath.Join(s.layers[i], layerRel)
		isDir, hidesLower, err := overlayEntryState(layerDir)
		if err != nil {
			if os.IsNotExist(err) || isNotDir(err) {
				continue
			}
			return nil, err
		}
		if !isDir {
			visibleMinLayer = i
			break
		}

		dirents, err := os.ReadDir(layerDir)
		if os.IsNotExist(err) || isNotDir(err) {
			continue
		}
		if err != nil {
			return nil, err
		}

		for _, de := range dirents {
			name := de.Name()
			if _, ok := hidden[name]; ok {
				continue
			}
			if _, ok := visible[name]; ok {
				continue
			}

			realPath := filepath.Join(layerDir, name)
			info, err := os.Lstat(realPath)
			if err != nil {
				return nil, err
			}
			if isWhiteout(info) {
				hidden[name] = struct{}{}
				delete(visible, name)
				continue
			}
			visible[name] = visibleEntry{
				realPath: realPath,
				info:     info,
			}
		}
		if hidesLower {
			visibleMinLayer = i
			break
		}
	}

	names := make([]string, 0, len(visible))
	for name := range visible {
		names = append(names, name)
	}
	slices.Sort(names)

	viewDir := filepath.Join(s.baseView, rel)
	entries := make([]sourceEntry, 0, len(names))
	for _, name := range names {
		ent := visible[name]
		entries = append(entries, sourceEntry{
			Rel:      filepath.Join(rel, name),
			ViewPath: filepath.Join(viewDir, name),
			RealPath: ent.realPath,
			Info:     ent.info,
			minLayer: visibleMinLayer,
		})
	}
	return entries, nil
}

func (s *source) realPath(rel string, minLayer int) (string, os.FileInfo, error) {
	rel = cleanRel(filepath.Join(s.baseRel, rel))
	if !s.overlay {
		realPath := filepath.Join(s.realRoot, rel)
		realInfo, err := os.Lstat(realPath)
		if err != nil {
			return "", nil, err
		}
		return realPath, realInfo, nil
	}

	for i := len(s.layers) - 1; i >= minLayer; i-- {
		realPath := filepath.Join(s.layers[i], rel)
		realInfo, err := os.Lstat(realPath)
		if err == nil {
			if isWhiteout(realInfo) {
				break
			}
			return realPath, realInfo, nil
		}
		if os.IsNotExist(err) || isNotDir(err) {
			continue
		}
		return "", nil, err
	}
	return "", nil, fmt.Errorf("failed to resolve source path %q in overlay layers", rel)
}

func (s *source) overlayAncestorMinLayer(rel string) (int, error) {
	if !s.overlay {
		return 0, nil
	}

	ancestors := []string{""}
	parent := cleanRel(filepath.Dir(rel))
	if parent != "" {
		var ancestor string
		for _, part := range strings.Split(parent, string(filepath.Separator)) {
			ancestor = filepath.Join(ancestor, part)
			ancestors = append(ancestors, ancestor)
		}
	}

	minLayer := 0
	for _, ancestor := range ancestors {
		if s.cache != nil {
			if cached, ok := s.cache.ancestorMinLayers[ancestor]; ok {
				if cached > minLayer {
					minLayer = cached
				}
				continue
			}
		}

		for i := len(s.layers) - 1; i >= minLayer; i-- {
			_, hidesLower, err := overlayEntryState(filepath.Join(s.layers[i], ancestor))
			if err != nil {
				if os.IsNotExist(err) || isNotDir(err) {
					continue
				}
				return 0, err
			}
			if hidesLower {
				minLayer = i
				break
			}
		}
		if s.cache != nil {
			s.cache.ancestorMinLayers[ancestor] = minLayer
		}
	}
	return minLayer, nil
}

func rootPath(root, p string, followFinalSymlink bool) (string, error) {
	// Joining cleans, and cleaning allocates; a clean absolute path is what
	// the join would give.
	if !filepath.IsAbs(p) || filepath.Clean(p) != p {
		p = filepath.Join("/", p)
	}
	if p == "/" {
		return root, nil
	}
	if resolved, ok := rootPathWithoutSymlinks(root, p, followFinalSymlink); ok {
		return resolved, nil
	}
	if followFinalSymlink {
		return continuityfs.RootPath(root, p)
	}
	dir, base := filepath.Split(p)
	resolvedDir, err := continuityfs.RootPath(root, dir)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolvedDir, base), nil
}

// rootPathWithoutSymlinks settles, with one openat2, the common case of a path
// that holds no symlinks, where continuity's walk takes an lstat, and several
// allocations, per component. Such a path resolves to itself under root. A
// final symlink counts only when followFinalSymlink is set. ok is false when
// the lookup can't settle it: a symlink anywhere in the path, root's own
// components included, or any error but a missing component. Every component
// before a missing one is a directory, and the walk takes the missing ones as
// they are.
func rootPathWithoutSymlinks(root, p string, followFinalSymlink bool) (string, bool) {
	full := root + p
	if filepath.Clean(full) != full {
		full = filepath.Join(root, p)
	}
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
		return full, true
	case unix.ENOENT:
		return full, true
	}
	return "", false
}

func isWhiteout(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	if st.Mode&syscall.S_IFMT != syscall.S_IFCHR {
		return false
	}
	return unix.Major(st.Rdev) == 0 && unix.Minor(st.Rdev) == 0
}

// overlayEntryState reports whether an overlay entry is a directory and
// whether it prevents lower layers from contributing below its path.
// Non-directories cover everything lower layers hold at the path, while opaque
// directories hide lower descendants.
func overlayEntryState(path string) (isDir, hidesLower bool, err error) {
	info, err := os.Lstat(path)
	if err != nil {
		return false, false, err
	}
	if !info.IsDir() {
		return false, true, nil
	}
	// The opaque value is the one byte "y", so one byte is all that's read: a
	// longer value doesn't fit (ERANGE), and isn't the marker either.
	var val [1]byte
	for _, key := range []string{"trusted.overlay.opaque", "user.overlay.opaque"} {
		n, err := unix.Lgetxattr(path, key, val[:])
		if err == unix.ENODATA || err == unix.ENOTSUP || err == unix.ERANGE {
			continue
		}
		if err != nil {
			return false, false, err
		}
		if n == 1 && val[0] == 'y' {
			return true, true, nil
		}
	}
	return true, false, nil
}

func isNotDir(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "not a directory") || os.IsNotExist(err))
}
