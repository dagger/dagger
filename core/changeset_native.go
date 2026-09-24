package core

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
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

// TryNativeWorkspaceMerge reconciles same-base local Git changes without
// restaging the baseline. The returned filesystem is a COW child of Before,
// not a raw After tree: Git normalizes only changed paths, while unchanged
// filesystem metadata survives. Temporary indexes, commits and objects never
// become part of the returned snapshot. False means an explicit eligibility
// fallback, never a failed merge, missing object, or cancellation.
func TryNativeWorkspaceMerge(ctx context.Context, working, incoming *Changeset) (_ *Directory, supported bool, rerr error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if working == nil || incoming == nil || working.Before.Self() == nil {
		return nil, false, nil
	}
	lazy, ok := working.Before.Self().Lazy.(*DirectoryGitTreeLazy)
	if !ok {
		return nil, false, nil
	}
	for _, changes := range []*Changeset{working, incoming} {
		ok, err := GitCommitChangesetNativeBase(ctx, lazy.Ref, changes)
		if err != nil || !ok {
			return nil, false, err
		}
	}
	ctx, span := Tracer(ctx).Start(ctx, "git native workspace merge", telemetry.Internal())
	defer func() {
		span.SetAttributes(attribute.Bool("dagger.git.native_merge.supported", supported))
		telemetry.EndWithCause(span, &rerr)
	}()
	contents := make([]*changesetContent, 2)
	for i, changes := range []*Changeset{working, incoming} {
		var err error
		contents[i], err = changes.content(ctx)
		if err != nil {
			return nil, true, err
		}
	}
	span.SetAttributes(attribute.Int("dagger.git.native_merge.scoped_stage_paths",
		len(commitStagePaths(contents[0].paths))+len(commitStagePaths(contents[1].paths))))
	local := lazy.Ref.Self().Backend.(*LocalGitRef)
	var result *Directory
	err := local.repo.mount(ctx, 0, false, nil, func(source *gitutil.GitCLI) error {
		out, err := source.Run(ctx, "rev-parse", "--absolute-git-dir")
		if err != nil {
			return err
		}
		gitDir, err := nativeCommitGitDir(ctx, strings.TrimSuffix(string(out), "\n"))
		if err != nil {
			return err
		}
		result, err = withGitMergeWorkspace(ctx, working.Before, "Workspace native reconciliation", func(ws *gitMergeWorkspace) error {
			paths := make([]*ChangesetPaths, 2)
			apply := make([]func(string) error, 2)
			for i, content := range contents {
				paths[i] = content.paths.withoutGitMeta()
				if err := validateNativeWorkspaceContent(ctx, ws.workDir, content); err != nil {
					return err
				}
				apply[i] = func(work string) error {
					return (&gitMergeWorkspace{root: work, dir: "/", workDir: work}).applyContent(ctx, content)
				}
			}
			return nativeWorkspaceMerge(ctx, filepath.Join(gitDir, "objects"), lazy.Ref.Self().Ref.SHA, ws.workDir, paths, apply)
		})
		return err
	})
	err = errors.Join(err, ctx.Err())
	if err != nil && result != nil {
		// The inner workspace may already have committed its snapshot when
		// the source mount fails to unmount, or cancellation arrives. Release
		// that result before abandoning it, preserving cleanup failures too.
		err = errors.Join(err, result.OnRelease(context.WithoutCancel(ctx)))
	}
	if nativeCommitFallback(err) {
		var reason nativeCommitUnsupportedReason
		if errors.As(err, &reason) {
			span.SetAttributes(attribute.String("dagger.git.native_merge.fallback_reason", string(reason)))
		}
		return nil, false, nil
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
		before, err := os.Lstat(filepath.Join(base, rel))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		stat := info.Sys().(*syscall.Stat_t)
		if before != nil && before.IsDir() {
			old := before.Sys().(*syscall.Stat_t)
			if info.Mode() != before.Mode() || stat.Uid != old.Uid || stat.Gid != old.Gid {
				return nativeCommitUnsupportedReason("directory-metadata")
			}
		} else if info.Mode().Perm() != 0755 || stat.Uid != uint32(os.Geteuid()) || stat.Gid != uint32(os.Getegid()) {
			return nativeCommitUnsupportedReason("directory-metadata")
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
func nativeWorkspaceMerge(ctx context.Context, parentObjects, parent, base string, paths []*ChangesetPaths, apply []func(string) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, changes := range paths {
		for _, p := range commitStagePaths(changes) {
			switch path.Base(p) {
			case ".gitattributes", ".gitignore":
				// Changing controls can restage otherwise unchanged baseline
				// files in the legacy whole-worktree add. Do not emulate that.
				return nativeCommitUnsupportedReason("merge-controls-change")
			}
		}
		for _, p := range changes.Added {
			if strings.HasSuffix(p, "/") && !slices.ContainsFunc(changes.Added, func(file string) bool {
				return !strings.HasSuffix(file, "/") && strings.HasPrefix(file, p)
			}) {
				return nativeCommitUnsupportedReason("empty-directory")
			}
		}
	}
	scratch, err := os.MkdirTemp("", "dagger-workspace-merge-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)
	meta := filepath.Join(scratch, "repo")
	if _, err := runWorkspaceCommitGit(ctx, scratch, nil, "init", "--bare", "--template=", "--object-format=sha1", meta); err != nil {
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
	run := func(args ...string) (string, error) {
		return runWorkspaceCommitGit(ctx, work, append(slices.Clone(env), "GIT_WORK_TREE="+work), args...)
	}
	commits := make([]string, 2)
	trees := make([]string, 2)
	for i, changes := range paths {
		work = filepath.Join(scratch, strconv.Itoa(i))
		if err := os.Mkdir(work, 0755); err != nil {
			return err
		}
		if err := stageNativeChanges(run, parent, changes, func() error {
			// Controls have been hydrated, but still describe the parent.
			if err := validateNativeWorkspaceBase(run, base, changes); err != nil {
				return err
			}
			return apply[i](work)
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
	}
	merged, err := run("merge-tree", "--write-tree", "--merge-base="+parent, commits[0], commits[1])
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("merge workspace changes: %w\n%s", err, merged)
	}
	merged = strings.TrimSpace(merged)
	// Replay the legacy worktree transitions, not a full checkout of merged.
	// A checkout rewrites only entries that differ between its two trees;
	// identical entries retain incoming permissions, ownership and raw bytes
	// (including CRLF). Applying raw content is part of that transition, not
	// a shortcut returning After in place of Git reconciliation.
	work = base
	if err := apply[0](base); err != nil {
		return err
	}
	if err := nativeWorkspaceCheckout(run, base, trees[0], parent); err != nil {
		return err
	}
	if err := apply[1](base); err != nil {
		return err
	}
	if err := nativeWorkspaceCheckout(run, base, trees[1], trees[0]); err != nil {
		return err
	}
	return nativeWorkspaceCheckout(run, base, trees[0], merged)
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
	var checkout []string
	for _, p := range splitOnNul([]byte(changed)) {
		if err := root.RemoveAll(filepath.FromSlash(p)); err != nil {
			return err
		}
		if !slices.Contains(deleted, p) {
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
