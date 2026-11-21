package dangv2

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/dagger/dagger/core"
	"github.com/vito/dang/v2/pkg/dang"
)

// stagedDangSource is a module's Dang source, staged where Dang runs it from.
//
// A module's source is mounted from its snapshot at a temporary path, and a
// mount nobody uses is released after a linger period: the same unchanged
// source is mounted again at a new path. Dang keeps the path of every file in
// what it compiles, to report errors at, so a compilation of the mounted
// source could only be reused while that mount lasts, and its errors would
// name an anonymous mount. Staging copies the source's .dang files, the only
// files Dang reads from it, to a directory named after the module and their
// content, which stays the same for as long as the content does:
//
//	<tmp>/dagger-dang/<module>-<digest>/<source subpath>/main.dang
type stagedDangSource struct {
	// dir holds the staged .dang files.
	dir string
	// version is the Dang version the source is written in, as the
	// directory it was staged from decides it. No dang.toml governs dir, so
	// the source must be run with ContextWithVersion(version).
	version dang.Version
	// release ends this use of dir.
	release func()
}

// runStagedDangSource runs the source of the module src in srcDir with run,
// from where it is staged (see stageDangSource), and with its version. When
// the source cannot be staged, run gets srcDir itself, and staged is false.
func runStagedDangSource(ctx context.Context, src *core.ModuleSource, srcDir string, run func(ctx context.Context, source stagedDangSource, staged bool) (dang.ValueScope, error)) (dang.ValueScope, error) {
	source, ok := stageDangSource(ctx, srcDir, src.ModuleOriginalName, src.SourceSubpath)
	if !ok {
		return run(ctx, stagedDangSource{dir: srcDir}, false)
	}
	defer source.release()
	return run(dang.ContextWithVersion(ctx, source.version), source, true)
}

// stageDangSource stages the module source in srcDir, the module's source
// subpath of its context, for the module named moduleName. It returns false
// when the source cannot be staged (it has no .dang files, they cannot all be
// read, or written); the source then runs from srcDir.
func stageDangSource(ctx context.Context, srcDir, moduleName, subpath string) (stagedDangSource, bool) {
	files, complete := readDangSourceFiles(srcDir)
	if !complete || len(files) == 0 {
		return stagedDangSource{}, false
	}
	version, err := dang.VersionForDir(ctx, srcDir)
	if err != nil {
		return stagedDangSource{}, false
	}
	subpath = filepath.Clean(subpath)
	if !filepath.IsLocal(subpath) {
		subpath = "."
	}
	name := stagedDangName(moduleName)
	h := sha256.New()
	writeField := func(b []byte) {
		var size [8]byte
		binary.LittleEndian.PutUint64(size[:], uint64(len(b)))
		h.Write(size[:])
		h.Write(b)
	}
	writeField([]byte(name))
	writeField([]byte(subpath))
	for _, file := range files {
		writeField([]byte(file.name))
		writeField(file.content)
	}
	root := filepath.Join(stagedDangSources.root, name+"-"+hex.EncodeToString(h.Sum(nil))[:32])
	dir, release, err := stagedDangSources.use(root, subpath, files)
	if err != nil {
		slog.Warn("stage Dang module source", "module", moduleName, "error", err)
		return stagedDangSource{}, false
	}
	return stagedDangSource{dir: dir, version: version, release: release}, true
}

// stagedDangName is moduleName as a directory name.
func stagedDangName(moduleName string) string {
	name := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		}
		return '_'
	}, moduleName)
	if name == "" {
		return "module"
	}
	return name
}

// maxStagedDangSources bounds the staged sources nobody is using that are
// kept. A staged source is a module's .dang files.
const maxStagedDangSources = 256

var stagedDangSources = newStagedDangDirs(filepath.Join(os.TempDir(), "dagger-dang"), maxStagedDangSources)

// stagedDangDirs keeps the directories sources are staged in, engine-wide:
// identical sources share one. The directories nobody is using beyond max are
// removed, least recently used first. A compilation of a removed directory
// stays valid: it reads nothing from it but the source to report an error
// at, and its next use stages the source again first.
type stagedDangDirs struct {
	root string
	max  int

	mu      sync.Mutex
	entries map[string]*list.Element
	// lru holds a *stagedDangDir per directory, most recently used first.
	lru *list.List
}

type stagedDangDir struct {
	root string
	// users counts the uses of the directory in progress.
	users int
}

func newStagedDangDirs(root string, limit int) *stagedDangDirs {
	return &stagedDangDirs{root: root, max: limit, entries: map[string]*list.Element{}, lru: list.New()}
}

// use returns the directory subpath of root, holding files, until release is
// called. The directory is written if this process has not written it yet,
// or it is gone.
func (s *stagedDangDirs) use(root, subpath string, files []dangSourceFile) (string, func(), error) {
	dir := filepath.Join(root, subpath)
	s.mu.Lock()
	defer s.mu.Unlock()
	elem, ok := s.entries[root]
	if ok {
		s.lru.MoveToFront(elem)
		if _, err := os.Stat(filepath.Join(dir, files[0].name)); err != nil {
			// Removed by someone else: write it again.
			if err := writeStagedDangFiles(dir, files); err != nil {
				return "", nil, err
			}
		}
	} else {
		elem = s.lru.PushFront(&stagedDangDir{root: root})
		s.entries[root] = elem
		// Whatever is there is left from an earlier engine.
		if err := errors.Join(os.RemoveAll(root), writeStagedDangFiles(dir, files)); err != nil {
			s.removeLocked(elem)
			return "", nil, err
		}
	}
	entry := elem.Value.(*stagedDangDir)
	entry.users++
	s.evictLocked()
	var once sync.Once
	return dir, func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			entry.users--
			s.evictLocked()
		})
	}, nil
}

func writeStagedDangFiles(dir string, files []dangSourceFile) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	for _, file := range files {
		if err := os.WriteFile(filepath.Join(dir, file.name), file.content, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// evictLocked removes the least recently used directories nobody is using
// beyond the bound.
func (s *stagedDangDirs) evictLocked() {
	for elem := s.lru.Back(); elem != nil && s.lru.Len() > s.max; {
		prev := elem.Prev()
		if elem.Value.(*stagedDangDir).users == 0 {
			s.removeLocked(elem)
		}
		elem = prev
	}
}

func (s *stagedDangDirs) removeLocked(elem *list.Element) {
	entry := elem.Value.(*stagedDangDir)
	s.lru.Remove(elem)
	delete(s.entries, entry.root)
	os.RemoveAll(entry.root)
}
