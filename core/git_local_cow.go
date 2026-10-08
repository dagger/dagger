package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/continuity/fs"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/wcprof"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/dagger/util/gitutil"
	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"
)

// cowTree materializes the full retained checkout of a local ref (depth 0,
// .git kept) as a copy-on-write child of an existing snapshot instead of
// fetching the entire history into an empty one. Snapshot ancestry pins and
// shares every existing object, as for native commits. In order of
// preference:
//
//   - delta: a child of the full checkout of the ref's checkout base (see
//     LocalGitRef.incrementalCheckoutIneligible), whose worktree is clean by
//     construction. Only the tree delta is written, and only the objects the
//     base lacks are fetched. Cost follows the change, not the tree.
//   - wipe: a child of the ref's repository snapshot, whose worktree (if any)
//     cannot be trusted. It is removed and the whole tree checked out again.
//
// False means the caller must use the full checkout: unsupported storage or
// layout (see cowGitCheckout), owned shallow history, a snapshot chain that is
// already too deep, or any other failure (see nativeFallback). Only the
// caller's cancellation surfaces. Which path was taken, and why the cheaper
// ones were not, is recorded on the span and as a wcprof marker (see
// recordCowCheckoutPath).
func (ref *LocalGitRef) cowTree(ctx context.Context, srv *dagql.Server, remotes []GitRemote, upstreamRemote *string) (_ *Directory, supported bool, rerr error) {
	profCtx := ctx
	ctx, span := Tracer(ctx).Start(ctx, "materialize copy-on-write git checkout", telemetry.Internal())
	path, detail := "full", ""
	var skipped []string
	var result *Directory
	defer func() {
		if rerr != nil && result != nil {
			rerr = errors.Join(rerr, result.OnRelease(context.WithoutCancel(ctx)))
			result = nil
		}
		if nativeFallback(ctx, span, "dagger.git.checkout.cow.fallback", rerr) {
			skipped = append(skipped, "wipe="+gitTreeFallbackCode(rerr))
			supported, rerr = false, nil
		}
		if !supported {
			path = "full"
		}
		span.SetAttributes(attribute.Bool("dagger.git.checkout.cow.supported", supported))
		recordCowCheckoutPath(profCtx, span, path, detail, skipped, rerr)
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
	result, base, reason, err := ref.cowDeltaTree(ctx, srv, remotes, upstreamRemote)
	if err != nil {
		return nil, false, err
	}
	if result != nil {
		path, detail = "delta", "base="+base
		return result, true, nil
	}
	skipped = append(skipped, "delta="+reason)
	path = "wipe"
	result, err = ref.cowWipeTree(ctx, remotes, upstreamRemote)
	if err != nil {
		return nil, false, err
	}
	return result, true, nil
}

// recordCowCheckoutPath names the path a copy-on-write checkout took (delta,
// wipe, or full when the caller must fetch), its detail and the reasons the
// cheaper paths were skipped: as span attributes dagger.git.checkout.cow.path,
// .detail and .skipped, and as a zero-length wcprof marker op of kind io,
// class "git.checkout.<path>[<detail and skip reasons>]".
func recordCowCheckoutPath(profCtx context.Context, span trace.Span, path, detail string, skipped []string, err error) {
	span.SetAttributes(
		attribute.String("dagger.git.checkout.cow.path", path),
		attribute.String("dagger.git.checkout.cow.detail", detail),
		attribute.StringSlice("dagger.git.checkout.cow.skipped", skipped),
	)
	if !wcprof.Enabled(profCtx) {
		return
	}
	why := strings.Join(append(append([]string{}, detail), skipped...), ",")
	why = strings.Trim(why, ",")
	class := "git.checkout." + path
	if why != "" {
		class += "[" + why + "]"
	}
	outcome := wcprof.OutcomeOK
	if err != nil {
		outcome = wcprof.OutcomeError
	}
	now := wcprof.NowNS()
	wcprof.RecordOp(profCtx, wcprof.OpKindIO, class, wcprof.OpOpts{Ident: why}, now, now, outcome)
}

// cowDeltaTree is the delta path of cowTree. A nil Directory means it does not
// apply, and reason says why; base says whether the base checkout was already
// materialized ("parent-checkout") or had to be evaluated first
// ("cold-parent-checkout"). Failures fall back too (see nativeFallback): only
// the caller's cancellation surfaces.
func (ref *LocalGitRef) cowDeltaTree(ctx context.Context, srv *dagql.Server, remotes []GitRemote, upstreamRemote *string) (_ *Directory, base, reason string, rerr error) {
	if reason := ref.incrementalCheckoutIneligible(); reason != "" {
		return nil, "", reason, nil
	}
	if !gitCheckoutRefsSupported(ref.Ref) {
		return nil, "", "ref-name", nil
	}
	outer := trace.SpanFromContext(ctx)
	ctx, span := Tracer(ctx).Start(ctx, "materialize delta git checkout", telemetry.Internal())
	var result *Directory
	defer func() {
		if rerr != nil && result != nil {
			rerr = errors.Join(rerr, result.OnRelease(context.WithoutCancel(ctx)))
			result = nil
		}
		if nativeFallback(ctx, span, "dagger.git.checkout.delta.fallback", rerr) {
			reason, rerr = gitTreeFallbackCode(rerr), nil
		}
		telemetry.EndWithCause(span, &rerr)
	}()
	baseRef := ref.repo.CheckoutBase.Parent
	err := ref.repo.mount(ctx, 0, false, nil, func(source *gitutil.GitCLI) error {
		if _, err := ref.repo.nativeGitDir(ctx, source.Dir()); err != nil {
			return err
		}
		// Gate on the delta before selecting or evaluating the base: these
		// are the same conditions under which the source-only tree applies
		// a delta to its base, and they hold for the retained worktree too.
		var plan *incrementalGitCheckoutPlan
		var err error
		plan, reason, err = planIncrementalGitCheckout(ctx, source, baseRef.Self().Ref.SHA, ref.SHA, false)
		if err != nil || reason != "" {
			return err
		}
		// The retained checkout also initializes submodules into .git, which
		// is rebuilt here: keep those on the full checkout.
		modules, err := source.New(gitutil.WithIgnoreError()).Run(ctx, "cat-file", "-t", ref.SHA+":.gitmodules")
		if err != nil {
			return err
		}
		if len(modules) != 0 {
			reason = "submodules"
			return nil
		}
		var checkout dagql.ObjectResult[*Directory]
		if err := srv.Select(ctx, baseRef, &checkout, dagql.Selector{Field: "__fullCheckout"}); err != nil {
			return err
		}
		cold := dagql.HasPendingLazyComputation(checkout)
		snapshot, selector, ok, err := incrementalParentTree(ctx, checkout)
		if err != nil {
			return err
		}
		if !ok {
			reason = "cold-parent"
			return nil
		}
		if filepath.Clean(selector) != "/" {
			reason = "parent-layout"
			return nil
		}
		base = "parent-checkout"
		if cold {
			base = "cold-parent-checkout"
		}
		span.SetAttributes(
			attribute.Int("dagger.git.checkout.delta.changed_paths", len(plan.changed)),
			attribute.String("dagger.git.checkout.delta.base", base),
		)
		result, err = cowCheckoutSnapshot(ctx, snapshot, ref.Ref, func(root string) error {
			return cowGitCheckout(ctx, root, source, remotes, upstreamRemote, ref.Ref, &cowCheckoutDelta{
				base: baseRef.Self().Ref.SHA,
				plan: plan,
			})
		}, span, outer)
		return err
	})
	err = errors.Join(err, ctx.Err())
	if err != nil {
		return nil, "", "", err
	}
	return result, base, reason, nil
}

// cowWipeTree is the wipe path of cowTree: a child of the repository's own
// snapshot. Errors make the caller fall back to the full checkout.
func (ref *LocalGitRef) cowWipeTree(ctx context.Context, remotes []GitRemote, upstreamRemote *string) (*Directory, error) {
	source := ref.repo.Directory
	parent, err := source.Self().Snapshot.GetOrEval(ctx, source.Result)
	if err != nil {
		return nil, err
	}
	selector, err := source.Self().Dir.GetOrEval(ctx, source.Result)
	if err != nil {
		return nil, err
	}
	return cowCheckoutSnapshot(ctx, parent, ref.Ref, func(root string) error {
		src, err := fs.RootPath(root, selector)
		if err != nil {
			return err
		}
		return cowGitCheckout(ctx, root, gitutil.NewGitCLI(gitutil.WithDir(src)), remotes, upstreamRemote, ref.Ref, nil)
	}, trace.SpanFromContext(ctx))
}

// cowCheckoutSnapshot runs fn in a new child of parent and commits it as a
// Directory at "/". Each such checkout stacks one more layer on its parent:
// past maxNativeSnapshotDepth, it fails, so the caller starts a fresh
// snapshot. The layer's disk usage is recorded on spans.
func cowCheckoutSnapshot(ctx context.Context, parent bkcache.ImmutableRef, ref *gitutil.Ref, fn func(root string) error, spans ...trace.Span) (_ *Directory, rerr error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	child, err := query.SnapshotManager().New(ctx, parent,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription(fmt.Sprintf("git local checkout (%s %s)", ref.Name, ref.SHA)))
	if err != nil {
		return nil, err
	}
	defer func() {
		if child != nil {
			rerr = errors.Join(rerr, child.Release(context.WithoutCancel(ctx)))
		}
	}()
	err = MountRef(ctx, child, func(root string, m *mount.Mount) error {
		if err := checkNativeSnapshotDepth(m); err != nil {
			return err
		}
		if err := fn(root); err != nil {
			return err
		}
		// The point of the exercise: the layer holds the changed worktree
		// and fresh metadata, never a copy of inherited objects or files.
		if total, objects, ok := overlayUpperBytes(m); ok {
			for _, span := range spans {
				span.SetAttributes(
					attribute.Int64("dagger.git.checkout.cow.layer_bytes", total),
					attribute.Int64("dagger.git.checkout.cow.layer_object_bytes", objects))
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	snap, err := child.Commit(ctx)
	if err != nil {
		return nil, err
	}
	child = nil
	dir := &Directory{
		Platform: query.Platform(),
		Dir:      new(LazyAccessor[string, *Directory]),
		Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]),
	}
	dir.SetPath("/")
	dir.SetSnapshot(snap)
	return dir, nil
}

// cowCheckoutDelta selects cowGitCheckout's delta mode: root holds the clean
// full checkout of base, to be moved to the ref by plan.
type cowCheckoutDelta struct {
	base string
	plan *incrementalGitCheckoutPlan
}

// cowGitCheckout turns root, a private writable snapshot, into the same
// retained checkout of ref that doGitCheckout would build in an empty
// directory (depth 0) from source, keeping objects in place instead of
// fetching them all. Only .git/objects survives (and, with delta, the
// worktree); the refs, configuration, index and every other piece of
// metadata are rebuilt by the same steps a fresh checkout takes.
//
// Without delta, root is a copy of the source repository itself (source's Git
// directory lies within it), and its worktree is checked out from scratch.
// With delta, root is a copy of the full checkout of delta.base: a clean
// worktree of that commit, by construction. Only the delta's paths are
// written (applyIncrementalGitCheckout, as for source-only trees), and the
// objects base lacks are fetched from source, negotiated from its refs.
//
// The result may hold objects unreachable from ref that a fresh checkout would
// not have; nothing observable depends on their absence. Everything else must
// match, which constrains the supported sources (unsupported ones return a
// nativeCommitUnsupportedReason):
//
//   - a complete, unshared SHA-1 object database with a files ref backend:
//     no shallow boundary, alternates, promisor packs, linked worktree or
//     reftable (nativeCommitGitDir), in source and in root.
//   - the Git directory is root/.git, where the checkout's own goes: either
//     a checkout at the snapshot root or a bare repository selected there,
//     as native commits produce. Moving a lower directory elsewhere would
//     copy it up.
//   - no hidden refs: a fresh fetch only sees the refs the source advertises.
//   - tags all peel to commits; see cowGitCheckoutTags.
func cowGitCheckout(ctx context.Context, root string, source *gitutil.GitCLI, remotes []GitRemote, upstreamRemote *string, ref *gitutil.Ref, delta *cowCheckoutDelta) error {
	root = filepath.Clean(root)
	sourceGitDir, err := nativeCommitGitDir(ctx, source.Dir())
	if err != nil {
		return err
	}
	gitDir := filepath.Join(root, ".git")
	if delta == nil {
		if filepath.Clean(sourceGitDir) != gitDir {
			return nativeCommitUnsupportedReason("repository-layout")
		}
	} else {
		baseGitDir, err := nativeCommitGitDir(ctx, root)
		if err != nil {
			return err
		}
		if filepath.Clean(baseGitDir) != gitDir {
			return nativeCommitUnsupportedReason("repository-layout")
		}
	}
	// Grafts rewrite history for every walk, upload-pack's included: rare
	// and deprecated, so fall back rather than model them. upload-pack
	// ignores replace refs; the tag walk below does too.
	if _, err := os.Lstat(filepath.Join(sourceGitDir, "info", "grafts")); err == nil {
		return nativeCommitUnsupportedReason("grafts")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
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

	checkoutGit := source.New(
		gitutil.WithDir(root),
		gitutil.WithWorkTree(root),
		gitutil.WithGitDir(gitDir),
	)
	reuse := gitCheckoutInheritedObjects
	if delta != nil {
		reuse = gitCheckoutInheritedWorktree
		if err := cowCheckoutDeltaWorktree(ctx, root, source, checkoutGit, gitURL, ref, tags, delta); err != nil {
			return err
		}
	}
	if err := wipeInheritedCheckout(ctx, root, gitDir, delta != nil); err != nil {
		return err
	}
	if _, err := checkoutGit.Run(ctx, "-c", "init.defaultBranch=main", "init"); err != nil {
		return err
	}
	// No fetch here: every object is already present. Even with nothing to
	// transfer, fetch would verify connectivity by walking every object
	// reachable from ref (there are no refs yet to stop at), and auto-follow
	// every source tag rather than only the reachable ones. Record what it
	// would: the reachable tags and the temporary ref finishGitCheckout
	// deletes again (which leaves its namespace directory behind, as after a
	// fetch).
	tmpref := "refs/dagger.tmp/" + identity.NewID()
	var stdin strings.Builder
	stdin.WriteString("create " + tmpref + " " + ref.SHA + "\n")
	for _, tag := range tags {
		stdin.WriteString("create " + tag.name + " " + tag.oid + "\n")
	}
	if _, err := checkoutGit.RunWithStdin(ctx, strings.NewReader(stdin.String()), "update-ref", "--stdin"); err != nil {
		return fmt.Errorf("failed to record fetched refs: %w", err)
	}
	if err := finishGitCheckout(ctx, checkoutGit, checkoutRemotes, gitURL, ref, false, tmpref, reuse); err != nil {
		return err
	}
	return writeGitRemoteSelection(ctx, checkoutGit, checkoutRemotes, upstream)
}

// cowCheckoutDeltaWorktree moves root, a clean full checkout of delta.base,
// to ref's worktree, and completes its object database from source while the
// base's refs are still there to negotiate (and stop the connectivity check)
// from. Its metadata is rebuilt afterwards.
func cowCheckoutDeltaWorktree(ctx context.Context, root string, source, checkoutGit *gitutil.GitCLI, sourceURL string, ref *gitutil.Ref, tags []cowGitCheckoutTag, delta *cowCheckoutDelta) error {
	head, err := checkoutGit.Run(ctx, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(head)) != delta.base {
		return nativeCommitUnsupportedReason("base-head")
	}
	// Fetch only what the base lacks: ref's history and any reachable tag
	// object it does not have. Keep every received pack whole (index-pack
	// never rewrites existing objects; unpacking loose ones could freshen
	// an inherited pack and copy it up) and never repack or write graphs.
	refspecs := []string{ref.SHA + ":refs/dagger.tmp/delta"}
	if len(tags) > 0 {
		var oids strings.Builder
		for _, tag := range tags {
			oids.WriteString(tag.oid + "\n")
		}
		out, err := checkoutGit.RunWithStdin(ctx, strings.NewReader(oids.String()), "cat-file", "--batch-check=%(objectname) %(objecttype)")
		if err != nil {
			return err
		}
		missing := map[string]bool{}
		for line := range strings.SplitSeq(strings.TrimSuffix(string(out), "\n"), "\n") {
			if oid, ok := strings.CutSuffix(line, " missing"); ok {
				missing[oid] = true
			}
		}
		for _, tag := range tags {
			if missing[tag.oid] {
				refspecs = append(refspecs, tag.name+":refs/dagger.tmp/delta-tags/"+strings.TrimPrefix(tag.name, "refs/tags/"))
			}
		}
	}
	fetchGit := checkoutGit.New(gitutil.WithArgs(
		"-c", "gc.auto=0",
		"-c", "maintenance.auto=false",
		"-c", "fetch.unpackLimit=1",
		"-c", "transfer.unpackLimit=1",
		"-c", "fetch.writeCommitGraph=false",
	))
	// Negotiate from HEAD: a full checkout's HEAD is usually detached, and
	// fetch otherwise only offers refs as common history (here at most a few
	// tags), so it would receive almost everything again.
	if _, err := fetchGit.Run(ctx, append([]string{"fetch", "--quiet", "--no-tags", "--no-write-fetch-head", "--no-recurse-submodules", "--negotiation-tip=HEAD", sourceURL}, refspecs...)...); err != nil {
		return fmt.Errorf("fetch delta objects: %w", err)
	}
	return applyIncrementalGitCheckout(ctx, source, root, ref.SHA, delta.plan)
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

// overlayUpperBytes measures the disk usage of an overlay mount's writable
// layer, in total and below .git/objects. False for other snapshotters.
func overlayUpperBytes(m *mount.Mount) (total, objects int64, ok bool) {
	if m == nil {
		return 0, 0, false
	}
	var upper string
	for _, opt := range m.Options {
		if dir, found := strings.CutPrefix(opt, "upperdir="); found {
			upper = dir
		}
	}
	if upper == "" {
		return 0, 0, false
	}
	objectsDir := filepath.Join(upper, ".git", "objects")
	err := filepath.WalkDir(upper, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		var used int64
		if st, isStat := info.Sys().(*syscall.Stat_t); isStat {
			used = st.Blocks * 512
		}
		total += used
		if path == objectsDir || strings.HasPrefix(path, objectsDir+string(filepath.Separator)) {
			objects += used
		}
		return nil
	})
	return total, objects, err == nil
}

// wipeInheritedCheckout removes everything but the object database and, with
// keepWorktree, the worktree.
func wipeInheritedCheckout(ctx context.Context, root, gitDir string, keepWorktree bool) (rerr error) {
	_, span := Tracer(ctx).Start(ctx, "remove inherited checkout", telemetry.Internal())
	defer telemetry.EndWithCause(span, &rerr)
	dirs := []struct{ path, keep string }{{gitDir, "objects"}}
	if !keepWorktree {
		dirs = append(dirs, struct{ path, keep string }{root, ".git"})
	}
	var paths []string
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir.path)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.Name() != dir.keep {
				paths = append(paths, filepath.Join(dir.path, entry.Name()))
			}
		}
	}
	span.SetAttributes(attribute.Int("dagger.git.checkout.removed_entries", len(paths)))
	return removeAllParallel(paths)
}

// cowWipeWorkers bounds the goroutines removing an inherited checkout. Each
// removal of a lower-layer file creates an overlay whiteout: metadata work
// that parallelizes across directories.
const cowWipeWorkers = 8

// removeAllParallel is os.RemoveAll of every path, spreading directories over
// a bounded number of goroutines. A goroutine waiting for its children never
// blocks a slot it needs: children only take free slots, or run inline.
func removeAllParallel(paths []string) error {
	var slots errgroup.Group
	slots.SetLimit(cowWipeWorkers - 1) // the caller's goroutine works too
	var removeEach func(paths []string) error
	remove := func(path string) error {
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.IsDir() {
			entries, err := os.ReadDir(path)
			if err != nil {
				return err
			}
			var dirs []string
			for _, entry := range entries {
				child := filepath.Join(path, entry.Name())
				if entry.IsDir() {
					dirs = append(dirs, child)
				} else if err := os.Remove(child); err != nil && !errors.Is(err, os.ErrNotExist) {
					return err
				}
			}
			if err := removeEach(dirs); err != nil {
				return err
			}
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	removeEach = func(paths []string) error {
		var wg sync.WaitGroup
		var mu sync.Mutex
		var errs []error
		for _, path := range paths {
			wg.Add(1)
			run := func() error {
				defer wg.Done()
				if err := remove(path); err != nil {
					mu.Lock()
					errs = append(errs, err)
					mu.Unlock()
				}
				return nil
			}
			if !slots.TryGo(run) {
				run()
			}
		}
		wg.Wait()
		return errors.Join(errs...)
	}
	return removeEach(paths)
}
