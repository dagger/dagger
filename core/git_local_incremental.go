package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

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

func (ref *LocalGitRef) incrementalCheckoutEligible() bool {
	base := ref.repo.CheckoutBase
	if base == nil || ref.Ref == nil || ref.SHA != base.CommitSHA || len(ref.SHA) != 40 || !IsFullGitSHA(ref.SHA) {
		return false
	}
	parent := base.Parent.Self()
	if parent == nil || parent.Ref == nil || len(parent.Ref.SHA) != 40 || !IsFullGitSHA(parent.Ref.SHA) {
		return false
	}
	_, local := parent.Backend.(*LocalGitRef)
	if local {
		return true
	}
	_, remote := parent.Backend.(*RemoteGitRef)
	return remote && base.Tree.Self() != nil
}

// incrementalTree applies only the commit's delta to a COW child of the parent
// tree. False means the caller must use the full checkout: unsupported inputs,
// a cold parent it may not materialize (see incrementalParentTree), a snapshot
// chain that is already too deep, an unusable parent tree, or any other failure
// (see nativeFallback). Only the caller's cancellation surfaces.
func (ref *LocalGitRef) incrementalTree(ctx context.Context, srv *dagql.Server) (_ *Directory, supported bool, rerr error) {
	ctx, span := Tracer(ctx).Start(ctx, "materialize incremental git checkout", telemetry.Internal())
	defer func() {
		if nativeFallback(ctx, span, "dagger.git.checkout.incremental.fallback", rerr) {
			supported, rerr = false, nil
		}
		span.SetAttributes(attribute.Bool("dagger.git.checkout.incremental.supported", supported))
		telemetry.EndWithCause(span, &rerr)
	}()
	if err := ref.repo.CheckoutBase.validateTree(ctx); err != nil {
		return nil, false, err
	}
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, false, err
	}
	var result *Directory
	err = ref.repo.mount(ctx, 0, false, nil, func(source *gitutil.GitCLI) (rerr error) {
		if _, err := ref.repo.nativeGitDir(ctx, source.Dir()); err != nil {
			return err
		}
		// These gates run before selecting/evaluating the parent tree. Unsupported
		// controls must re-checkout every file, including otherwise unchanged blobs.
		plan, reason, err := planIncrementalGitCheckout(ctx, source, ref.repo.CheckoutBase.Parent.Self().Ref.SHA, ref.SHA, ref.repo.HistorySource.Self() != nil)
		if err != nil {
			return err
		}
		if reason != "" {
			span.SetAttributes(attribute.String("dagger.git.checkout.incremental.fallback", reason))
			return nil
		}
		// Selecting the tree is cheap: it returns the canonical lazy result
		// without materializing it. A remote parent's tree is pinned instead,
		// so its checkout never needs another fetch.
		parent := ref.repo.CheckoutBase.Tree
		if parent.Self() == nil {
			if err := srv.Select(ctx, ref.repo.CheckoutBase.Parent, &parent, dagql.Selector{Field: "tree", Args: []dagql.NamedInput{{Name: "discardGitDir", Value: dagql.Boolean(true)}}}); err != nil {
				return err
			}
		}
		snapshot, parentPath, ok, err := incrementalParentTree(ctx, parent)
		if err != nil {
			return err
		}
		if !ok {
			span.SetAttributes(attribute.String("dagger.git.checkout.incremental.fallback", "cold-parent"))
			return nil
		}
		supported = true
		span.SetAttributes(attribute.Int("dagger.git.checkout.incremental.changed_paths", len(plan.changed)))
		child, err := query.SnapshotManager().New(ctx, snapshot, bkcache.WithRecordType(bkclient.UsageRecordTypeRegular), bkcache.WithDescription("incremental git source checkout"))
		if err != nil {
			return err
		}
		defer func() {
			if child != nil {
				rerr = errors.Join(rerr, child.Release(context.WithoutCancel(ctx)))
			}
		}()
		err = MountRef(ctx, child, func(root string, m *mount.Mount) error {
			// Each incremental tree stacks one more layer on its parent's.
			// Past the bound, the full checkout starts a fresh snapshot.
			if err := checkNativeSnapshotDepth(m); err != nil {
				return err
			}
			dest, err := fs.RootPath(root, parentPath)
			if err != nil {
				return err
			}
			return applyIncrementalGitCheckout(ctx, source, dest, ref.SHA, plan)
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
		result.SetPath(parentPath)
		result.SetSnapshot(snap)
		return nil
	})
	err = errors.Join(err, ctx.Err())
	if err != nil {
		if result != nil {
			err = errors.Join(err, result.OnRelease(context.WithoutCancel(ctx)))
		}
		return nil, supported, err
	}
	return result, supported, nil
}

// incrementalColdParentKey marks a context that is materializing a cold parent
// tree on behalf of an incremental checkout.
type incrementalColdParentKey struct{}

// incrementalParentTree returns the parent's canonical tree. A cold parent is
// materialized only for a top-level checkout, and that evaluation may itself
// only apply a delta to an already materialized grandparent. Without this
// bound, a cold chain (a session resumed on a pruned or restarted engine) would
// recurse through every ancestor: a nested mount, plan and delta per commit on
// top of a full checkout of the oldest. With it, a cold chain costs at most one
// full checkout of the parent plus one delta, and later commits are deltas
// again. False means a cold parent within such an evaluation: the caller takes
// the full checkout, which also starts a fresh snapshot chain. Restored stored
// snapshots are not cold, since opening them is cheap; an evaluation already in
// flight elsewhere is.
func incrementalParentTree(ctx context.Context, parent dagql.ObjectResult[*Directory]) (_ bkcache.ImmutableRef, _ string, ok bool, _ error) {
	if dagql.HasPendingLazyComputation(parent) {
		if ctx.Value(incrementalColdParentKey{}) != nil {
			return nil, "", false, nil
		}
		// Context values reach the parent's lazy evaluation, including its
		// own incrementalTree, through the cache's detached evaluation context.
		ctx = context.WithValue(ctx, incrementalColdParentKey{}, struct{}{})
	}
	snapshot, err := parent.Self().Snapshot.GetOrEval(ctx, parent.Result)
	if err != nil {
		return nil, "", false, err
	}
	dir, err := parent.Self().Dir.GetOrEval(ctx, parent.Result)
	if err != nil {
		return nil, "", false, err
	}
	return snapshot, dir, true, nil
}

type incrementalGitCheckoutPlan struct {
	changed  []string
	removed  []string // paths that existed in the parent, including modifications
	checkout []incrementalGitCheckoutEntry
}

// incrementalGitCheckoutEntry is a child tree leaf to write, as diff-tree
// reports it: the only index entries checkout-index needs.
type incrementalGitCheckoutEntry struct {
	mode, sha, path string
}

// No baseline filesystem traversal: tree/index scans are Git metadata only.
// A supported result is immutable evidence for the immediately following apply.
func planIncrementalGitCheckout(ctx context.Context, source *gitutil.GitCLI, parent, child string, ownedShallow bool) (*incrementalGitCheckoutPlan, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	if len(parent) != 40 || len(child) != 40 || !IsFullGitSHA(parent) || !IsFullGitSHA(child) {
		return nil, "commit-format", nil
	}
	gitDir, err := nativeCommitGitDirWithShallow(ctx, source.Dir(), ownedShallow)
	if err != nil {
		if nativeCommitFallback(err) {
			return nil, "repository-layout", nil
		}
		return nil, "", err
	}
	// Do not trust annotation alone: the retained recipe must be an actual
	// ancestor of this commit in the current object database (see
	// incrementalCheckoutAncestor). The tree delta itself holds for any pair
	// of commits; ancestry keeps it to the history the annotation describes.
	source = source.New(gitutil.WithArgs("--no-replace-objects"))
	ancestor, err := incrementalCheckoutAncestor(ctx, source, gitDir, parent, child)
	if err != nil {
		return nil, "", err
	}
	if !ancestor {
		return nil, "not-ancestor", nil
	}
	// Only the delta is inspected, never either full tree. A full checkout's
	// submodule step depends only on the gitlinks and .gitmodules, so when the
	// delta changes neither, the parent's canonical tree already holds the
	// child's submodule content. diff-tree -r lists gitlinks as leaves,
	// including those inside added, removed or replaced directories.
	changes, err := source.Run(ctx, "diff-tree", "--no-commit-id", "--no-renames", "--raw", "-r", "-z", parent, child)
	if err != nil {
		return nil, "", err
	}
	return parseIncrementalGitCheckoutPlan(changes)
}

// incrementalCheckoutAncestor reports whether base is child itself or one of
// its ancestors by the parents recorded in the commit objects. Replace refs
// are disabled by the caller, and revision traversal in the source would also
// honor info/grafts, which rewrite parents even then. The common case, a base
// that is the child's sole parent (GitRef.withCommit), reads the child's raw
// headers. Otherwise (several commits pulled on top of the base, or a
// fast-forward through merges), the walk runs in a private view of the
// object database: no refs, replace refs or grafts, only the exact shallow
// boundary of owned storage, so a base beyond it is simply not an ancestor.
func incrementalCheckoutAncestor(ctx context.Context, source *gitutil.GitCLI, gitDir, base, child string) (bool, error) {
	commit, err := source.Run(ctx, "cat-file", "commit", child)
	if err != nil {
		return false, err
	}
	if base == child {
		return true, nil
	}
	// Only top-level commit headers count.
	headers, _, _ := strings.Cut(string(commit), "\n\n")
	var parents []string
	for _, line := range strings.Split(headers, "\n") {
		if sha, ok := strings.CutPrefix(line, "parent "); ok {
			parents = append(parents, sha)
		}
	}
	if len(parents) == 1 && parents[0] == base {
		return true, nil
	}
	if len(parents) == 0 {
		return false, nil
	}
	ancestor := false
	err = withGitObjectView(ctx, []string{filepath.Join(gitDir, "objects")}, "sha1", func(view *gitutil.GitCLI) error {
		if err := copyGitShallowBoundary(gitDir, view.Dir()); err != nil {
			return err
		}
		var err error
		ancestor, err = gitIsAncestor(ctx, view.New(gitutil.WithArgs("--no-replace-objects")), base, child)
		return err
	})
	return ancestor, err
}

// parseIncrementalGitCheckoutPlan reads `diff-tree --raw -z` output:
// ":<src mode> <dst mode> <src sha> <dst sha> <status>" NUL "<path>" NUL.
func parseIncrementalGitCheckoutPlan(changes []byte) (*incrementalGitCheckoutPlan, string, error) {
	entries := splitOnNul(changes)
	if len(entries)%2 != 0 {
		return nil, "", fmt.Errorf("invalid git tree diff")
	}
	plan := &incrementalGitCheckoutPlan{}
	for i := 0; i < len(entries); i += 2 {
		header, p := entries[i], entries[i+1]
		fields := strings.Fields(strings.TrimPrefix(header, ":"))
		if !strings.HasPrefix(header, ":") || len(fields) != 5 {
			return nil, "", fmt.Errorf("invalid git tree diff entry")
		}
		status := fields[4]
		if path.Clean(p) != p || path.IsAbs(p) || p == ".." || strings.HasPrefix(p, "../") || gitMetaPath(p) {
			return nil, "unsafe-path", nil
		}
		// The full checkout initializes submodules: a gitlink added, removed
		// or changed (directly or within a replaced directory) changes more
		// than checkout-index would write.
		if fields[0] == "160000" || fields[1] == "160000" {
			return nil, "gitlink-change", nil
		}
		if path.Base(p) == ".gitattributes" || path.Base(p) == ".gitmodules" {
			return nil, "checkout-controls", nil
		}
		switch status {
		case "A", "D", "M", "T":
		default:
			return nil, "", fmt.Errorf("unexpected git diff status %q", status)
		}
		plan.changed = append(plan.changed, p)
		if status != "A" {
			plan.removed = append(plan.removed, p)
		}
		if status != "D" {
			// -r lists no trees, and gitlinks fell back above.
			mode, sha := fields[1], fields[3]
			if mode != "100644" && mode != "100755" && mode != "120000" {
				return nil, "", fmt.Errorf("unexpected git tree diff mode %q", mode)
			}
			if !IsFullGitSHA(sha) {
				return nil, "", fmt.Errorf("invalid git tree diff entry")
			}
			plan.checkout = append(plan.checkout, incrementalGitCheckoutEntry{mode: mode, sha: sha, path: p})
		}
	}
	return plan, "", nil
}

func applyIncrementalGitCheckout(ctx context.Context, source *gitutil.GitCLI, dest, child string, plan *incrementalGitCheckoutPlan) (rerr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(plan.changed) == 0 {
		return unix.UtimesNanoAt(unix.AT_FDCWD, dest, []unix.Timespec{{Sec: 1}, {Sec: 1}}, unix.AT_SYMLINK_NOFOLLOW)
	}
	var checkout *gitutil.GitCLI
	if len(plan.checkout) > 0 {
		scratch, err := os.MkdirTemp("", "dagger-git-checkout-")
		if err != nil {
			return err
		}
		defer func() { rerr = errors.Join(rerr, os.RemoveAll(scratch)) }()
		// Identical clean config/checkout conversion to the full source checkout,
		// never the source repository's config, info/attributes or dirty worktree.
		checkout = source.New(gitutil.WithDir(dest), gitutil.WithWorkTree(dest), gitutil.WithGitDir(filepath.Join(scratch, "git")), gitutil.WithIndexFile(filepath.Join(scratch, "index")))
		if err := initLocalGitTreeCheckout(ctx, source, checkout); err != nil {
			return err
		}
		// Stage only the leaves to write, from the delta's own modes and blobs,
		// in one process: reading the whole child tree into the index would
		// cost as much as the tree is large. checkout-index writes each entry
		// from its mode and blob alone, creating missing directories itself;
		// attributes come from the commit (see below), not other entries.
		var entries bytes.Buffer
		for _, entry := range plan.checkout {
			entries.WriteString(entry.mode + " " + entry.sha + "\t" + entry.path + "\x00")
		}
		if _, err := checkout.RunWithStdin(ctx, &entries, "update-index", "-z", "--index-info"); err != nil {
			return err
		}
	}
	root, err := os.OpenRoot(dest)
	if err != nil {
		return err
	}
	defer root.Close()
	touched := map[string]bool{".": true}
	for _, p := range plan.changed {
		for dir := path.Dir(p); ; dir = path.Dir(dir) {
			touched[dir] = true
			if dir == "." {
				break
			}
		}
	}
	// Remove only old tree leaves, not added paths that might currently traverse
	// an old symlink. Remove deepest first for directory/file replacements.
	removed := slices.Clone(plan.removed)
	slices.SortFunc(removed, func(a, b string) int { return strings.Count(b, "/") - strings.Count(a, "/") })
	for _, p := range removed {
		if err := root.Remove(filepath.FromSlash(p)); err != nil {
			return err
		}
	}
	dirs := make([]string, 0, len(touched))
	for dir := range touched {
		if dir != "." {
			dirs = append(dirs, dir)
		}
	}
	slices.SortFunc(dirs, func(a, b string) int { return strings.Count(b, "/") - strings.Count(a, "/") })
	for _, dir := range dirs {
		err := root.Remove(filepath.FromSlash(dir))
		if err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, unix.ENOTEMPTY) && !errors.Is(err, unix.EEXIST) && !errors.Is(err, unix.ENOTDIR) {
			return err
		}
	}
	// Git writes only below verified directory ancestors. Its directory/file
	// creation supplies the same modes and umask semantics as full checkout.
	for _, entry := range plan.checkout {
		prefix := ""
		for _, part := range strings.Split(path.Dir(entry.path), "/") {
			prefix = filepath.Join(prefix, part)
			info, err := root.Lstat(prefix)
			if errors.Is(err, os.ErrNotExist) {
				break
			}
			if err != nil {
				return err
			}
			if !info.IsDir() {
				return fmt.Errorf("incremental checkout: non-directory ancestor")
			}
		}
	}
	// A full checkout reads .gitattributes from the index, but checkout-index
	// reads the worktree copy first, which differs from its blob when the file
	// is itself converted on checkout (working-tree-encoding, ident). Read them
	// from the checked out commit, as the index has them. The index holds
	// exactly the paths to write, so --all needs no pathspecs.
	if checkout != nil {
		attrCheckout := checkout.New(gitutil.WithArgs("--attr-source=" + child))
		if _, err := attrCheckout.Run(ctx, "checkout-index", "--all", "--force"); err != nil {
			return err
		}
	}
	for _, entry := range plan.checkout {
		touched[entry.path] = true
	}
	return normalizeIncrementalGitCheckout(ctx, root, dest, touched)
}

func normalizeIncrementalGitCheckout(ctx context.Context, root *os.Root, dest string, touched map[string]bool) error {
	times := []unix.Timespec{{Sec: 1}, {Sec: 1}}
normalize:
	for p := range touched {
		if err := ctx.Err(); err != nil {
			return err
		}
		// A removed deep directory may now be below a replacement symlink.
		// Do not follow it even when it points back inside this same root.
		prefix := ""
		for _, part := range strings.Split(path.Dir(p), "/") {
			prefix = filepath.Join(prefix, part)
			info, err := root.Lstat(prefix)
			if errors.Is(err, os.ErrNotExist) || errors.Is(err, unix.ENOTDIR) {
				continue normalize
			}
			if err != nil {
				return err
			}
			if !info.IsDir() {
				continue normalize
			}
		}
		if _, err := root.Lstat(filepath.FromSlash(p)); errors.Is(err, os.ErrNotExist) || errors.Is(err, unix.ENOTDIR) {
			continue
		} else if err != nil {
			return err
		}
		// Ancestors were verified before checkout; the only symlinks just created
		// are terminal leaves. Never follow those when normalizing their timestamps.
		if err := unix.UtimesNanoAt(unix.AT_FDCWD, filepath.Join(dest, filepath.FromSlash(p)), times, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return err
		}
	}
	return nil
}
