package core

import (
	"context"
	"fmt"

	ctrdmount "github.com/containerd/containerd/v2/core/mount"

	"github.com/dagger/dagger/engine/cachevolumedir"
	bkcache "github.com/dagger/dagger/engine/snapshots"
)

// pinnedCacheVolumeDir is the contents directory of a directory-backed cache
// volume that lockMountedCaches pinned for the exec.
func pinnedCacheVolumeDir(ctx context.Context, dir *cachevolumedir.Store, cache *CacheVolume) (string, error) {
	ident, err := cache.dirIdentity(ctx)
	if err != nil {
		return "", err
	}
	path, err := dir.PinnedPath(ident)
	if err != nil {
		return "", fmt.Errorf("mount cache volume %q: %w", cache.Key, err)
	}
	return path, nil
}

// prepareExecCacheDirMount bind-mounts a directory-backed cache volume, like
// an engine volume.
func prepareExecCacheDirMount(ctx context.Context, path string) (bkcache.Mountable, error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, fmt.Errorf("get current query for cache volume mount: %w", err)
	}
	return &execCacheDirMount{
		path:              path,
		recursiveReadOnly: query.EngineVolumeState().RecursiveReadOnlySupported,
	}, nil
}

type execCacheDirMount struct {
	path              string
	recursiveReadOnly bool
}

func (mnt *execCacheDirMount) Mount(_ context.Context, readonly bool) (bkcache.MountableRef, error) {
	options := []string{"rbind"}
	if readonly {
		if mnt.recursiveReadOnly {
			options = append(options, "rro")
		} else {
			options = append(options, "ro")
		}
	}
	return &execVolumeMountInstance{mounts: []ctrdmount.Mount{{
		Type:    "bind",
		Source:  mnt.path,
		Options: options,
	}}, release: func() error { return nil }}, nil
}
