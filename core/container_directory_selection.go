package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	"github.com/containerd/containerd/v2/core/mount"
	containerdfs "github.com/containerd/continuity/fs"
	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/util/layercopy"
)

// materializeSelection builds the container's snapshot-backed mount view before
// resolving links. Mounts replace their targets; copying their contents over the
// rootfs without removing the target would expose files hidden by the mount.
func (lazy *ContainerDirectoryLazy) materializeSelection(ctx context.Context, dir *Directory) error {
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return err
	}
	parent := lazy.Parent.Self()
	parts := []dagql.PartKey{ContainerPartFS}
	for _, mnt := range parent.Mounts {
		if mnt.DirectorySource == nil && mnt.FileSource == nil && mnt.FilesSource == nil {
			return fmt.Errorf("resolveBrokenSymlinks: cannot retrieve path from non-snapshot mount %s", mnt.Target)
		}
		parts = append(parts, ContainerPartMount(mnt.Target))
	}
	// Secrets and sockets are exec resources, not snapshot contents. Never fall
	// through to a rootfs file hidden by one of these resources.
	if slices.ContainsFunc(parent.Secrets, func(secret ContainerSecret) bool { return secret.MountPath != "" }) || len(parent.Sockets) != 0 {
		return fmt.Errorf("resolveBrokenSymlinks: cannot retrieve paths with secret or socket mounts")
	}
	if err := cache.EvaluateParts(ctx, lazy.Parent, parts...); err != nil {
		return err
	}
	rootfs, ok := parent.FS.Peek()
	if !ok || rootfs == nil {
		return fmt.Errorf("container directory selection: missing rootfs")
	}
	mounts := parent.Mounts
	// A directory mounted at / becomes the base view, including its metadata.
	for i, mnt := range mounts {
		if mnt.Target == "/" {
			if mnt.DirectorySource == nil {
				return fmt.Errorf("container root mount is not a directory")
			}
			rootfs, ok = mnt.DirectorySource.Peek()
			if !ok || rootfs == nil {
				return fmt.Errorf("container root mount has no source")
			}
			mounts = mounts[i+1:]
			break
		}
	}
	ref, rootPath, err := materializedDirectorySnapshotAndPath(rootfs)
	if err != nil {
		return err
	}
	dir.Platform = parent.Platform
	dir.Services = slices.Clone(parent.Services)
	selected := absPath(parent.Config.WorkingDir, lazy.Path)
	if len(mounts) == 0 {
		return dir.materializeSubdirectory(ctx, ref, rootPath, selected)
	}
	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}
	view, err := query.SnapshotManager().New(ctx, ref, bkcache.WithDescription("Container.directory mount view"))
	if err != nil {
		return err
	}
	defer view.Release(context.WithoutCancel(ctx))
	err = MountRef(ctx, view, func(root string, _ *mount.Mount) error {
		return copySelectionMounts(ctx, root, rootPath, mounts)
	})
	if err != nil {
		return err
	}
	composed, err := view.Commit(ctx)
	if err != nil {
		return err
	}
	defer composed.Release(context.WithoutCancel(ctx))
	return dir.materializeSubdirectory(ctx, composed, rootPath, selected)
}

func copySelectionMounts(ctx context.Context, root, rootPath string, mounts ContainerMounts) error {
	parentPath, err := containerdfs.RootPath(root, rootPath)
	if err != nil {
		return err
	}
	parentRel, err := filepath.Rel(root, parentPath)
	if err != nil {
		return err
	}
	pinned, err := os.OpenInRoot(root, parentRel)
	if err != nil {
		return err
	}
	defer pinned.Close()
	boundary := fmt.Sprintf("/proc/self/fd/%d", pinned.Fd())
	handle, err := os.OpenRoot(boundary)
	if err != nil {
		return err
	}
	defer handle.Close()
	ancestors := map[string]*syscall.Stat_t{}
	for _, mnt := range mounts {
		target, err := containerdfs.RootPath(boundary, mnt.Target)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(boundary, target)
		if err != nil {
			return err
		}
		if rel == "." {
			return fmt.Errorf("mount %s resolves to the container root", mnt.Target)
		}
		// Removing and copying mountpoints must not change the timestamps
		// of their existing parents in the container view.
		for ancestor := filepath.Dir(rel); ; ancestor = filepath.Dir(ancestor) {
			if _, seen := ancestors[ancestor]; !seen {
				info, err := handle.Stat(ancestor)
				if err != nil && !os.IsNotExist(err) {
					return err
				}
				if err == nil {
					ancestors[ancestor] = info.Sys().(*syscall.Stat_t)
				}
			}
			if ancestor == "." {
				break
			}
		}
		if err := handle.RemoveAll(rel); err != nil {
			return err
		}
		// Keep writes in the mounted view, like RemoveAll and Chtimes. Mixing
		// these operations with raw upper-layer writes leaves whiteouts in
		// the way and bypasses overlayfs inode and copy-up state.
		// A fresh copier also discards caches for the removed mount target.
		copier, err := layercopy.NewCopier(layercopy.Mount{Root: root})
		if err != nil {
			return err
		}
		err = copySelectionMount(ctx, copier, mnt, filepath.Join(parentRel, rel))
		closeErr := copier.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	for path, info := range ancestors {
		if err := handle.Chtimes(path, time.Unix(info.Atim.Sec, info.Atim.Nsec), time.Unix(info.Mtim.Sec, info.Mtim.Nsec)); err != nil {
			return err
		}
	}
	return nil
}

func copySelectionMount(ctx context.Context, copier *layercopy.Copier, mnt ContainerMount, target string) error {
	switch {
	case mnt.DirectorySource != nil:
		source, ok := mnt.DirectorySource.Peek()
		if !ok || source == nil {
			return fmt.Errorf("mount %s has no directory source", mnt.Target)
		}
		ref, path, err := materializedDirectorySnapshotAndPath(source)
		if err != nil {
			return err
		}
		return MountRef(ctx, ref, func(root string, m *mount.Mount) error {
			return copier.Copy(ctx, layercopy.Mount{Root: root, Mount: m}, path, target, layercopy.CopyOptions{CopyDirContents: true, PreserveRootMetadata: true})
		}, mountRefAsReadOnly)
	case mnt.FileSource != nil:
		source, ok := mnt.FileSource.Peek()
		if !ok || source == nil {
			return fmt.Errorf("mount %s has no file source", mnt.Target)
		}
		ref, ok := source.Snapshot.Peek()
		if !ok {
			return fmt.Errorf("mount %s has no file snapshot", mnt.Target)
		}
		path, ok := source.File.Peek()
		if !ok {
			return fmt.Errorf("mount %s has no file path", mnt.Target)
		}
		return copySelectionMountFile(ctx, copier, ref, path, target)
	case mnt.FilesSource != nil:
		files, err := execMountedFiles(ctx, mnt.FilesSource)
		if err != nil {
			return err
		}
		if err := copier.Mkdir(ctx, target, layercopy.CopyOptions{}); err != nil {
			return err
		}
		for _, file := range files.Files {
			if err := copySelectionMountFile(ctx, copier, file.Ref, file.Path, filepath.Join(target, file.Name)); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported mount %s", mnt.Target)
	}
}

func copySelectionMountFile(ctx context.Context, copier *layercopy.Copier, ref bkcache.ImmutableRef, path, target string) error {
	return MountRef(ctx, ref, func(root string, m *mount.Mount) error {
		return copier.CopyFile(ctx, layercopy.Mount{Root: root, Mount: m}, path, target, layercopy.CopyOptions{})
	}, mountRefAsReadOnly)
}
