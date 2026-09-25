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

func (ref *LocalGitRef) incrementalTree(ctx context.Context, srv *dagql.Server) (_ *Directory, supported bool, rerr error) {
	ctx, span := Tracer(ctx).Start(ctx, "materialize incremental git checkout", telemetry.Internal())
	defer func() {
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
		supported = true
		span.SetAttributes(attribute.Int("dagger.git.checkout.incremental.changed_paths", len(plan.changed)))
		parent := ref.repo.CheckoutBase.Tree
		if parent.Self() == nil {
			if err := srv.Select(ctx, ref.repo.CheckoutBase.Parent, &parent, dagql.Selector{Field: "tree", Args: []dagql.NamedInput{{Name: "discardGitDir", Value: dagql.Boolean(true)}}}); err != nil {
				return err
			}
		}
		snapshot, err := parent.Self().Snapshot.GetOrEval(ctx, parent.Result)
		if err != nil {
			return err
		}
		parentPath, err := parent.Self().Dir.GetOrEval(ctx, parent.Result)
		if err != nil {
			return err
		}
		child, err := query.SnapshotManager().New(ctx, snapshot, bkcache.WithRecordType(bkclient.UsageRecordTypeRegular), bkcache.WithDescription("incremental git source checkout"))
		if err != nil {
			return err
		}
		defer func() {
			if child != nil {
				rerr = errors.Join(rerr, child.Release(context.WithoutCancel(ctx)))
			}
		}()
		err = MountRef(ctx, child, func(root string, _ *mount.Mount) error {
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

type incrementalGitCheckoutPlan struct {
	changed  []string
	removed  []string // paths that existed in the parent, including modifications
	checkout []string
}

// No baseline filesystem traversal: tree/index scans are Git metadata only.
// A supported result is immutable evidence for the immediately following apply.
func planIncrementalGitCheckout(ctx context.Context, source *gitutil.GitCLI, parent, child string, ownedShallow ...bool) (*incrementalGitCheckoutPlan, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	if len(parent) != 40 || len(child) != 40 || !IsFullGitSHA(parent) || !IsFullGitSHA(child) {
		return nil, "commit-format", nil
	}
	if _, err := nativeCommitGitDirWithShallow(ctx, source.Dir(), len(ownedShallow) > 0 && ownedShallow[0]); err != nil {
		if nativeCommitFallback(err) {
			return nil, "repository-layout", nil
		}
		return nil, "", err
	}
	// Do not trust annotation alone, nor accept merges: the retained recipe
	// must be this commit's single actual parent in the current object database.
	source = source.New(gitutil.WithArgs("--no-replace-objects"))
	// Read the raw object, not revision traversal: info/grafts rewrites parents
	// even when replace refs are disabled. Only top-level commit headers count.
	commit, err := source.Run(ctx, "cat-file", "commit", child)
	if err != nil {
		return nil, "", err
	}
	headers, _, _ := strings.Cut(string(commit), "\n\n")
	var parents []string
	for _, line := range strings.Split(headers, "\n") {
		if sha, ok := strings.CutPrefix(line, "parent "); ok {
			parents = append(parents, sha)
		}
	}
	if len(parents) != 1 || parents[0] != parent {
		return nil, "parent-mismatch", nil
	}
	for _, sha := range []string{parent, child} {
		entries, err := source.Run(ctx, "ls-tree", "-r", "-z", sha)
		if err != nil {
			return nil, "", err
		}
		for _, entry := range splitOnNul(entries) {
			if strings.HasPrefix(entry, "160000 ") {
				return nil, "gitlinks", nil
			}
		}
	}
	changes, err := source.Run(ctx, "diff-tree", "--no-commit-id", "--no-renames", "--name-status", "-r", "-z", parent, child)
	if err != nil {
		return nil, "", err
	}
	entries := splitOnNul(changes)
	if len(entries)%2 != 0 {
		return nil, "", fmt.Errorf("invalid git tree diff")
	}
	plan := &incrementalGitCheckoutPlan{}
	for i := 0; i < len(entries); i += 2 {
		status, p := entries[i], entries[i+1]
		if path.Clean(p) != p || path.IsAbs(p) || p == ".." || strings.HasPrefix(p, "../") || gitMetaPath(p) {
			return nil, "unsafe-path", nil
		}
		if path.Base(p) == ".gitattributes" || path.Base(p) == ".gitmodules" {
			return nil, "checkout-controls", nil
		}
		plan.changed = append(plan.changed, p)
		switch status {
		case "A":
			plan.checkout = append(plan.checkout, p)
		case "D":
			plan.removed = append(plan.removed, p)
		case "M", "T":
			plan.removed = append(plan.removed, p)
			plan.checkout = append(plan.checkout, p)
		default:
			return nil, "", fmt.Errorf("unexpected git diff status %q", status)
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
	scratch, err := os.MkdirTemp("", "dagger-git-checkout-")
	if err != nil {
		return err
	}
	defer func() { rerr = errors.Join(rerr, os.RemoveAll(scratch)) }()
	// Identical clean config/checkout conversion to the full source checkout,
	// never the source repository's config, info/attributes or dirty worktree.
	checkout := source.New(gitutil.WithDir(dest), gitutil.WithWorkTree(dest), gitutil.WithGitDir(filepath.Join(scratch, "git")), gitutil.WithIndexFile(filepath.Join(scratch, "index")))
	if err := initLocalGitTreeCheckout(ctx, source, checkout); err != nil {
		return err
	}
	if _, err := checkout.Run(ctx, "read-tree", child); err != nil {
		return err
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
	for _, p := range plan.checkout {
		prefix := ""
		for _, part := range strings.Split(path.Dir(p), "/") {
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
	for _, batch := range batchPathSpecs(plan.checkout) {
		if _, err := checkout.Run(ctx, append([]string{"checkout-index", "--force", "--"}, batch...)...); err != nil {
			return err
		}
	}
	for _, p := range plan.checkout {
		touched[p] = true
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
