package fsxutil

import (
	"path/filepath"
	"strings"

	continuityfs "github.com/containerd/continuity/fs"
)

// RootPath resolves p under root as continuity's fs.RootPath does, following
// symlinks within root. A path that holds no symlinks, the common case, is
// settled with one lookup where continuity's walk takes an lstat, and several
// allocations, per component; any other path is walked by continuity.
func RootPath(root, p string) (string, error) {
	if resolved, ok := RootPathWithoutSymlinks(root, p, true); ok {
		return resolved, nil
	}
	return continuityfs.RootPath(root, p)
}

// RootPathWithoutSymlinks settles, with one lookup, the common case of a path
// that holds no symlinks, which resolves to itself under root. A final symlink
// counts only when followFinalSymlink is set. ok is false when the lookup can't
// settle it: a symlink anywhere in the path, root's own components included,
// any error but a missing component, a ".." component, or a platform without
// the lookup. Every component before a missing one is a directory, and
// continuity's walk takes the missing ones as they are. Continuity takes a ".."
// after the symlinks before it, so cleaning it away first could change the
// answer.
func RootPathWithoutSymlinks(root, p string, followFinalSymlink bool) (string, bool) {
	if hasDotDot(p) {
		return "", false
	}
	// Joining cleans, and cleaning allocates; a clean absolute path is what
	// the join would give.
	if !filepath.IsAbs(p) || filepath.Clean(p) != p {
		p = filepath.Join("/", p)
	}
	if p == "/" {
		return "", false
	}
	full := root + p
	if filepath.Clean(full) != full {
		full = filepath.Join(root, p)
	}
	if !resolvesWithoutSymlinks(full, followFinalSymlink) {
		return "", false
	}
	return full, true
}

func hasDotDot(p string) bool {
	for p != "" {
		var elem string
		elem, p, _ = strings.Cut(p, "/")
		if elem == ".." {
			return true
		}
	}
	return false
}
