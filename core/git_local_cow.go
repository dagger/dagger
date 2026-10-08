package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/continuity/fs"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/dagger/util/gitutil"
	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
)

// cowTree materializes the full retained checkout of a local ref (depth 0,
// .git kept) as a copy-on-write child of its repository's own snapshot,
// instead of fetching the entire history into an empty one. Snapshot ancestry
// pins and shares every existing object, as for native commits; the new layer
// holds only the worktree, the index and fresh Git metadata.
//
// False means the caller must use the full checkout: unsupported storage or
// layout (see cowGitCheckout), owned shallow history, a snapshot chain that is
// already too deep, or any other failure (see nativeFallback). Only the
// caller's cancellation surfaces.
func (ref *LocalGitRef) cowTree(ctx context.Context, remotes []GitRemote, upstreamRemote *string) (_ *Directory, supported bool, rerr error) {
	ctx, span := Tracer(ctx).Start(ctx, "materialize copy-on-write git checkout", telemetry.Internal())
	var result *Directory
	defer func() {
		if rerr != nil && result != nil {
			rerr = errors.Join(rerr, result.OnRelease(context.WithoutCancel(ctx)))
			result = nil
		}
		if nativeFallback(ctx, span, "dagger.git.checkout.cow.fallback", rerr) {
			supported, rerr = false, nil
		}
		span.SetAttributes(attribute.Bool("dagger.git.checkout.cow.supported", supported))
		telemetry.EndWithCause(span, &rerr)
	}()
	if ref.Ref == nil || len(ref.SHA) != 40 || !IsFullGitSHA(ref.SHA) {
		return nil, false, nativeCommitUnsupportedReason("commit-format")
	}
	// Owned shallow storage hydrates its complete history into a different
	// (root bare) repository first; the full checkout handles that.
	if ref.repo.HistorySource.Self() != nil {
		return nil, false, nativeCommitUnsupportedReason("owned-shallow-history")
	}
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, false, err
	}
	source := ref.repo.Directory
	parent, err := source.Self().Snapshot.GetOrEval(ctx, source.Result)
	if err != nil {
		return nil, false, err
	}
	selector, err := source.Self().Dir.GetOrEval(ctx, source.Result)
	if err != nil {
		return nil, false, err
	}
	child, err := query.SnapshotManager().New(ctx, parent,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription(fmt.Sprintf("git local checkout (%s %s)", ref.Ref.Name, ref.Ref.SHA)))
	if err != nil {
		return nil, false, err
	}
	defer func() {
		if child != nil {
			rerr = errors.Join(rerr, child.Release(context.WithoutCancel(ctx)))
		}
	}()
	err = MountRef(ctx, child, func(root string, m *mount.Mount) error {
		// Each checkout stacks one more layer on its repository's snapshot.
		// Past the bound, the full checkout starts a fresh snapshot.
		if err := checkNativeSnapshotDepth(m); err != nil {
			return err
		}
		src, err := fs.RootPath(root, selector)
		if err != nil {
			return err
		}
		return cowGitCheckout(ctx, root, src, remotes, upstreamRemote, ref.Ref)
	})
	if err != nil {
		return nil, false, err
	}
	snap, err := child.Commit(ctx)
	if err != nil {
		return nil, false, err
	}
	child = nil
	result = &Directory{
		Platform: query.Platform(),
		Dir:      new(LazyAccessor[string, *Directory]),
		Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]),
	}
	result.SetPath("/")
	result.SetSnapshot(snap)
	if err := ctx.Err(); err != nil {
		return nil, true, err
	}
	return result, true, nil
}

// cowGitCheckout turns root, a private writable copy of the source repository
// at src, into the same retained checkout of ref that doGitCheckout would
// build in an empty directory (depth 0), keeping the source's object database
// in place instead of fetching it: only .git/objects survives, and the
// worktree, index, refs, configuration and every other piece of metadata are
// rebuilt by the same steps a fresh checkout takes.
//
// The result may hold objects unreachable from ref that a fresh checkout would
// not have; nothing observable depends on their absence. Everything else must
// match, which constrains the supported sources (unsupported ones return a
// nativeCommitUnsupportedReason):
//
//   - a complete, unshared SHA-1 object database with a files ref backend:
//     no shallow boundary, alternates, promisor packs, linked worktree or
//     reftable (nativeCommitGitDir).
//   - the Git directory is root/.git, where the checkout's own goes: either
//     a checkout at the snapshot root or a bare repository selected there,
//     as native commits produce. Moving a lower directory elsewhere would
//     copy it up.
//   - no hidden refs: a fresh fetch only sees the refs the source advertises.
//   - tags all peel to commits; see cowGitCheckoutTags.
func cowGitCheckout(ctx context.Context, root, src string, remotes []GitRemote, upstreamRemote *string, ref *gitutil.Ref) error {
	gitDir, err := nativeCommitGitDir(ctx, src)
	if err != nil {
		return err
	}
	gitDir = filepath.Clean(gitDir)
	root = filepath.Clean(root)
	if gitDir != filepath.Join(root, ".git") {
		return nativeCommitUnsupportedReason("repository-layout")
	}
	// Grafts rewrite history for every walk, upload-pack's included: rare
	// and deprecated, so fall back rather than model them. upload-pack
	// ignores replace refs; the tag walk below does too.
	if _, err := os.Lstat(filepath.Join(gitDir, "info", "grafts")); err == nil {
		return nativeCommitUnsupportedReason("grafts")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	source := gitutil.NewGitCLI(gitutil.WithDir(src))
	gitURL, err := source.URL(ctx)
	if err != nil {
		return fmt.Errorf("could not find git url: %w", err)
	}
	out, err := source.New(gitutil.WithIgnoreError()).Run(ctx, "rev-parse", "--verify", "--quiet", ref.SHA+"^{commit}")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(out)) != ref.SHA {
		return fmt.Errorf("commit %s is not in the local repository", ref.SHA)
	}
	hidden, err := source.New(gitutil.WithIgnoreError()).Run(ctx, "config", "--get-regexp", `^(transfer|uploadpack)\.hiderefs$`)
	if err != nil {
		return err
	}
	if len(hidden) != 0 {
		return nativeCommitUnsupportedReason("hidden-refs")
	}
	tags, err := cowGitCheckoutTags(ctx, source.New(gitutil.WithArgs("--no-replace-objects")), ref.SHA)
	if err != nil {
		return err
	}
	checkoutRemotes, upstream, err := localCheckoutRemotes(ctx, source, ref.Name, remotes, upstreamRemote)
	if err != nil {
		return err
	}

	if err := removeAllExcept(root, ".git"); err != nil {
		return err
	}
	if err := removeAllExcept(gitDir, "objects"); err != nil {
		return err
	}
	checkoutGit := source.New(
		gitutil.WithDir(root),
		gitutil.WithWorkTree(root),
		gitutil.WithGitDir(gitDir),
	)
	if _, err := checkoutGit.Run(ctx, "-c", "init.defaultBranch=main", "init"); err != nil {
		return err
	}
	// No fetch: every object is already here. Even with nothing to transfer,
	// fetch would verify connectivity by walking every object reachable from
	// ref (there are no refs yet to stop at), and auto-follow every source tag
	// rather than only the reachable ones. Record what it would: the reachable
	// tags and the temporary ref finishGitCheckout deletes again (which leaves
	// its namespace directory behind, as after a fetch).
	tmpref := "refs/dagger.tmp/" + identity.NewID()
	var stdin strings.Builder
	stdin.WriteString("create " + tmpref + " " + ref.SHA + "\n")
	for _, tag := range tags {
		stdin.WriteString("create " + tag.name + " " + tag.oid + "\n")
	}
	if _, err := checkoutGit.RunWithStdin(ctx, strings.NewReader(stdin.String()), "update-ref", "--stdin"); err != nil {
		return fmt.Errorf("failed to record fetched refs: %w", err)
	}
	if err := finishGitCheckout(ctx, checkoutGit, checkoutRemotes, gitURL, ref, false, tmpref, true); err != nil {
		return err
	}
	return writeGitRemoteSelection(ctx, checkoutGit, checkoutRemotes, upstream)
}

type cowGitCheckoutTag struct {
	name, oid string
}

// cowGitCheckoutTags lists the tags a fresh checkout's fetch would create by
// auto-following: those whose peeled object it received, which at depth 0 is
// exactly the history of sha. --merged computes that for commits (a commit
// walk, not the object walk fetch's connectivity check costs). A tag peeling
// to a tree or blob could be in that history too: rare enough to fall back.
func cowGitCheckoutTags(ctx context.Context, source *gitutil.GitCLI, sha string) ([]cowGitCheckoutTag, error) {
	out, err := source.Run(ctx, "for-each-ref", "--format=%(objecttype) %(*objecttype)", "refs/tags/")
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, nil
	}
	for line := range strings.SplitSeq(strings.TrimSuffix(string(out), "\n"), "\n") {
		if line != "commit " && line != "tag commit" {
			return nil, nativeCommitUnsupportedReason("tag-target")
		}
	}
	out, err = source.Run(ctx, "for-each-ref", "--merged="+sha, "--format=%(objectname) %(refname)", "refs/tags/")
	if err != nil {
		return nil, err
	}
	var tags []cowGitCheckoutTag
	for line := range strings.SplitSeq(strings.TrimSuffix(string(out), "\n"), "\n") {
		if line == "" {
			continue
		}
		oid, name, ok := strings.Cut(line, " ")
		if !ok || !strings.HasPrefix(name, "refs/tags/") {
			return nil, fmt.Errorf("invalid tag listing %q", line)
		}
		tags = append(tags, cowGitCheckoutTag{name: name, oid: oid})
	}
	return tags, nil
}

// removeAllExcept empties dir but for one entry. Entries from lower snapshot
// layers become whiteouts; nothing is copied up.
func removeAllExcept(dir, keep string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == keep {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}
