package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"

	"github.com/containerd/containerd/v2/core/mount"
	containerdfs "github.com/containerd/continuity/fs"
	"golang.org/x/sys/unix"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/engineutil"
	"github.com/dagger/dagger/engine/slog"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/util/layercopy"
)

// interactiveTerminalSnapshotMaxBytes bounds how much file data a single
// snapshot of a running terminal may copy, so a refresh on a huge tree fails
// fast with a clear error instead of stalling.
var interactiveTerminalSnapshotMaxBytes int64 = 1 << 30

// interactiveTerminal tracks a running terminal whose live filesystem may be
// snapshotted by the client that owns it (e.g. the -i explorer's refresh).
type interactiveTerminal struct {
	sessionID   string
	running     *RunningService
	containerID string // runc container ID of the running terminal
	workdir     string
	platform    Platform
}

// errLiveMountUnsupported means the mount covering the workdir cannot be
// captured as base+changes (e.g. cache volume, tmpfs, subpath selector).
var errLiveMountUnsupported = errors.New("workdir mount does not support live change capture")

var interactiveTerminals sync.Map // terminal ID -> *interactiveTerminal

func registerInteractiveTerminal(id string, term *interactiveTerminal) (unregister func()) {
	interactiveTerminals.Store(id, term)
	return func() { interactiveTerminals.Delete(id) }
}

// SnapshotInteractiveTerminal captures the current state of a running
// terminal's working directory as a new immutable Directory, while the
// terminal keeps running untouched.
//
// Preferred path: the mount covering the workdir (the rootfs, or e.g. a
// mounted /src) is captured as its immutable base plus only the changes
// currently in its live overlay upperdir, via the snapshot manager's
// diff-apply machinery. The base is shared as overlay lowerdirs and only
// changed files are copied, so the cost is proportional to what the terminal
// changed rather than to the size of the tree.
//
// Fallback (non-overlay snapshotter, cache volumes, subpath mounts, ...): the
// live tree is read through /proc/<pid>/root and copied.
func SnapshotInteractiveTerminal(ctx context.Context, terminalID string) (dagql.ObjectResult[*Directory], error) {
	var res dagql.ObjectResult[*Directory]

	v, ok := interactiveTerminals.Load(terminalID)
	if !ok {
		return res, fmt.Errorf("terminal %q is not running", terminalID)
	}
	term := v.(*interactiveTerminal)

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return res, err
	}
	if clientMetadata.SessionID != term.sessionID {
		// Terminal IDs are unguessable, but never let another session read a
		// terminal's filesystem even if an ID leaks.
		return res, fmt.Errorf("terminal %q is not running", terminalID)
	}

	rootfs, err := engineutil.ContainerRootFSPath(term.containerID)
	if err != nil {
		return res, fmt.Errorf("locate terminal filesystem: %w", err)
	}
	query, err := CurrentQuery(ctx)
	if err != nil {
		return res, err
	}

	var (
		immutableRef bkcache.ImmutableRef
		dirPath      string
	)
	err = term.running.WithMountStates(func(states []*execMountState) error {
		// Secret and SSH mounts must never end up in a snapshot.
		skip, err := sensitiveMountPaths(rootfs, states)
		if err != nil {
			return err
		}
		immutableRef, dirPath, err = snapshotLiveWorkdir(ctx, query, states, rootfs, term.workdir, skip)
		if errors.Is(err, errLiveMountUnsupported) || errors.Is(err, bkcache.ErrLiveChangesUnsupported) {
			slog.Debug("interactive terminal snapshot: falling back to copy", "reason", err)
			immutableRef, err = snapshotByCopy(ctx, query, rootfs, term.workdir, skip)
			dirPath = "/"
		}
		return err
	})
	if err != nil {
		return res, fmt.Errorf("snapshot terminal filesystem: %w", err)
	}

	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		_ = immutableRef.Release(context.WithoutCancel(ctx))
		return res, fmt.Errorf("get dagql server: %w", err)
	}

	dir := &Directory{
		Platform: term.platform,
		Dir:      new(LazyAccessor[string, *Directory]),
		Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]),
	}
	dir.SetPath(dirPath)
	dir.SetSnapshot(immutableRef)

	res, err = dagql.NewObjectResultForCurrentCall(ctx, srv, dir)
	if err != nil {
		_ = dir.OnRelease(context.WithoutCancel(ctx))
		return res, err
	}
	return res, nil
}

// snapshotLiveWorkdir captures the workdir as seen by the terminal using the
// engine's snapshot primitives, without copying the whole tree:
//
//  1. The innermost mount containing the workdir (the "covering" mount, e.g.
//     the rootfs or a mounted /src) is captured as its immutable base plus
//     only the live changes in its overlay upperdir.
//  2. Mounts nested below the workdir (e.g. a cache at /src/node_modules) are
//     composed on top in a single extra layer, the same way withDirectory
//     lays a directory down (layercopy), shallowest first so deeper mounts
//     win -- each from its cheapest source (see composeNestedMounts).
//
// It returns the new ref and the workdir's path within it.
func snapshotLiveWorkdir(
	ctx context.Context,
	query *Query,
	states []*execMountState,
	rootfs, workdir string,
	skip map[string]struct{},
) (bkcache.ImmutableRef, string, error) {
	workdir = path.Clean("/" + workdir)

	covering, nested := selectWorkdirMounts(states, workdir)
	if covering == nil {
		return nil, "", errLiveMountUnsupported
	}
	coveringRef, err := captureLiveMount(ctx, query, covering, rootfs)
	if err != nil {
		return nil, "", err
	}
	dirPath := path.Clean("/" + strings.TrimPrefix(workdir, mountDest(covering)))
	if len(nested) == 0 {
		return coveringRef, dirPath, nil
	}

	composed, err := composeNestedMounts(ctx, query, coveringRef, dirPath, workdir, nested, rootfs, skip)
	// The composed snapshot keeps its parent alive; drop our handle either way.
	_ = coveringRef.Release(context.WithoutCancel(ctx))
	if err != nil {
		return nil, "", fmt.Errorf("compose nested mounts: %w", err)
	}
	return composed, dirPath, nil
}

// selectWorkdirMounts returns the innermost mount containing workdir (what
// the terminal sees there), and the mounts nested below workdir sorted
// shallowest first (so deeper mounts are laid down last and win).
func selectWorkdirMounts(states []*execMountState, workdir string) (covering *execMountState, nested []*execMountState) {
	workdir = path.Clean("/" + workdir)
	for _, state := range states {
		dest := mountDest(state)
		if !pathWithin(workdir, dest) {
			continue
		}
		if covering == nil || len(dest) > len(mountDest(covering)) {
			covering = state
		}
	}
	for _, state := range states {
		dest := mountDest(state)
		if state == covering || dest == workdir || !pathWithin(dest, workdir) {
			continue
		}
		nested = append(nested, state)
	}
	sort.SliceStable(nested, func(i, j int) bool {
		return strings.Count(mountDest(nested[i]), "/") < strings.Count(mountDest(nested[j]), "/")
	})
	return covering, nested
}

// captureLiveMount captures a writable, whole-ref overlay mount as its
// immutable base plus its current live changes. Other mount kinds return
// errLiveMountUnsupported so callers can pick another strategy.
func captureLiveMount(ctx context.Context, query *Query, state *execMountState, rootfs string) (bkcache.ImmutableRef, error) {
	if state.ActiveRef == nil ||
		state.Readonly ||
		state.Volume != nil || state.TmpfsOpt != nil ||
		state.Secret != nil || state.SSH != nil {
		return nil, errLiveMountUnsupported
	}
	// The container sees the mount's Selector subpath at Dest; only whole-ref
	// mounts map the merged view 1:1 onto the ref.
	if path.Clean("/"+state.Selector) != "/" {
		return nil, errLiveMountUnsupported
	}
	var base bkcache.ImmutableRef
	if state.SourceRef != nil {
		iref, ok := state.SourceRef.(bkcache.ImmutableRef)
		if !ok {
			return nil, errLiveMountUnsupported
		}
		base = iref
	}
	lcs, ok := query.SnapshotManager().(bkcache.LiveChangesSnapshotter)
	if !ok {
		return nil, errLiveMountUnsupported
	}
	mergedView, err := containerdfs.RootPath(rootfs, mountDest(state))
	if err != nil {
		return nil, fmt.Errorf("resolve mount %q in terminal: %w", mountDest(state), err)
	}
	return lcs.SnapshotLiveChanges(ctx, base, state.ActiveRef, mergedView,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription("interactive terminal snapshot"))
}

// composeNestedMounts lays the nested mounts down onto a new snapshot on top
// of base, at their paths relative to the workdir. Each mount comes from its
// cheapest source:
//
//   - read-only mount: its immutable base ref as-is (nothing can change it)
//   - writable whole-ref overlay mount: base + live changes (captureLiveMount)
//   - anything else (tmpfs, cache volume, subpath mount, ...): the terminal's
//     live view of it, copied with hardlinks disabled
//   - secret / SSH mounts: never captured
func composeNestedMounts(
	ctx context.Context,
	query *Query,
	base bkcache.ImmutableRef,
	dirPath, workdir string,
	nested []*execMountState,
	rootfs string,
	skip map[string]struct{},
) (_ bkcache.ImmutableRef, rerr error) {
	newRef, err := query.SnapshotManager().New(ctx, base,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription("interactive terminal snapshot (nested mounts)"))
	if err != nil {
		return nil, fmt.Errorf("create snapshot: %w", err)
	}
	defer func() {
		if newRef != nil {
			_ = newRef.Release(context.WithoutCancel(ctx))
		}
	}()

	var liveRefs []bkcache.ImmutableRef
	defer func() {
		for _, ref := range liveRefs {
			_ = ref.Release(context.WithoutCancel(ctx))
		}
	}()

	err = MountRef(ctx, newRef, func(destRoot string, destMnt *mount.Mount) error {
		copier, err := layercopy.NewCopier(layercopy.Mount{Root: destRoot, Mount: destMnt})
		if err != nil {
			return err
		}
		defer copier.Close()

		for _, state := range nested {
			if state.Secret != nil || state.SSH != nil {
				continue
			}
			dest := mountDest(state)
			target := path.Join(dirPath, strings.TrimPrefix(dest, workdir))

			// immutable source ref + path within it, when available
			var (
				srcRef bkcache.ImmutableRef
				srcSel = path.Clean("/" + state.Selector)
			)
			switch iref, isImmutable := state.SourceRef.(bkcache.ImmutableRef); {
			case state.Readonly && isImmutable && state.Volume == nil && state.TmpfsOpt == nil:
				srcRef = iref
			default:
				ref, err := captureLiveMount(ctx, query, state, rootfs)
				switch {
				case err == nil:
					liveRefs = append(liveRefs, ref)
					srcRef, srcSel = ref, "/"
				case errors.Is(err, errLiveMountUnsupported), errors.Is(err, bkcache.ErrLiveChangesUnsupported):
					// fall through to the live view below
				default:
					return fmt.Errorf("capture mount %q: %w", dest, err)
				}
			}

			if srcRef != nil {
				err := MountRef(ctx, srcRef, func(srcRoot string, srcMnt *mount.Mount) error {
					return copyIntoLayer(ctx, copier, layercopy.Mount{Root: srcRoot, Mount: srcMnt}, srcRoot, srcSel, target, false, nil)
				}, mountRefAsReadOnly)
				if err != nil {
					return fmt.Errorf("compose mount %q: %w", dest, err)
				}
				continue
			}

			liveView, err := containerdfs.RootPath(rootfs, dest)
			if err != nil {
				return fmt.Errorf("resolve mount %q in terminal: %w", dest, err)
			}
			if err := copyIntoLayer(ctx, copier, layercopy.Mount{Root: liveView}, liveView, "/", target, true, skip); err != nil {
				return fmt.Errorf("compose mount %q: %w", dest, err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	ref, err := newRef.Commit(ctx)
	if err != nil {
		return nil, fmt.Errorf("commit terminal snapshot: %w", err)
	}
	newRef = nil
	return ref, nil
}

// copyIntoLayer copies srcPath (a file or directory within src) to target,
// replacing what is there. live sources may change while being read, so
// hardlinks out of them are never created; skip lists host paths (e.g.
// secret mounts) whose content must be excluded.
func copyIntoLayer(
	ctx context.Context,
	copier *layercopy.Copier,
	src layercopy.Mount,
	srcRoot, srcPath, target string,
	live bool,
	skip map[string]struct{},
) error {
	resolved, err := containerdfs.RootPath(srcRoot, srcPath)
	if err != nil {
		return err
	}
	fi, err := os.Lstat(resolved)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil // nothing mounted there anymore
		}
		return err
	}
	opts := layercopy.CopyOptions{
		CopyDirContents:  true,
		ReplaceExisting:  true,
		DisableHardlinks: live,
	}
	for hostPath := range skip {
		if rel, err := filepath.Rel(resolved, hostPath); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			opts.Filter.Exclude = append(opts.Filter.Exclude, filepath.ToSlash(rel))
		}
	}
	if !fi.IsDir() {
		return copier.CopyFile(ctx, src, srcPath, target, opts)
	}
	return copier.Copy(ctx, src, srcPath, target, opts)
}

// snapshotByCopy is the fallback capture: it copies the live workdir tree,
// read through /proc/<pid>/root (including nested mounts, but never the
// skipped sensitive mounts), into a fresh snapshot.
func snapshotByCopy(ctx context.Context, query *Query, rootfs, workdir string, skip map[string]struct{}) (bkcache.ImmutableRef, error) {
	// Resolve the workdir scoped to the container root so symlinks in the
	// path are interpreted as the container would.
	src, err := containerdfs.RootPath(rootfs, workdir)
	if err != nil {
		return nil, fmt.Errorf("resolve terminal workdir %q: %w", workdir, err)
	}
	mutableRef, err := query.SnapshotManager().New(ctx, nil,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription("interactive terminal snapshot"))
	if err != nil {
		return nil, fmt.Errorf("create snapshot: %w", err)
	}
	defer func() {
		if mutableRef != nil {
			_ = mutableRef.Release(context.WithoutCancel(ctx))
		}
	}()
	err = MountRef(ctx, mutableRef, func(dest string, _ *mount.Mount) error {
		return copyLiveTree(ctx, src, dest, skip)
	})
	if err != nil {
		return nil, err
	}
	ref, err := mutableRef.Commit(ctx)
	if err != nil {
		return nil, fmt.Errorf("commit terminal snapshot: %w", err)
	}
	mutableRef = nil
	return ref, nil
}

// sensitiveMountPaths returns the host paths (via rootfs) of the terminal's
// secret and SSH socket mounts, which must never be captured.
func sensitiveMountPaths(rootfs string, states []*execMountState) (map[string]struct{}, error) {
	skip := make(map[string]struct{})
	for _, state := range states {
		if state.Secret == nil && state.SSH == nil {
			continue
		}
		p, err := containerdfs.RootPath(rootfs, mountDest(state))
		if err != nil {
			return nil, fmt.Errorf("resolve sensitive mount %q: %w", mountDest(state), err)
		}
		skip[p] = struct{}{}
	}
	return skip, nil
}

func mountDest(state *execMountState) string {
	return path.Clean("/" + state.Dest)
}

// pathWithin reports whether p is dir or lies below it.
func pathWithin(p, dir string) bool {
	return dir == "/" || p == dir || strings.HasPrefix(p, dir+"/")
}

// pseudoFSMagics are filesystem types that expose kernel state rather than
// user files; walking them is useless at best (and unbounded for procfs).
var pseudoFSMagics = map[int64]struct{}{
	unix.PROC_SUPER_MAGIC:    {},
	unix.SYSFS_MAGIC:         {},
	unix.DEVPTS_SUPER_MAGIC:  {},
	unix.CGROUP_SUPER_MAGIC:  {},
	unix.CGROUP2_SUPER_MAGIC: {},
	unix.DEBUGFS_MAGIC:       {},
	unix.TRACEFS_MAGIC:       {},
	unix.SECURITYFS_MAGIC:    {},
	unix.BPF_FS_MAGIC:        {},
}

// isPseudoFS reports whether path is on a kernel pseudo filesystem (procfs,
// sysfs, cgroupfs, ...).
func isPseudoFS(path string) bool {
	var sfs unix.Statfs_t
	if unix.Statfs(path, &sfs) != nil {
		return false
	}
	_, pseudo := pseudoFSMagics[int64(sfs.Type)] //nolint:unconvert // Statfs_t.Type's width differs across architectures (e.g. uint32 on s390x)
	return pseudo
}

// copyLiveTree copies a live directory tree (which may be changing underneath
// us) from src into dest. It preserves modes, ownership, and mtimes; copies
// symlinks as symlinks; skips device nodes, sockets, FIFOs, and pseudo
// filesystems; and tolerates entries that vanish or become unreadable
// mid-walk.
func copyLiveTree(ctx context.Context, src, dest string, skip map[string]struct{}) error {
	var copied int64
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, sensitive := skip[path]; sensitive {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if walkErr != nil {
			if path == src {
				return walkErr
			}
			// Vanished or unreadable mid-walk: skip it.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, rel)

		info, err := d.Info()
		if err != nil {
			return nil //nolint:nilerr // vanished mid-walk: skip it
		}
		st, _ := info.Sys().(*syscall.Stat_t)

		switch mode := info.Mode(); {
		case mode.IsDir():
			if path != src && isPseudoFS(path) {
				return fs.SkipDir
			}
			if err := os.MkdirAll(target, mode.Perm()); err != nil {
				return err
			}
			_ = os.Chmod(target, mode.Perm()|(mode&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky)))
			chownLike(target, st, false)

		case mode.IsRegular():
			copied += info.Size()
			if copied > interactiveTerminalSnapshotMaxBytes {
				return fmt.Errorf("working directory is larger than %d MiB; too large to snapshot",
					interactiveTerminalSnapshotMaxBytes>>20)
			}
			if err := copyLiveFile(path, target, mode, st); err != nil {
				if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) {
					return nil // vanished or unreadable; skip
				}
				return err
			}
			_ = os.Chtimes(target, info.ModTime(), info.ModTime())

		case mode&fs.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return nil //nolint:nilerr // vanished mid-walk: skip it
			}
			if err := os.Symlink(link, target); err != nil && !errors.Is(err, fs.ErrExist) {
				return err
			}
			chownLike(target, st, true)

		default:
			// device nodes, sockets, FIFOs: not meaningful to browse
		}
		return nil
	})
}

func copyLiveFile(src, dest string, mode fs.FileMode, st *syscall.Stat_t) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode.Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	// chown before chmod: chown clears setuid/setgid bits.
	chownLike(dest, st, false)
	return os.Chmod(dest, mode.Perm()|(mode&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky)))
}

func chownLike(path string, st *syscall.Stat_t, symlink bool) {
	if st == nil {
		return
	}
	if symlink {
		_ = os.Lchown(path, int(st.Uid), int(st.Gid))
		return
	}
	_ = os.Chown(path, int(st.Uid), int(st.Gid))
}
