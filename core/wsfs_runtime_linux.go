//go:build linux

package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	containerdmount "github.com/containerd/containerd/v2/core/mount"
	"github.com/dagger/dagger/engine"
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
	server, _, err := nodefs.Mount(
		fusePath,
		nfs.Root(),
		&fuse.MountOptions{
			// Avoid hard dependency on fusermount in the engine image.
			// The engine already runs with mount capabilities required for direct mount.
			DirectMount: true,
		},
		&nodefs.Options{},
	)
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
		if err := wsfs.stopLiveReadLoop(); err != nil {
			errs = errors.Join(errs, err)
		}
		if err := server.Unmount(); err != nil {
			errs = errors.Join(errs, err)
		}
		server.Wait()
		if !r.readonly && r.workspace.Export {
			if err := wsfs.syncWriteThrough(); err != nil {
				errs = errors.Join(errs, err)
			}
		}
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
	journal       *wsfsWriteJournal

	liveReadMu      sync.Mutex
	liveReadPaths   map[string]struct{}
	liveReadCancel  context.CancelFunc
	liveReadDoneCh  chan struct{}
	liveReadRunning bool
}

const wsfsLiveReadRefreshInterval = 250 * time.Millisecond

func newWSFSPathFS(ctx context.Context, upperRoot string, workspace *WorkspaceMountSource) *wsfsPathFS {
	fsys := &wsfsPathFS{
		FileSystem:    pathfs.NewLoopbackFileSystem(upperRoot),
		ctx:           ctx,
		upperRoot:     upperRoot,
		workspace:     workspace,
		journal:       newWSFSWriteJournal(),
		liveReadPaths: map[string]struct{}{},
	}
	if workspace != nil && workspace.LiveRead {
		loopCtx, cancel := context.WithCancel(ctx)
		doneCh := make(chan struct{})
		fsys.liveReadCancel = cancel
		fsys.liveReadDoneCh = doneCh
		fsys.liveReadRunning = true
		go fsys.liveReadLoop(loopCtx, doneCh)
	}
	return fsys
}

func (fsys *wsfsPathFS) stopLiveReadLoop() error {
	fsys.liveReadMu.Lock()
	if !fsys.liveReadRunning {
		fsys.liveReadMu.Unlock()
		return nil
	}

	cancel := fsys.liveReadCancel
	doneCh := fsys.liveReadDoneCh
	fsys.liveReadRunning = false
	fsys.liveReadCancel = nil
	fsys.liveReadDoneCh = nil
	fsys.liveReadMu.Unlock()

	if cancel != nil {
		cancel()
	}
	if doneCh != nil {
		<-doneCh
	}

	return nil
}

func (fsys *wsfsPathFS) liveReadLoop(ctx context.Context, doneCh chan struct{}) {
	defer close(doneCh)

	ticker := time.NewTicker(wsfsLiveReadRefreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		for _, rel := range fsys.snapshotLiveReadPaths() {
			if ctx.Err() != nil {
				return
			}
			if fsys.journal.isShadowed(rel) {
				continue
			}
			// Best-effort refresh: keep serving local state even if workspace calls fail.
			if err := fsys.refreshWorkspaceFile(rel); err != nil && !errors.Is(err, os.ErrNotExist) {
				continue
			}
		}
	}
}

func (fsys *wsfsPathFS) trackLiveReadPath(rel string) {
	if rel == "." || fsys.workspace == nil || !fsys.workspace.LiveRead {
		return
	}

	fsys.liveReadMu.Lock()
	defer fsys.liveReadMu.Unlock()
	if !fsys.liveReadRunning {
		return
	}
	fsys.liveReadPaths[rel] = struct{}{}
}

func (fsys *wsfsPathFS) untrackLiveReadPath(rel string, includeChildren bool) {
	if rel == "." {
		return
	}

	fsys.liveReadMu.Lock()
	defer fsys.liveReadMu.Unlock()
	if !includeChildren {
		delete(fsys.liveReadPaths, rel)
		return
	}

	for trackedRel := range fsys.liveReadPaths {
		if wsfsPathHasPrefix(trackedRel, rel) {
			delete(fsys.liveReadPaths, trackedRel)
		}
	}
}

func (fsys *wsfsPathFS) snapshotLiveReadPaths() []string {
	fsys.liveReadMu.Lock()
	defer fsys.liveReadMu.Unlock()

	paths := make([]string, 0, len(fsys.liveReadPaths))
	for rel := range fsys.liveReadPaths {
		paths = append(paths, rel)
	}
	sort.Strings(paths)
	return paths
}

func (fsys *wsfsPathFS) GetAttr(name string, c *fuse.Context) (*fuse.Attr, fuse.Status) {
	rel, status := fsys.cleanRel(name)
	if !status.Ok() {
		return nil, status
	}

	if fsys.journal.isDeleted(rel) {
		return nil, fuse.ENOENT
	}

	if fsys.workspace != nil && fsys.workspace.LiveRead && !fsys.journal.isShadowed(rel) {
		stat, err := fsys.workspaceStat(rel)
		if err == nil {
			return statToFuseAttr(stat), fuse.OK
		}
		if errors.Is(err, os.ErrNotExist) {
			return nil, fuse.ENOENT
		}
	}

	if attr, status := fsys.FileSystem.GetAttr(name, c); status.Ok() {
		return attr, status
	} else if status != fuse.ENOENT {
		return nil, status
	}

	stat, err := fsys.workspaceStat(rel)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fuse.ENOENT
		}
		return nil, fuse.ToStatus(err)
	}
	return statToFuseAttr(stat), fuse.OK
}

func (fsys *wsfsPathFS) Access(name string, mode uint32, c *fuse.Context) fuse.Status {
	rel, status := fsys.cleanRel(name)
	if !status.Ok() {
		return status
	}

	if fsys.journal.isDeleted(rel) {
		return fuse.ENOENT
	}

	if fsys.workspace != nil && fsys.workspace.LiveRead && !fsys.journal.isShadowed(rel) {
		_, err := fsys.workspaceStat(rel)
		if err == nil {
			return fuse.OK
		}
		if errors.Is(err, os.ErrNotExist) {
			return fuse.ENOENT
		}
	}

	if status := fsys.FileSystem.Access(name, mode, c); status.Ok() {
		return status
	} else if status != fuse.ENOENT {
		return status
	}

	_, err := fsys.workspaceStat(rel)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fuse.ENOENT
		}
		return fuse.ToStatus(err)
	}
	return fuse.OK
}

func (fsys *wsfsPathFS) OpenDir(name string, c *fuse.Context) ([]fuse.DirEntry, fuse.Status) {
	rel, status := fsys.cleanRel(name)
	if !status.Ok() {
		return nil, status
	}

	if fsys.journal.isDeleted(rel) {
		return nil, fuse.ENOENT
	}

	upperEntries, upperStatus := fsys.FileSystem.OpenDir(name, c)
	if upperStatus != fuse.OK && upperStatus != fuse.ENOENT {
		return nil, upperStatus
	}

	workspaceEntries, err := workspaceMountEntries(fsys.ctx, fsys.workspace, rel, fsys.workspace != nil && fsys.workspace.LiveRead)
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
		childRel := entry.Name
		if rel != "." {
			childRel = path.Join(rel, entry.Name)
		}
		if fsys.workspace != nil && fsys.workspace.LiveRead && !fsys.journal.isShadowed(childRel) {
			if _, err := fsys.workspaceStat(childRel); errors.Is(err, os.ErrNotExist) {
				continue
			}
		}
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
		if fsys.journal.isShadowed(childRel) {
			continue
		}
		stat, err := fsys.workspaceStatMode(childRel, fsys.workspace != nil && fsys.workspace.LiveRead)
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
	rel, status := fsys.cleanRel(name)
	if !status.Ok() {
		return nil, status
	}
	if fsys.journal.isDeleted(rel) {
		return nil, fuse.ENOENT
	}

	writeIntent := wsfsWriteIntent(flags)
	if !writeIntent && fsys.workspace != nil && fsys.workspace.LiveRead && !fsys.journal.isShadowed(rel) {
		if err := fsys.refreshWorkspaceFile(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fuse.ToStatus(err)
		}
	}

	if f, status := fsys.FileSystem.Open(name, flags, c); status.Ok() {
		if !writeIntent && fsys.workspace != nil && fsys.workspace.LiveRead {
			fsys.trackLiveReadPath(rel)
		}
		return fsys.wrapTrackedFile(name, flags, f), status
	} else if status != fuse.ENOENT {
		return nil, status
	}

	if fsys.workspace != nil && fsys.workspace.LiveRead && !writeIntent {
		return nil, fuse.ENOENT
	}

	if err := fsys.materializeWorkspaceFile(name); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fuse.ENOENT
		}
		return nil, fuse.ToStatus(err)
	}

	f, status := fsys.FileSystem.Open(name, flags, c)
	return fsys.wrapTrackedFile(name, flags, f), status
}

func (fsys *wsfsPathFS) Create(name string, flags uint32, mode uint32, c *fuse.Context) (nodefs.File, fuse.Status) {
	f, status := fsys.FileSystem.Create(name, flags, mode, c)
	if status.Ok() {
		fsys.markUpsert(name)
	}
	return f, status
}

func (fsys *wsfsPathFS) Mkdir(name string, mode uint32, c *fuse.Context) fuse.Status {
	status := fsys.FileSystem.Mkdir(name, mode, c)
	if status.Ok() {
		fsys.markUpsert(name)
	}
	return status
}

func (fsys *wsfsPathFS) Mknod(name string, mode uint32, dev uint32, c *fuse.Context) fuse.Status {
	status := fsys.FileSystem.Mknod(name, mode, dev, c)
	if status.Ok() {
		fsys.markUpsert(name)
	}
	return status
}

func (fsys *wsfsPathFS) Symlink(value string, linkName string, c *fuse.Context) fuse.Status {
	status := fsys.FileSystem.Symlink(value, linkName, c)
	if status.Ok() {
		fsys.markUpsert(linkName)
	}
	return status
}

func (fsys *wsfsPathFS) Link(oldName string, newName string, c *fuse.Context) fuse.Status {
	status := fsys.FileSystem.Link(oldName, newName, c)
	if status.Ok() {
		fsys.markUpsert(newName)
	}
	return status
}

func (fsys *wsfsPathFS) Rename(oldName string, newName string, c *fuse.Context) fuse.Status {
	status := fsys.FileSystem.Rename(oldName, newName, c)
	if status.Ok() {
		fsys.markDelete(oldName)
		fsys.markUpsert(newName)
	}
	return status
}

func (fsys *wsfsPathFS) Rmdir(name string, c *fuse.Context) fuse.Status {
	status := fsys.FileSystem.Rmdir(name, c)
	if status.Ok() {
		fsys.markDelete(name)
		return status
	}
	if status != fuse.ENOENT {
		return status
	}

	rel, cleanStatus := fsys.cleanRel(name)
	if !cleanStatus.Ok() {
		return cleanStatus
	}
	stat, err := workspaceMountStat(fsys.ctx, fsys.workspace, rel, true, false)
	switch {
	case err == nil && stat.FileType == FileTypeDirectory:
		fsys.journal.markDelete(rel)
		return fuse.OK
	case err == nil:
		return fuse.ENOTDIR
	case errors.Is(err, os.ErrNotExist):
		return fuse.ENOENT
	default:
		return fuse.ToStatus(err)
	}
}

func (fsys *wsfsPathFS) Unlink(name string, c *fuse.Context) fuse.Status {
	status := fsys.FileSystem.Unlink(name, c)
	if status.Ok() {
		fsys.markDelete(name)
		return status
	}
	if status != fuse.ENOENT {
		return status
	}

	rel, cleanStatus := fsys.cleanRel(name)
	if !cleanStatus.Ok() {
		return cleanStatus
	}
	stat, err := workspaceMountStat(fsys.ctx, fsys.workspace, rel, true, false)
	switch {
	case err == nil && stat.FileType != FileTypeDirectory:
		fsys.journal.markDelete(rel)
		return fuse.OK
	case err == nil:
		return fuse.EISDIR
	case errors.Is(err, os.ErrNotExist):
		return fuse.ENOENT
	default:
		return fuse.ToStatus(err)
	}
}

func (fsys *wsfsPathFS) Truncate(name string, size uint64, c *fuse.Context) fuse.Status {
	status := fsys.FileSystem.Truncate(name, size, c)
	if status.Ok() {
		fsys.markUpsert(name)
	}
	return status
}

func (fsys *wsfsPathFS) Chmod(name string, mode uint32, c *fuse.Context) fuse.Status {
	status := fsys.FileSystem.Chmod(name, mode, c)
	if status.Ok() {
		fsys.markUpsert(name)
	}
	return status
}

func (fsys *wsfsPathFS) Chown(name string, uid uint32, gid uint32, c *fuse.Context) fuse.Status {
	status := fsys.FileSystem.Chown(name, uid, gid, c)
	if status.Ok() {
		fsys.markUpsert(name)
	}
	return status
}

func (fsys *wsfsPathFS) Utimens(name string, atime *time.Time, mtime *time.Time, c *fuse.Context) fuse.Status {
	status := fsys.FileSystem.Utimens(name, atime, mtime, c)
	if status.Ok() {
		fsys.markUpsert(name)
	}
	return status
}

func (fsys *wsfsPathFS) materializeWorkspaceFile(name string) error {
	return fsys.materializeWorkspaceFileMode(name, false)
}

func (fsys *wsfsPathFS) refreshWorkspaceFile(name string) error {
	return fsys.materializeWorkspaceFileMode(name, true)
}

func (fsys *wsfsPathFS) materializeWorkspaceFileMode(name string, refresh bool) error {
	rel, status := fsys.cleanRel(name)
	if !status.Ok() {
		return syscall.EINVAL
	}

	fsys.materializeMu.Lock()
	defer fsys.materializeMu.Unlock()

	target := filepath.Join(fsys.upperRoot, filepath.FromSlash(rel))
	if !refresh {
		if _, err := os.Lstat(target); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}

	noCache := refresh && fsys.workspace != nil && fsys.workspace.LiveRead

	stat, err := fsys.workspaceStatMode(rel, noCache)
	if err != nil {
		if refresh && errors.Is(err, os.ErrNotExist) {
			if rmErr := os.RemoveAll(target); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
				return rmErr
			}
		}
		return err
	}
	if stat.FileType != FileTypeRegular {
		if refresh {
			if rmErr := os.RemoveAll(target); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
				return rmErr
			}
		}
		return os.ErrNotExist
	}

	if refresh {
		if info, statErr := os.Lstat(target); statErr == nil {
			if !info.Mode().IsRegular() {
				if rmErr := os.RemoveAll(target); rmErr != nil {
					return rmErr
				}
			}
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}
	}

	file, err := workspaceMountFile(fsys.ctx, fsys.workspace, rel, noCache)
	if err != nil {
		return err
	}
	if fileStat, statErr := file.Stat(fsys.ctx); statErr == nil {
		stat = fileStat
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

func wsfsWriteIntent(flags uint32) bool {
	return flags&uint32(syscall.O_ACCMODE) != uint32(os.O_RDONLY) || flags&uint32(syscall.O_TRUNC) != 0
}

func (fsys *wsfsPathFS) wrapTrackedFile(name string, flags uint32, file nodefs.File) nodefs.File {
	if file == nil || !wsfsWriteIntent(flags) {
		return file
	}

	rel, status := fsys.cleanRel(name)
	if !status.Ok() || rel == "." {
		return file
	}

	tracked := &wsfsTrackedFile{
		File: file,
		mark: func() {
			fsys.journal.markUpsert(rel)
		},
	}
	if flags&uint32(syscall.O_TRUNC) != 0 {
		tracked.markOnce()
	}
	return tracked
}

func (fsys *wsfsPathFS) markUpsert(name string) {
	rel, status := fsys.cleanRel(name)
	if !status.Ok() || rel == "." {
		return
	}
	fsys.journal.markUpsert(rel)
	fsys.untrackLiveReadPath(rel, false)
}

func (fsys *wsfsPathFS) markDelete(name string) {
	rel, status := fsys.cleanRel(name)
	if !status.Ok() || rel == "." {
		return
	}
	fsys.journal.markDelete(rel)
	fsys.untrackLiveReadPath(rel, true)
}

func (fsys *wsfsPathFS) syncWriteThrough() error {
	if fsys.workspace == nil || !fsys.workspace.Export {
		return nil
	}
	ws := fsys.workspace.Workspace.Self()
	if ws == nil {
		return fmt.Errorf("workspace mount has no workspace value")
	}

	upserts, deletes := fsys.journal.snapshot()
	if len(upserts) == 0 && len(deletes) == 0 {
		return nil
	}

	syncRoot, err := os.MkdirTemp("", "dagger-wsfs-sync-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(syncRoot)

	for _, rel := range upserts {
		if err := wsfsCopyFromUpper(fsys.upperRoot, syncRoot, rel); err != nil {
			return fmt.Errorf("copy changed workspace path %q: %w", rel, err)
		}
	}

	syncCtx, err := wsfsWorkspaceContext(fsys.ctx, ws)
	if err != nil {
		return err
	}

	query, err := CurrentQuery(syncCtx)
	if err != nil {
		return err
	}
	bk, err := query.Buildkit(syncCtx)
	if err != nil {
		return fmt.Errorf("failed to get buildkit client: %w", err)
	}

	return bk.LocalDirExport(syncCtx, syncRoot, ws.Root, true, deletes)
}

func wsfsWorkspaceContext(ctx context.Context, ws *Workspace) (context.Context, error) {
	if ws == nil {
		return nil, fmt.Errorf("workspace is nil")
	}
	if ws.ClientID == "" {
		return nil, fmt.Errorf("workspace has no client ID")
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, fmt.Errorf("get current query: %w", err)
	}
	clientMetadata, err := query.SpecificClientMetadata(ctx, ws.ClientID)
	if err != nil {
		return nil, fmt.Errorf("get client metadata: %w", err)
	}
	return engine.ContextWithClientMetadata(ctx, clientMetadata), nil
}

func wsfsCopyFromUpper(upperRoot, syncRoot, rel string) error {
	src := filepath.Join(upperRoot, filepath.FromSlash(rel))
	info, err := os.Lstat(src)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}

	dst := filepath.Join(syncRoot, filepath.FromSlash(rel))
	return wsfsCopyPath(src, dst, info)
}

func wsfsCopyPath(src, dst string, info os.FileInfo) error {
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.Remove(dst); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return os.Symlink(target, dst)

	case info.IsDir():
		if err := os.MkdirAll(dst, info.Mode().Perm()); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, ent := range entries {
			childSrc := filepath.Join(src, ent.Name())
			childInfo, err := os.Lstat(childSrc)
			if err != nil {
				return err
			}
			childDst := filepath.Join(dst, ent.Name())
			if err := wsfsCopyPath(childSrc, childDst, childInfo); err != nil {
				return err
			}
		}
		return nil

	case info.Mode().IsRegular():
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		srcFile, err := os.Open(src)
		if err != nil {
			return err
		}
		defer srcFile.Close()

		dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
		if err != nil {
			return err
		}
		defer dstFile.Close()

		_, err = io.Copy(dstFile, srcFile)
		return err

	default:
		return fmt.Errorf("unsupported file type for %s", src)
	}
}

type wsfsTrackedFile struct {
	nodefs.File
	once sync.Once
	mark func()
}

func (f *wsfsTrackedFile) markOnce() {
	if f.mark != nil {
		f.once.Do(f.mark)
	}
}

func (f *wsfsTrackedFile) Write(data []byte, off int64) (uint32, fuse.Status) {
	f.markOnce()
	return f.File.Write(data, off)
}

func (f *wsfsTrackedFile) Truncate(size uint64) fuse.Status {
	f.markOnce()
	return f.File.Truncate(size)
}

func (f *wsfsTrackedFile) Chmod(perms uint32) fuse.Status {
	f.markOnce()
	return f.File.Chmod(perms)
}

func (f *wsfsTrackedFile) Chown(uid uint32, gid uint32) fuse.Status {
	f.markOnce()
	return f.File.Chown(uid, gid)
}

func (f *wsfsTrackedFile) Utimens(atime *time.Time, mtime *time.Time) fuse.Status {
	f.markOnce()
	return f.File.Utimens(atime, mtime)
}

func (f *wsfsTrackedFile) Allocate(off uint64, size uint64, mode uint32) fuse.Status {
	f.markOnce()
	return f.File.Allocate(off, size, mode)
}

type wsfsWriteJournal struct {
	mu      sync.Mutex
	upserts map[string]struct{}
	deletes map[string]struct{}
}

func newWSFSWriteJournal() *wsfsWriteJournal {
	return &wsfsWriteJournal{
		upserts: make(map[string]struct{}),
		deletes: make(map[string]struct{}),
	}
}

func (j *wsfsWriteJournal) markUpsert(rel string) {
	j.mu.Lock()
	defer j.mu.Unlock()

	// Recreating or modifying a path invalidates deletes on the same path
	// or any parent path.
	for del := range j.deletes {
		if wsfsPathHasPrefix(rel, del) {
			delete(j.deletes, del)
		}
	}

	// Keep only the broadest upsert path.
	for up := range j.upserts {
		if wsfsPathHasPrefix(rel, up) {
			return
		}
		if wsfsPathHasPrefix(up, rel) {
			delete(j.upserts, up)
		}
	}
	j.upserts[rel] = struct{}{}
}

func (j *wsfsWriteJournal) markDelete(rel string) {
	j.mu.Lock()
	defer j.mu.Unlock()

	// Deleting a path invalidates upserts under that path.
	for up := range j.upserts {
		if wsfsPathHasPrefix(up, rel) {
			delete(j.upserts, up)
		}
	}

	// Avoid redundant nested deletes.
	for del := range j.deletes {
		if wsfsPathHasPrefix(rel, del) {
			return
		}
		if wsfsPathHasPrefix(del, rel) {
			delete(j.deletes, del)
		}
	}
	j.deletes[rel] = struct{}{}
}

func (j *wsfsWriteJournal) snapshot() ([]string, []string) {
	j.mu.Lock()
	defer j.mu.Unlock()

	upserts := make([]string, 0, len(j.upserts))
	for rel := range j.upserts {
		upserts = append(upserts, rel)
	}
	deletes := make([]string, 0, len(j.deletes))
	for rel := range j.deletes {
		deletes = append(deletes, rel)
	}
	sort.Strings(upserts)
	sort.Strings(deletes)
	return upserts, deletes
}

func (j *wsfsWriteJournal) isDeleted(rel string) bool {
	j.mu.Lock()
	defer j.mu.Unlock()

	for del := range j.deletes {
		if wsfsPathHasPrefix(rel, del) {
			return true
		}
	}
	return false
}

func (j *wsfsWriteJournal) isShadowed(rel string) bool {
	j.mu.Lock()
	defer j.mu.Unlock()

	for del := range j.deletes {
		if wsfsPathHasPrefix(rel, del) {
			return true
		}
	}
	for up := range j.upserts {
		if wsfsPathHasPrefix(rel, up) || wsfsPathHasPrefix(up, rel) {
			return true
		}
	}
	return false
}

func wsfsPathHasPrefix(pathValue, prefix string) bool {
	if prefix == "." || prefix == "" {
		return true
	}
	if pathValue == prefix {
		return true
	}
	return strings.HasPrefix(pathValue, prefix+"/")
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

// workspaceStat resolves path metadata from workspace APIs with a symlink-aware
// fallback. This avoids dropping symlink entries when followed stat fails on
// filtered snapshots.
func (fsys *wsfsPathFS) workspaceStat(rel string) (*Stat, error) {
	return fsys.workspaceStatMode(rel, false)
}

func (fsys *wsfsPathFS) workspaceStatMode(rel string, noCache bool) (*Stat, error) {
	stat, err := workspaceMountStat(fsys.ctx, fsys.workspace, rel, false, noCache)
	if err == nil {
		return stat, nil
	}

	lstat, lstatErr := workspaceMountStat(fsys.ctx, fsys.workspace, rel, true, noCache)
	if lstatErr != nil {
		return nil, err
	}

	if lstat.FileType != FileTypeSymlink {
		return lstat, nil
	}

	resolved := lstat.Clone()
	if _, entriesErr := workspaceMountEntries(fsys.ctx, fsys.workspace, rel, noCache); entriesErr == nil {
		resolved.FileType = FileTypeDirectory
		return resolved, nil
	}

	if _, fileErr := workspaceMountFile(fsys.ctx, fsys.workspace, rel, noCache); fileErr == nil {
		resolved.FileType = FileTypeRegular
		return resolved, nil
	}

	return resolved, nil
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
