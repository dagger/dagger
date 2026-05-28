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
	"github.com/containerd/continuity/sysx"
	"golang.org/x/sys/unix"
)

type source struct {
	root       string
	realRoot   string
	layers     []string
	overlay    bool
	baseRel    string
	baseView   string
	baseReal   string
	baseInfo   os.FileInfo
	baseCached bool
}

type sourceEntry struct {
	Rel      string
	ViewPath string
	RealPath string
	Info     os.FileInfo
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

	s.baseView = baseView
	s.baseRel = baseRel
	s.baseInfo = info
	realPath, realInfo, err := s.realPath("", info)
	if err != nil {
		return err
	}
	s.baseReal = realPath
	s.baseInfo = realInfo
	s.baseCached = true
	return nil
}

func (s *source) entry(rel string) (sourceEntry, error) {
	if err := s.selectBase(".", false); err != nil {
		return sourceEntry{}, err
	}
	if rel == "" || rel == "." {
		return sourceEntry{
			Rel:      "",
			ViewPath: s.baseView,
			RealPath: s.baseReal,
			Info:     s.baseInfo,
		}, nil
	}

	viewPath := filepath.Join(s.baseView, rel)
	info, err := os.Lstat(viewPath)
	if err != nil {
		return sourceEntry{}, err
	}
	realPath, realInfo, err := s.realPath(rel, info)
	if err != nil {
		return sourceEntry{}, err
	}
	return sourceEntry{
		Rel:      filepath.Clean(rel),
		ViewPath: viewPath,
		RealPath: realPath,
		Info:     realInfo,
	}, nil
}

func (s *source) readDir(rel string) ([]sourceEntry, error) {
	if err := s.selectBase(".", false); err != nil {
		return nil, err
	}
	rel = cleanRel(rel)
	if !s.overlay {
		return s.readBindDir(rel)
	}
	return s.readOverlayDir(rel)
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
			return nil, err
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

func (s *source) readOverlayDir(rel string) ([]sourceEntry, error) {
	type visibleEntry struct {
		realPath string
		info     os.FileInfo
	}

	layerRel := cleanRel(filepath.Join(s.baseRel, rel))
	visible := map[string]visibleEntry{}
	hidden := map[string]struct{}{}

	for i := len(s.layers) - 1; i >= 0; i-- {
		layerDir := filepath.Join(s.layers[i], layerRel)
		opaque, err := isOpaqueDir(layerDir)
		if err != nil && !os.IsNotExist(err) && !isNotDir(err) {
			return nil, err
		}

		dirents, err := os.ReadDir(layerDir)
		if os.IsNotExist(err) || isNotDir(err) {
			if opaque {
				break
			}
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
		if opaque {
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
		})
	}
	return entries, nil
}

func (s *source) realPath(rel string, viewInfo os.FileInfo) (string, os.FileInfo, error) {
	rel = cleanRel(filepath.Join(s.baseRel, rel))
	if !s.overlay {
		realPath := filepath.Join(s.realRoot, rel)
		realInfo, err := os.Lstat(realPath)
		if err != nil {
			return "", nil, err
		}
		return realPath, realInfo, nil
	}

	if viewInfo != nil && viewInfo.IsDir() {
		// Directory contents are walked from the mounted view; metadata can be
		// copied from any visible instance, with uppermost preferred.
	}
	for i := len(s.layers) - 1; i >= 0; i-- {
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

func rootPath(root, p string, followFinalSymlink bool) (string, error) {
	p = filepath.Join("/", p)
	if p == "/" {
		return root, nil
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

func isWhiteout(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	if st.Mode&syscall.S_IFMT != syscall.S_IFCHR {
		return false
	}
	return unix.Major(uint64(st.Rdev)) == 0 && unix.Minor(uint64(st.Rdev)) == 0
}

func isOpaqueDir(path string) (bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		return false, nil
	}
	for _, key := range []string{"trusted.overlay.opaque", "user.overlay.opaque"} {
		val, err := sysx.LGetxattr(path, key)
		if err == unix.ENODATA || err == unix.ENOTSUP {
			continue
		}
		if err != nil {
			return false, err
		}
		if len(val) == 1 && val[0] == 'y' {
			return true, nil
		}
	}
	return false, nil
}

func isNotDir(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "not a directory") || os.IsNotExist(err))
}
