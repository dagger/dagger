package layercopy

import (
	"fmt"
	"path/filepath"
	"strings"

	continuityfs "github.com/containerd/continuity/fs"
)

// BoundedRootPath resolves p, a path in the filesystem mounted at root, as
// continuity's RootPath does, but with boundary, a directory in that
// filesystem, as the root for paths under it: their ".." stops at boundary,
// and their symlinks, absolute ones included, resolve inside it. boundary must
// already be resolved, so it holds no symlinks; "" or "/" is the whole
// filesystem. Ancestors of boundary resolve to themselves, and any other path
// is an error. If followFinalSymlink is false, a final symlink is left
// unresolved.
func BoundedRootPath(root, boundary, p string, followFinalSymlink bool) (string, error) {
	p = filepath.Join("/", p)
	boundary = filepath.Join("/", boundary)
	if boundary != "/" {
		rel, err := filepath.Rel(boundary, p)
		if err != nil {
			return "", err
		}
		switch {
		case rel != ".." && !strings.HasPrefix(rel, "../"):
			root = filepath.Join(root, boundary)
			p = filepath.Join("/", rel)
		case p == "/" || strings.HasPrefix(boundary, p+"/"):
			// boundary holds no symlinks, so neither do its ancestors.
			return filepath.Join(root, p), nil
		default:
			return "", fmt.Errorf("path %q is outside %q", p, boundary)
		}
	}
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
