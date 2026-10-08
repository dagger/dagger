package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/continuity/fs"
	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/util/gitutil"
	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	"golang.org/x/sys/unix"
)

// contentsCheckoutTree builds the source-only tree of a ref from a retained
// checkout (GitRef.__fullCheckout, which WorkspaceGit.__checkout selects)
// instead of checking it out again. Repositories opened over such a checkout
// with GitRepository.withContents carry no checkout base, which is how most
// recorded workspace histories are shaped; see gitContentsCheckoutBase for the
// checkouts recognized.
//
// A retained checkout is a clean, timestamp-normalized checkout of its HEAD
// by construction (finishGitCheckout), so a copy-on-write copy of it without
// .git is that commit's tree; see finishRetainedCheckoutTree. Another commit
// is the delta from HEAD applied first, as incrementalTree applies it, when
// HEAD is its ancestor.
//
// False means the caller must use the full checkout: no recognized checkout,
// one that is not materialized yet (evaluating it costs as much as the full
// checkout), submodules, a delta the incremental planner refuses, a snapshot
// chain that is too deep, or any other failure (see nativeFallback); reason
// names the cause. When supported, reason names the checkout and whether a
// delta was applied.
func (ref *LocalGitRef) contentsCheckoutTree(ctx context.Context, srv *dagql.Server) (_ *Directory, supported bool, reason string, rerr error) {
	ctx, span := Tracer(ctx).Start(ctx, "materialize git source tree from checkout", telemetry.Internal())
	defer func() {
		if nativeFallback(ctx, span, "dagger.git.checkout.contents.fallback", rerr) {
			reason = gitTreeFallbackCode(rerr)
			supported, rerr = false, nil
		}
		span.SetAttributes(
			attribute.Bool("dagger.git.checkout.contents.supported", supported),
			attribute.String("dagger.git.checkout.contents.reason", reason),
		)
		telemetry.EndWithCause(span, &rerr)
	}()
	if ref.Ref == nil || len(ref.SHA) != 40 || !IsFullGitSHA(ref.SHA) {
		return nil, false, "commit-format", nil
	}
	base, worktree, kind, err := gitContentsCheckoutBase(ctx, srv, ref.repo.Directory)
	if err != nil {
		return nil, false, "", err
	}
	if base.Self() == nil {
		return nil, false, kind, nil
	}
	if dagql.HasPendingLazyComputation(base) {
		return nil, false, "cold-" + kind, nil
	}
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, false, "", err
	}
	root, err := freshSnapshotRoot(ctx, query)
	if err != nil {
		return nil, false, "", err
	}
	snapshot, err := base.Self().Snapshot.GetOrEval(ctx, base.Result)
	if err != nil {
		return nil, false, "", err
	}
	var result *Directory
	err = ref.repo.mount(ctx, 0, false, nil, func(source *gitutil.GitCLI) (rerr error) {
		child, err := query.SnapshotManager().New(ctx, snapshot, bkcache.WithRecordType(bkclient.UsageRecordTypeRegular), bkcache.WithDescription("git source tree from checkout"))
		if err != nil {
			return err
		}
		defer func() {
			if child != nil {
				rerr = errors.Join(rerr, child.Release(context.WithoutCancel(ctx)))
			}
		}()
		err = MountRef(ctx, child, func(mountRoot string, m *mount.Mount) error {
			if err := checkNativeSnapshotDepth(m); err != nil {
				return err
			}
			dest, err := fs.RootPath(mountRoot, worktree)
			if err != nil {
				return err
			}
			head, err := retainedCheckoutHead(ctx, dest)
			if err != nil {
				return err
			}
			reason = kind + "/strip"
			if head != ref.SHA {
				plan, refused, err := planIncrementalGitCheckout(ctx, source, head, ref.SHA, ref.repo.HistorySource.Self() != nil)
				if err != nil {
					return err
				}
				if refused != "" {
					return nativeCommitUnsupportedReason(refused)
				}
				span.SetAttributes(attribute.Int("dagger.git.checkout.contents.changed_paths", len(plan.changed)))
				if err := applyIncrementalGitCheckout(ctx, source, dest, ref.SHA, plan); err != nil {
					return err
				}
				reason = kind + "/delta"
			}
			return finishRetainedCheckoutTree(dest, root)
		})
		if err != nil {
			return err
		}
		snap, err := child.Commit(ctx)
		if err != nil {
			return err
		}
		child = nil
		result = &Directory{Platform: query.Platform(), Dir: new(LazyAccessor[string, *Directory]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory])}
		result.SetPath(worktree)
		result.SetSnapshot(snap)
		return nil
	})
	err = errors.Join(err, ctx.Err())
	if err != nil {
		if result != nil {
			err = errors.Join(err, result.OnRelease(context.WithoutCancel(ctx)))
		}
		return nil, false, "", err
	}
	return result, true, reason, nil
}

// gitContentsCheckoutBase finds the retained checkout behind a local
// repository's storage, from the recipe that produced it, and the selector of
// its worktree in that checkout's snapshot:
//
//   - the checkout itself (GitRef.__fullCheckout, WorkspaceGit.__checkout):
//     kind "checkout";
//   - its .git directory (Directory.directory(path: ".git") of one, which
//     shares its snapshot): kind "checkout-git-dir";
//   - a pull's scratch repository (Workspace.__pullDirectory), a
//     copy-on-write child of the receiver's checkout that may carry the
//     receiver's uncommitted edits and rewritten timestamps: the receiver's
//     checkout itself, kind "pull-receiver".
//
// Anything else returns no base, with a reason. The recipe is only a hint:
// the checkout's HEAD and layout are verified once mounted.
func gitContentsCheckoutBase(ctx context.Context, srv *dagql.Server, contents dagql.ObjectResult[*Directory]) (base dagql.ObjectResult[*Directory], worktree, kind string, _ error) {
	if contents.Self() == nil {
		return base, "", "no-contents", nil
	}
	frame, err := contents.ResultCall()
	if err != nil || frame == nil {
		return base, "", "contents-recipe", nil
	}
	isCheckout := func(field string) bool { return field == "__fullCheckout" || field == "__checkout" }
	switch {
	case isCheckout(frame.Field):
		selector, err := contents.Self().Dir.GetOrEval(ctx, contents.Result)
		if err != nil {
			return base, "", "", err
		}
		return contents, selector, "checkout", nil
	case frame.Field == "directory":
		var dir string
		for _, arg := range frame.Args {
			if arg.Name == "path" && arg.Value != nil && arg.Value.Kind == dagql.ResultCallLiteralKindString {
				dir = arg.Value.StringValue
			}
		}
		if path.Clean("/"+dir) != "/.git" {
			return base, "", "contents-not-checkout", nil
		}
		receiver, err := frame.ReceiverCall(ctx)
		if err != nil || receiver == nil || !isCheckout(receiver.Field) {
			return base, "", "contents-not-checkout", nil
		}
		selector, err := contents.Self().Dir.GetOrEval(ctx, contents.Result)
		if err != nil {
			return base, "", "", err
		}
		if path.Base(path.Clean(selector)) != ".git" {
			return base, "", "contents-not-checkout", nil
		}
		return contents, path.Dir(path.Clean(selector)), "checkout-git-dir", nil
	case frame.Field == "__pullDirectory":
		receiver, err := frame.ReceiverCall(ctx)
		if err != nil || receiver == nil {
			return base, "", "contents-recipe", nil
		}
		id, err := receiver.RecipeID(ctx)
		if err != nil {
			return base, "", "contents-recipe", nil
		}
		ws, err := dagql.NewID[*Workspace](id).Load(ctx, srv)
		if err != nil {
			if ctx.Err() != nil {
				return base, "", "", err
			}
			return base, "", "pull-receiver-recipe", nil
		}
		// The checkout the pull itself started from: already materialized
		// when the pull was.
		if err := srv.Select(ctx, ws, &base, dagql.Selector{Field: "git"}, dagql.Selector{Field: "__checkout"}); err != nil {
			if ctx.Err() != nil {
				return base, "", "", err
			}
			return dagql.ObjectResult[*Directory]{}, "", "pull-receiver-recipe", nil
		}
		if dagql.HasPendingLazyComputation(base) {
			return base, "", "pull-receiver", nil
		}
		selector, err := base.Self().Dir.GetOrEval(ctx, base.Result)
		if err != nil {
			return dagql.ObjectResult[*Directory]{}, "", "", err
		}
		return base, selector, "pull-receiver", nil
	default:
		return base, "", "contents-not-checkout", nil
	}
}

// retainedCheckoutHead verifies that dest is a non-bare checkout with its
// Git directory at dest/.git and no submodules, and returns its HEAD commit.
// Submodule content depends on the superproject's Git metadata, which is not
// modeled: a .gitmodules is refused.
func retainedCheckoutHead(ctx context.Context, dest string) (string, error) {
	gitDir := filepath.Join(dest, ".git")
	if info, err := os.Lstat(gitDir); err != nil || !info.IsDir() {
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		return "", nativeCommitUnsupportedReason("checkout-layout")
	}
	if _, err := os.Lstat(filepath.Join(dest, ".gitmodules")); err == nil {
		return "", nativeCommitUnsupportedReason("submodules")
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	git := gitutil.NewGitCLI(gitutil.WithDir(dest), gitutil.WithGitDir(gitDir), gitutil.WithWorkTree(dest))
	out, err := git.New(gitutil.WithIgnoreError()).Run(ctx, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	if err != nil {
		return "", err
	}
	head := strings.TrimSpace(string(out))
	if len(head) != 40 || !IsFullGitSHA(head) {
		return "", nativeCommitUnsupportedReason("checkout-head")
	}
	return head, nil
}

// finishRetainedCheckoutTree turns dest, a private writable copy of a
// retained checkout, into the source-only tree a discarded checkout writes
// into a fresh snapshot. Both run the same init and finishGitCheckout steps,
// so their worktrees match: the same files, attributes, modes, symlinks and
// normalized timestamps. What differs is fixed here:
//
//   - .git itself, removed (whiteouts only: nothing below it is copied up).
//     A discarded checkout removes it before normalizing timestamps, so the
//     root's times are normalized again.
//   - the root directory, which a copy-on-write checkout inherits from its
//     repository's snapshot: its mode and ownership are set to a fresh
//     snapshot's (want), and differing extended attributes are refused.
func finishRetainedCheckoutTree(dest string, want snapshotRootInfo) error {
	got, err := readSnapshotRootInfo(dest)
	if err != nil {
		return err
	}
	if !slices.Equal(got.xattrs, want.xattrs) {
		return nativeCommitUnsupportedReason("root-xattrs")
	}
	if err := os.RemoveAll(filepath.Join(dest, ".git")); err != nil {
		return err
	}
	if got.uid != want.uid || got.gid != want.gid {
		if err := os.Lchown(dest, int(want.uid), int(want.gid)); err != nil {
			return err
		}
	}
	// chown clears setuid/setgid bits, so the mode is set after it.
	if got.mode != want.mode || got.uid != want.uid || got.gid != want.gid {
		if err := os.Chmod(dest, want.mode&(os.ModePerm|os.ModeSetuid|os.ModeSetgid|os.ModeSticky)); err != nil {
			return err
		}
	}
	return unix.UtimesNanoAt(unix.AT_FDCWD, dest, []unix.Timespec{{Sec: 1}, {Sec: 1}}, unix.AT_SYMLINK_NOFOLLOW)
}

// snapshotRootInfo is the metadata of a snapshot's root directory.
type snapshotRootInfo struct {
	mode     os.FileMode
	uid, gid uint32
	xattrs   []string
}

func readSnapshotRootInfo(root string) (snapshotRootInfo, error) {
	var st unix.Stat_t
	if err := unix.Lstat(root, &st); err != nil {
		return snapshotRootInfo{}, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR {
		return snapshotRootInfo{}, fmt.Errorf("snapshot root %s is not a directory", root)
	}
	info, err := os.Lstat(root)
	if err != nil {
		return snapshotRootInfo{}, err
	}
	xattrs, err := listXattrs(root)
	if err != nil {
		return snapshotRootInfo{}, err
	}
	return snapshotRootInfo{mode: info.Mode(), uid: st.Uid, gid: st.Gid, xattrs: xattrs}, nil
}

func listXattrs(p string) ([]string, error) {
	size, err := unix.Llistxattr(p, nil)
	if errors.Is(err, unix.ENOTSUP) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if size == 0 {
		return nil, nil
	}
	buf := make([]byte, size)
	size, err = unix.Llistxattr(p, buf)
	if err != nil {
		return nil, err
	}
	var names []string
	for name := range strings.SplitSeq(string(buf[:size]), "\x00") {
		if name != "" {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names, nil
}

var freshSnapshotRoots sync.Map // bkcache.SnapshotManager -> snapshotRootInfo

// freshSnapshotRoot returns the root metadata of a new empty snapshot, the
// root a full source-only checkout writes into. It is probed once per
// snapshot manager rather than assumed.
func freshSnapshotRoot(ctx context.Context, query *Query) (_ snapshotRootInfo, rerr error) {
	manager := query.SnapshotManager()
	if info, ok := freshSnapshotRoots.Load(manager); ok {
		return info.(snapshotRootInfo), nil
	}
	ref, err := manager.New(ctx, nil, bkcache.WithRecordType(bkclient.UsageRecordTypeRegular), bkcache.WithDescription("git source tree root probe"))
	if err != nil {
		return snapshotRootInfo{}, err
	}
	defer func() { rerr = errors.Join(rerr, ref.Release(context.WithoutCancel(ctx))) }()
	var info snapshotRootInfo
	if err := MountRef(ctx, ref, func(root string, _ *mount.Mount) error {
		info, err = readSnapshotRootInfo(root)
		return err
	}); err != nil {
		return snapshotRootInfo{}, err
	}
	freshSnapshotRoots.Store(manager, info)
	return info, nil
}
