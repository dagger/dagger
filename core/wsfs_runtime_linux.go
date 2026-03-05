//go:build linux

package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	containerdmount "github.com/containerd/containerd/v2/core/mount"
	"github.com/dagger/dagger/internal/buildkit/executor"
	"github.com/dagger/dagger/internal/buildkit/snapshot"
	"github.com/docker/docker/pkg/idtools"
	"github.com/hanwen/go-fuse/v2/fuse"
	"github.com/hanwen/go-fuse/v2/fuse/nodefs"
	"github.com/hanwen/go-fuse/v2/fuse/pathfs"
)

func setupWSFSMountsImpl(
	ctx context.Context,
	container *Container,
	mounts ContainerMountData,
	execMounts []executor.Mount,
) (func() error, error) {
	_ = mounts

	workspaceByTarget := make(map[string]*WorkspaceMountSource)
	for _, mount := range container.Mounts {
		if mount.WorkspaceSource != nil {
			workspaceByTarget[mount.Target] = mount.WorkspaceSource
		}
	}
	if len(workspaceByTarget) == 0 {
		return func() error { return nil }, nil
	}

	ctx = context.WithoutCancel(ctx)
	for i := range execMounts {
		workspace := workspaceByTarget[execMounts[i].Dest]
		if workspace == nil {
			continue
		}
		if execMounts[i].Src == nil {
			return nil, fmt.Errorf("workspace mount %q has no mount source", execMounts[i].Dest)
		}

		execMounts[i].Src = &wsfsMountable{
			base:      execMounts[i].Src,
			workspace: workspace,
			ctx:       ctx,
		}
	}

	return func() error { return nil }, nil
}

type wsfsMountable struct {
	base      executor.Mountable
	workspace *WorkspaceMountSource
	ctx       context.Context
}

func (m *wsfsMountable) Mount(ctx context.Context, readonly bool) (executor.MountableRef, error) {
	baseRef, err := m.base.Mount(ctx, readonly)
	if err != nil {
		return nil, err
	}
	return &wsfsMountableRef{
		base:      baseRef,
		workspace: m.workspace,
		ctx:       m.ctx,
		readonly:  readonly,
	}, nil
}

type wsfsMountableRef struct {
	base      executor.MountableRef
	workspace *WorkspaceMountSource
	ctx       context.Context
	readonly  bool
}

func (r *wsfsMountableRef) IdentityMapping() *idtools.IdentityMapping {
	return r.base.IdentityMapping()
}

func (r *wsfsMountableRef) Mount() ([]containerdmount.Mount, func() error, error) {
	baseMounts, baseCleanup, err := r.base.Mount()
	if err != nil {
		return nil, nil, err
	}

	upperMounter := snapshot.LocalMounterWithMounts(baseMounts)
	upperPath, err := upperMounter.Mount()
	if err != nil {
		_ = baseCleanup()
		return nil, nil, err
	}

	fusePath, err := os.MkdirTemp("", "dagger-wsfs-")
	if err != nil {
		_ = upperMounter.Unmount()
		_ = baseCleanup()
		return nil, nil, err
	}

	wsfs := newWSFSPathFS(r.ctx, upperPath, r.workspace)
	nfs := pathfs.NewPathNodeFs(wsfs, nil)
	server, _, err := nodefs.MountRoot(fusePath, nfs.Root(), &nodefs.Options{})
	if err != nil {
		_ = os.RemoveAll(fusePath)
		_ = upperMounter.Unmount()
		_ = baseCleanup()
		return nil, nil, err
	}
	go server.Serve()
	if err := server.WaitMount(); err != nil {
		_ = server.Unmount()
		server.Wait()
		_ = os.RemoveAll(fusePath)
		_ = upperMounter.Unmount()
		_ = baseCleanup()
		return nil, nil, err
	}

	bindOpts := []string{"rbind"}
	if r.readonly {
		bindOpts = append(bindOpts, "ro")
	}

	cleanup := func() error {
		var errs error
		if err := server.Unmount(); err != nil {
			errs = errors.Join(errs, err)
		}
		server.Wait()
		if err := os.RemoveAll(fusePath); err != nil {
			errs = errors.Join(errs, err)
		}
		if err := upperMounter.Unmount(); err != nil {
			errs = errors.Join(errs, err)
		}
		if err := baseCleanup(); err != nil {
			errs = errors.Join(errs, err)
		}
		return errs
	}

	return []containerdmount.Mount{{
		Type:    "bind",
		Source:  fusePath,
		Options: bindOpts,
	}}, cleanup, nil
}

type wsfsPathFS struct {
	pathfs.FileSystem

	ctx       context.Context
	upperRoot string
	workspace *WorkspaceMountSource

	materializeMu sync.Mutex
}

func newWSFSPathFS(ctx context.Context, upperRoot string, workspace *WorkspaceMountSource) *wsfsPathFS {
	return &wsfsPathFS{
		FileSystem: pathfs.NewLoopbackFileSystem(upperRoot),
		ctx:        ctx,
		upperRoot:  upperRoot,
		workspace:  workspace,
	}
}

func (fsys *wsfsPathFS) GetAttr(name string, c *fuse.Context) (*fuse.Attr, fuse.Status) {
	if attr, status := fsys.FileSystem.GetAttr(name, c); status.Ok() {
		return attr, status
	} else if status != fuse.ENOENT {
		return nil, status
	}

	rel, status := fsys.cleanRel(name)
	if !status.Ok() {
		return nil, status
	}
	stat, err := workspaceMountStat(fsys.ctx, fsys.workspace, rel, false)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fuse.ENOENT
		}
		return nil, fuse.ToStatus(err)
	}
	return statToFuseAttr(stat), fuse.OK
}

func (fsys *wsfsPathFS) Access(name string, mode uint32, c *fuse.Context) fuse.Status {
	if status := fsys.FileSystem.Access(name, mode, c); status.Ok() {
		return status
	} else if status != fuse.ENOENT {
		return status
	}

	rel, status := fsys.cleanRel(name)
	if !status.Ok() {
		return status
	}
	_, err := workspaceMountStat(fsys.ctx, fsys.workspace, rel, false)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fuse.ENOENT
		}
		return fuse.ToStatus(err)
	}
	return fuse.OK
}

func (fsys *wsfsPathFS) OpenDir(name string, c *fuse.Context) ([]fuse.DirEntry, fuse.Status) {
	upperEntries, upperStatus := fsys.FileSystem.OpenDir(name, c)
	if upperStatus != fuse.OK && upperStatus != fuse.ENOENT {
		return nil, upperStatus
	}

	rel, status := fsys.cleanRel(name)
	if !status.Ok() {
		return nil, status
	}

	workspaceEntries, err := workspaceMountEntries(fsys.ctx, fsys.workspace, rel)
	if err != nil {
		if upperStatus == fuse.OK {
			return upperEntries, fuse.OK
		}
		if errors.Is(err, os.ErrNotExist) {
			return nil, fuse.ENOENT
		}
		return nil, fuse.ToStatus(err)
	}

	out := make([]fuse.DirEntry, 0, len(upperEntries)+len(workspaceEntries))
	seen := make(map[string]struct{}, len(upperEntries)+len(workspaceEntries))
	for _, entry := range upperEntries {
		out = append(out, entry)
		seen[entry.Name] = struct{}{}
	}

	for _, rawName := range workspaceEntries {
		entryName := strings.TrimSuffix(rawName, "/")
		if entryName == "" {
			continue
		}
		if _, ok := seen[entryName]; ok {
			continue
		}

		childRel := entryName
		if rel != "." {
			childRel = path.Join(rel, entryName)
		}
		stat, err := workspaceMountStat(fsys.ctx, fsys.workspace, childRel, false)
		if err != nil {
			continue
		}

		out = append(out, fuse.DirEntry{
			Name: entryName,
			Mode: statToFuseMode(stat),
		})
		seen[entryName] = struct{}{}
	}

	return out, fuse.OK
}

func (fsys *wsfsPathFS) Open(name string, flags uint32, c *fuse.Context) (nodefs.File, fuse.Status) {
	if f, status := fsys.FileSystem.Open(name, flags, c); status.Ok() {
		return f, status
	} else if status != fuse.ENOENT {
		return nil, status
	}

	if flags&uint32(syscall.O_ACCMODE) != uint32(os.O_RDONLY) {
		return nil, fuse.ENOENT
	}

	if err := fsys.materializeWorkspaceFile(name); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fuse.ENOENT
		}
		return nil, fuse.ToStatus(err)
	}

	return fsys.FileSystem.Open(name, flags, c)
}

func (fsys *wsfsPathFS) materializeWorkspaceFile(name string) error {
	rel, status := fsys.cleanRel(name)
	if !status.Ok() {
		return syscall.EINVAL
	}

	fsys.materializeMu.Lock()
	defer fsys.materializeMu.Unlock()

	target := filepath.Join(fsys.upperRoot, filepath.FromSlash(rel))
	if _, err := os.Lstat(target); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	stat, err := workspaceMountStat(fsys.ctx, fsys.workspace, rel, false)
	if err != nil {
		return err
	}
	if stat.FileType != FileTypeRegular {
		return os.ErrNotExist
	}

	file, err := workspaceMountFile(fsys.ctx, fsys.workspace, rel)
	if err != nil {
		return err
	}
	contents, err := file.Contents(fsys.ctx, nil, nil)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.WriteFile(target, contents, os.FileMode(stat.Permissions))
}

func (fsys *wsfsPathFS) cleanRel(name string) (string, fuse.Status) {
	clean := strings.TrimPrefix(path.Clean("/"+name), "/")
	if clean == "" || clean == "." {
		return ".", fuse.OK
	}
	if strings.HasPrefix(clean, "../") {
		return "", fuse.EINVAL
	}
	return clean, fuse.OK
}

func statToFuseMode(stat *Stat) uint32 {
	mode := uint32(stat.Permissions)
	switch stat.FileType {
	case FileTypeDirectory:
		mode |= syscall.S_IFDIR
	case FileTypeSymlink:
		mode |= syscall.S_IFLNK
	default:
		mode |= syscall.S_IFREG
	}
	return mode
}

func statToFuseAttr(stat *Stat) *fuse.Attr {
	return &fuse.Attr{
		Mode: statToFuseMode(stat),
		Size: uint64(stat.Size),
	}
}
