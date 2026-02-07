//go:build darwin || windows

package core

import (
	"path/filepath"

	"github.com/containerd/containerd/v2/core/mount"
	containerdfs "github.com/containerd/continuity/fs"
	fscopy "github.com/dagger/dagger/internal/fsutil/copy"
)

func pathResolverForMount(
	m *mount.Mount,
	mntedPath string, // if set, paths will be assumed to be provided as seen from under mntedPath
) (fscopy.PathResolver, error) {
	if m == nil {
		return nil, nil
	}
	switch m.Type {
	case "bind", "rbind":
		return func(p string) (string, error) {
			if mntedPath != "" {
				var err error
				p, err = filepath.Rel(mntedPath, p)
				if err != nil {
					return "", err
				}
			}
			return containerdfs.RootPath(m.Source, p)
		}, nil
	default:
		return nil, nil
	}
}
