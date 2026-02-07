//go:build !darwin && !windows

package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/containerd/containerd/v2/core/mount"
	containerdfs "github.com/containerd/continuity/fs"
	"github.com/dagger/dagger/internal/buildkit/util/overlay"
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
	case "overlay":
		overlayDirs, err := overlay.GetOverlayLayers(*m)
		if err != nil {
			return nil, fmt.Errorf("failed to get overlay layers: %w", err)
		}
		return func(p string) (string, error) {
			if mntedPath != "" {
				var err error
				p, err = filepath.Rel(mntedPath, p)
				if err != nil {
					return "", err
				}
			}
			// overlayDirs is lower->upper, so iterate in reverse to check
			// upper layers first
			var resolvedUpperdirPath string
			for i := len(overlayDirs) - 1; i >= 0; i-- {
				layerRoot := overlayDirs[i]
				resolvedPath, err := containerdfs.RootPath(layerRoot, p)
				if err != nil {
					return "", err
				}
				if i == len(overlayDirs)-1 {
					resolvedUpperdirPath = resolvedPath
				}
				_, err = os.Lstat(resolvedPath)
				switch {
				case err == nil:
					return resolvedPath, nil
				case errors.Is(err, os.ErrNotExist):
					// try next layer
				default:
					return "", fmt.Errorf("failed to stat path %s in overlay layer: %w", resolvedPath, err)
				}
			}
			// path doesn't exist, so if it's gonna exist, it should be in the upperdir
			return resolvedUpperdirPath, nil
		}, nil
	default:
		return nil, nil
	}
}
