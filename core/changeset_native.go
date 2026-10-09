package core

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/dagger/dagger/util/gitutil"
	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	"golang.org/x/sys/unix"
)

// TryNativeWorkspaceMerge reconciles same-base Git changes without
// restaging the baseline. The returned filesystem is a COW child of Before,
// not a raw After tree: Git normalizes only changed paths, while unchanged
// filesystem metadata survives. Temporary indexes, commits and objects never
// become part of the returned snapshot. False means the caller must use the
// existing merge: an eligibility fallback or any failure of the native merge
// (see nativeFallback). Errors are the caller's own cancellation and merge
// conflicts, which are the merge's answer rather than a failure.
func TryNativeWorkspaceMerge(ctx context.Context, working, incoming *Changeset) (_ *Directory, supported bool, rerr error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if working == nil || incoming == nil || working.Before.Self() == nil {
		return nil, false, nil
	}
	ctx, span := Tracer(ctx).Start(ctx, "git native workspace merge", telemetry.Internal())
	phases := newMergePhases(span, "git.native_merge")
	defer func() {
		if nativeFallback(ctx, span, "dagger.git.native_merge.fallback_reason", rerr) {
			supported, rerr = false, nil
		}
		span.SetAttributes(attribute.Bool("dagger.git.native_merge.supported", supported))
		telemetry.EndWithCause(span, &rerr)
	}()
	// Ineligible provenance is a fallback like any other: record its reason,
	// prefixed with the side, rather than skipping the merge silently.
	lazy, ok := working.Before.Self().Lazy.(*DirectoryGitTreeLazy)
	if !ok {
		return nil, false, nativeCommitUnsupportedReason("working-before-not-git-tree")
	}
	for i, changes := range []*Changeset{working, incoming} {
		reason, err := gitCommitChangesetNativeBaseReason(ctx, lazy.Ref, changes)
		if err != nil {
			return nil, false, err
		}
		if reason != "" {
			return nil, false, nativeCommitUnsupportedReason(nativeMergeLabels[i] + "-" + reason)
		}
	}
	contents := make([]*changesetContent, 2)
	for i, changes := range []*Changeset{working, incoming} {
		if err := phases.run(ctx, "paths_"+nativeMergeLabels[i], func(ctx context.Context) error {
			_, err := changes.ComputePaths(ctx)
			return err
		}); err != nil {
			return nil, true, err
		}
		if err := phases.run(ctx, "content_"+nativeMergeLabels[i], func(ctx context.Context) (err error) {
			contents[i], err = changes.content(ctx)
			return err
		}); err != nil {
			return nil, true, err
		}
	}
	span.SetAttributes(attribute.Int("dagger.git.native_merge.scoped_stage_paths",
		len(commitStagePaths(contents[0].paths))+len(commitStagePaths(contents[1].paths))))
	var local *LocalGitRepository
	if err := phases.run(ctx, "repository", func(ctx context.Context) (err error) {
		local, err = nativeCommitRepository(ctx, lazy.Ref)
		return err
	}); err != nil {
		return nil, true, err
	}
	var result *Directory
	err := local.mount(ctx, 0, false, nil, func(source *gitutil.GitCLI) error {
		out, err := source.Run(ctx, "rev-parse", "--absolute-git-dir")
		if err != nil {
			return err
		}
		gitDir, err := local.nativeGitDir(ctx, strings.TrimSuffix(string(out), "\n"))
		if err != nil {
			return err
		}
		setup := phaseNow()
		var fnEnd phaseMark
		result, err = withGitMergeWorkspace(ctx, working.Before, "Workspace native reconciliation", func(ws *gitMergeWorkspace) error {
			defer func() { fnEnd = phaseNow() }()
			phases.record(ctx, "workspace", setup)
			paths := make([]*ChangesetPaths, 2)
			apply := make([]func(string) error, 2)
			for i, content := range contents {
				paths[i] = content.paths.withoutGitMeta()
				if err := phases.run(ctx, "validate_"+nativeMergeLabels[i], func(ctx context.Context) error {
					return validateNativeWorkspaceContent(ctx, ws.workDir, content)
				}); err != nil {
					return err
				}
				apply[i] = func(work string) error {
					return (&gitMergeWorkspace{root: work, dir: "/", workDir: work}).applyContent(ctx, content)
				}
			}
			return nativeWorkspaceMerge(ctx, filepath.Join(gitDir, "objects"), lazy.Ref.Self().Ref.SHA, ws.workDir, paths, apply, phases)
		})
		if err == nil {
			phases.record(ctx, "commit", fnEnd)
		}
		return err
	})
	err = errors.Join(err, ctx.Err())
	if err != nil && result != nil {
		// The inner workspace may already have committed its snapshot when
		// the source mount fails to unmount, or cancellation arrives. Release
		// that result before failing or falling back, preserving cleanup errors.
		err = errors.Join(err, result.OnRelease(context.WithoutCancel(ctx)))
	}
	if err != nil {
		return nil, true, err
	}
	return result, true, nil
}

// validateNativeWorkspaceContent walks only the materialized delta, never the
// baseline. File metadata changes omitted by ComputePaths and ancestor directory
// metadata cannot safely be represented by Git trees; leave those to the oracle.
func validateNativeWorkspaceContent(ctx context.Context, base string, content *changesetContent) error {
	if content.diff.Self() == nil {
		return nil
	}
	ref, err := content.diff.Self().Snapshot.GetOrEval(ctx, content.diff.Result)
	if err != nil {
		return err
	}
	selector, err := content.diff.Self().Dir.GetOrEval(ctx, content.diff.Result)
	if err != nil {
		return err
	}
	if ref == nil {
		return nil
	}
	return MountRef(ctx, ref, func(root string, _ *mount.Mount) error {
		dir, err := RootPathWithoutFinalSymlink(root, selector)
		if err != nil {
			return err
		}
		return validateNativeWorkspaceDelta(ctx, base, dir, content.paths.withoutGitMeta())
	}, mountRefAsReadOnly)
}

func validateNativeWorkspaceDelta(ctx context.Context, base, delta string, paths *ChangesetPaths) error {
	root, err := os.OpenRoot(base)
	if err != nil {
		return err
	}
	defer root.Close()
	// WalkDir visits parents first. Once a delta directory replaces a
	// non-directory (including a symlink), or is newly introduced, none of
	// its descendants exist in Before. Do not look through the old ancestor.
	introducedDirs := map[string]bool{}
	declared := map[string]bool{}
	for _, p := range slices.Concat(paths.Added, paths.Modified) {
		declared[p] = true
	}
	return filepath.WalkDir(delta, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(delta, name)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if gitMetaPath(rel) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.IsDir() {
			if !declared[rel] {
				return nativeCommitUnsupportedReason("unreported-filesystem-change")
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		var before os.FileInfo
		if !introducedDirs[path.Dir(rel)] {
			before, err = root.Lstat(filepath.FromSlash(rel))
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		if before == nil || !before.IsDir() {
			introducedDirs[rel] = true
		}
		stat := info.Sys().(*syscall.Stat_t)
		// A directory the delta introduces may carry any mode and owner (a
		// patch applied under the engine's umask 000 makes 0777 ones). The
		// replay applies the same raw delta and the same checkout transitions
		// as the legacy merge, so it ends with the same directory: Git's own
		// where a transition recreates it, the raw one where none touches it
		// (TestNativeWorkspaceMergeMatchesCheckout, introduced-directory-modes).
		// A delta that changes an existing directory's metadata still falls
		// back.
		if before != nil && before.IsDir() {
			old := before.Sys().(*syscall.Stat_t)
			if info.Mode() != before.Mode() || stat.Uid != old.Uid || stat.Gid != old.Gid {
				return nativeCommitUnsupportedReason("directory-metadata")
			}
		}
		// Git cannot reproduce directory xattrs. Even equal ones are uncommon;
		// conservatively fall back rather than widening the metadata contract.
		n, err := unix.Llistxattr(name, nil)
		if err != nil && !errors.Is(err, unix.ENOTSUP) {
			return err
		}
		if n != 0 {
			return nativeCommitUnsupportedReason("directory-xattrs")
		}
		return nil
	})
}

// nativeWorkspaceMerge is the filesystem-only transaction, also exercised by
// the checkout oracle tests. base is a private writable child; parentObjects is
// held read-only by its caller. Only scratch worktrees and delta paths are read.
func nativeWorkspaceMerge(ctx context.Context, parentObjects, parent, base string, paths []*ChangesetPaths, apply []func(string) error, phases *mergePhases) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateNativeMergePaths(paths); err != nil {
		return err
	}
	start := phaseNow()
	scratch, err := os.MkdirTemp("", "dagger-workspace-merge-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)
	meta := filepath.Join(scratch, "repo")
	if _, err := runWorkspaceCommitGit(ctx, scratch, nil, "init", "--bare", "--template=", "--object-format=sha1", "--ref-format=files", meta); err != nil {
		return err
	}
	if err := copyGitShallowBoundary(filepath.Dir(parentObjects), meta); err != nil {
		return err
	}
	env := []string{
		"GIT_DIR=" + meta,
		"GIT_INDEX_FILE=" + filepath.Join(scratch, "index"),
		"GIT_ALTERNATE_OBJECT_DIRECTORIES=" + strconv.Quote(parentObjects),
		"GIT_LITERAL_PATHSPECS=1", "GIT_NO_LAZY_FETCH=1", "GIT_NO_REPLACE_OBJECTS=1",
		"GIT_AUTHOR_DATE=2000-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2000-01-01T00:00:00Z",
	}
	work := ""
	// Each phase runs its git commands under its own profile op, so a
	// profile breaks a slow phase down by command (git.<verb>).
	runCtx := ctx
	run := func(args ...string) (string, error) {
		return runWorkspaceCommitGit(runCtx, work, append(slices.Clone(env), "GIT_WORK_TREE="+work), args...)
	}
	phase := func(name string, fn func() error) error {
		outer := runCtx
		return phases.run(outer, name, func(ctx context.Context) error {
			runCtx = ctx
			defer func() { runCtx = outer }()
			return fn()
		})
	}
	// The raw content applied to a worktree, timed apart from the git
	// commands around it.
	applyTo := func(i int, dir string) error {
		return phases.run(runCtx, "apply_"+nativeMergeLabels[i], func(context.Context) error {
			return apply[i](dir)
		})
	}
	commits := make([]string, 2)
	trees := make([]string, 2)
	phases.record(ctx, "init", start)
	for i, changes := range paths {
		if err := phase("stage_"+nativeMergeLabels[i], func() error {
			work = filepath.Join(scratch, strconv.Itoa(i))
			if err := os.Mkdir(work, 0755); err != nil {
				return err
			}
			if err := stageNativeChanges(run, parent, changes, false, func() error {
				// Controls have been hydrated, but still describe the parent.
				if err := validateNativeWorkspaceBase(run, base, changes); err != nil {
					return err
				}
				return applyTo(i, work)
			}); err != nil {
				return err
			}
			tree, err := run("write-tree")
			if err != nil {
				return err
			}
			trees[i] = strings.TrimSpace(tree)
			commit, err := run("commit-tree", trees[i], "-p", parent, "-m", "workspace merge")
			if err != nil {
				return err
			}
			commits[i] = strings.TrimSpace(commit)
			// merge-tree labels conflict messages with its arguments. Name the
			// sides instead of printing scratch commit IDs nobody can look up.
			_, err = run("update-ref", "refs/heads/"+nativeMergeLabels[i], commits[i])
			return err
		}); err != nil {
			return err
		}
	}
	var merged string
	err = phase("merge_tree", func() (err error) {
		merged, err = run("merge-tree", "--write-tree", "--name-only", "--merge-base="+parent, nativeMergeLabels[0], nativeMergeLabels[1])
		return err
	})
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return newNativeMergeConflict(merged)
		}
		return fmt.Errorf("merge workspace changes: %w", err)
	}
	merged = strings.TrimSpace(merged)
	// Replay the legacy worktree transitions, not a full checkout of merged.
	// A checkout rewrites only entries that differ between its two trees;
	// identical entries retain incoming permissions, ownership and raw bytes
	// (including CRLF). Applying raw content is part of that transition, not
	// a shortcut returning After in place of Git reconciliation.
	return phase("replay", func() error {
		work = base
		if err := applyTo(0, base); err != nil {
			return err
		}
		if err := nativeWorkspaceCheckout(run, base, trees[0], parent); err != nil {
			return err
		}
		if err := applyTo(1, base); err != nil {
			return err
		}
		if err := nativeWorkspaceCheckout(run, base, trees[1], trees[0]); err != nil {
			return err
		}
		return nativeWorkspaceCheckout(run, base, trees[0], merged)
	})
}

// validateNativeMergePaths rejects deltas the native merge does not emulate:
// changed Git controls and empty added directories.
func validateNativeMergePaths(paths []*ChangesetPaths) error {
	for _, changes := range paths {
		for _, p := range commitStagePaths(changes) {
			switch path.Base(p) {
			case ".gitattributes", ".gitignore":
				// Changing controls can restage otherwise unchanged baseline
				// files in the legacy whole-worktree add. Do not emulate that.
				return nativeCommitUnsupportedReason("merge-controls-change")
			}
		}
		// An added directory must be an ancestor of some added file.
		filled := map[string]bool{}
		for _, p := range changes.Added {
			if strings.HasSuffix(p, "/") {
				continue
			}
			for dir := path.Dir(p); dir != "." && dir != "/"; dir = path.Dir(dir) {
				if filled[dir+"/"] {
					break // its ancestors were recorded with it
				}
				filled[dir+"/"] = true
			}
		}
		for _, p := range changes.Added {
			if strings.HasSuffix(p, "/") && !filled[p] {
				return nativeCommitUnsupportedReason("empty-directory")
			}
		}
	}
	return nil
}

// nativeMergeLabels name the two sides in merge-tree's conflict messages: the
// workspace's pending edits and the changes being committed.
var nativeMergeLabels = [2]string{"workspace", "incoming"}

// nativeMergeConflict is merge-tree's answer, not a failure of the native
// merge: it merges on HEAD's real tree, so the legacy merge, restaging the
// whole baseline, has nothing better to report. nativeFallback returns it.
type nativeMergeConflict string

func (conflict nativeMergeConflict) Error() string { return string(conflict) }

// newNativeMergeConflict reports a merge-tree conflict by the conflicted
// paths and Git's own CONFLICT messages, dropping the merged tree ID and
// "Auto-merging" noise. With --name-only, the output is the tree ID, one
// conflicted path per line, a blank line, then informational messages.
func newNativeMergeConflict(out string) error {
	info, messages, _ := strings.Cut(strings.TrimRight(out, "\n"), "\n\n")
	var paths []string
	if _, rest, ok := strings.Cut(info, "\n"); ok {
		paths = strings.Split(rest, "\n")
	}
	var conflicts []string
	for _, line := range strings.Split(messages, "\n") {
		if strings.HasPrefix(line, "CONFLICT") {
			conflicts = append(conflicts, line)
		}
	}
	msg := "merge conflict between workspace and incoming changes"
	if len(paths) > 0 {
		msg += " in " + strings.Join(paths, ", ")
	}
	if len(conflicts) > 0 {
		msg += ":\n" + strings.Join(conflicts, "\n")
	}
	return nativeMergeConflict(msg)
}

// nativeWorkspaceCheckout performs only the filesystem writes a Git tree
// transition requires. The private index is never stored in the output layer.
func nativeWorkspaceCheckout(run func(...string) (string, error), base, from, to string) error {
	changed, err := run("diff-tree", "--no-commit-id", "--no-renames", "--name-only", "-r", "-z", from, to)
	if err != nil {
		return err
	}
	removed, err := run("diff-tree", "--no-commit-id", "--no-renames", "--diff-filter=D", "--name-only", "-r", "-z", from, to)
	if err != nil {
		return err
	}
	if _, err := run("read-tree", to); err != nil {
		return err
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return err
	}
	defer root.Close()
	deleted := splitOnNul([]byte(removed))
	isDeleted := make(map[string]bool, len(deleted))
	for _, p := range deleted {
		isDeleted[p] = true
	}
	var checkout []string
	for _, p := range splitOnNul([]byte(changed)) {
		if err := root.RemoveAll(filepath.FromSlash(p)); err != nil {
			return err
		}
		if !isDeleted[p] {
			checkout = append(checkout, p)
		}
	}
	// Prune only ancestors of removed files. Git checkout removes empty parent
	// directories, but leaves unrelated baseline directories and metadata alone.
	for _, p := range deleted {
		for dir := path.Dir(p); dir != "."; dir = path.Dir(dir) {
			err := root.Remove(filepath.FromSlash(dir))
			if errors.Is(err, unix.ENOTEMPTY) || errors.Is(err, unix.EEXIST) {
				break
			}
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	for _, p := range checkout {
		// Validate existing ancestors without creating them: Git must create
		// missing directories with its own mode (0777 masked by the engine's
		// umask), not the metadata publication helper's fixed 0755.
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
				return nativeCommitUnsupportedReason("unsafe-write-path")
			}
		}
	}
	for _, batch := range batchPathSpecs(checkout) {
		if _, err := run(append([]string{"checkout-index", "--force", "--"}, batch...)...); err != nil {
			return err
		}
	}
	return nil
}

// Validate only declared paths against baseline Git evidence. This catches
// tracked ignored files and historical blobs whose current attributes would
// produce a different synthetic base in the legacy merge, without git add -A.
func validateNativeWorkspaceBase(run func(...string) (string, error), base string, paths *ChangesetPaths) error {
	for _, batch := range batchPathSpecs(commitStagePaths(paths)) {
		// check-ignore takes literal filenames, but rejects the literal
		// pathspec flag supported by add/ls-files. Prefix ./ to prevent a
		// leading colon from becoming pathspec magic when clearing that flag.
		ignoreArgs := []string{"--no-literal-pathspecs", "check-ignore", "--no-index", "--"}
		for _, p := range batch {
			ignoreArgs = append(ignoreArgs, "./"+p)
		}
		ignored, err := run(ignoreArgs...)
		if err != nil {
			return err
		}
		if ignored != "" {
			return nativeCommitUnsupportedReason("ignored-merge-path")
		}
		// ls-files matches only at or under each declared path, never a
		// sibling: a gitlink is either declared itself or inside a directory
		// a declared file replaced.
		entries, err := run(append([]string{"ls-files", "--stage", "-z", "--"}, batch...)...)
		if err != nil {
			return err
		}
		for _, entry := range splitOnNul([]byte(entries)) {
			header, name, ok := strings.Cut(entry, "\t")
			fields := strings.Fields(header)
			if !ok || len(fields) != 3 {
				return fmt.Errorf("invalid index entry")
			}
			if fields[0] == "120000" {
				continue // symlinks have no clean conversion
			}
			if fields[0] == "160000" {
				return nativeCommitUnsupportedReason("gitlink-change")
			}
			blob, err := run("hash-object", "--path="+name, "--", filepath.Join(base, name))
			if err != nil {
				return err
			}
			if strings.TrimSpace(blob) != fields[1] {
				return nativeCommitUnsupportedReason("noncanonical-merge-base")
			}
		}
	}
	return nil
}
