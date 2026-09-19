package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/util/gitutil"
	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
)

// WorkspaceExportBaseAvailable is a non-evaluating eligibility check. A local
// backend can otherwise wrap a lazy host sync, a shallow tree, or arbitrary Git
// storage; its type and SHA alone do not prove that it owns usable history.
func (ref *GitRef) WorkspaceExportBaseAvailable() bool {
	if ref == nil || ref.Ref == nil || !gitutil.IsCommitSHA(ref.Ref.SHA) || ref.Repo.Self() == nil {
		return false
	}
	repo, ok := ref.Repo.Self().Backend.(*LocalGitRepository)
	if !ok || repo == nil || repo.Directory.Self() == nil {
		return false
	}
	backend, ok := ref.Backend.(*LocalGitRef)
	if !ok || backend == nil || backend.repo != repo || backend.Ref == nil || backend.SHA != ref.Ref.SHA {
		return false
	}
	dir := repo.Directory.Self()
	if dir.Snapshot == nil || dir.Dir == nil || len(dir.Services) != 0 {
		return false
	}
	snapshot, evaluated := dir.Snapshot.Peek()
	_, pathEvaluated := dir.Dir.Peek()
	return evaluated && snapshot != nil && pathEvaluated
}

// WorkspaceExportBaseReady proves readiness read-only. The schema caches this
// proof by the immutable GitRef result, so it is not a full-history scan on
// every save. Unsupported/incomplete storage falls back to destination capture;
// cancellation is not ineligibility. ImportGitBundle remains authoritative for
// exact-prerequisite isolation and validation of the captured bundle itself.
func (ref *GitRef) WorkspaceExportBaseReady(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if !ref.WorkspaceExportBaseAvailable() {
		return false, nil
	}
	ctx, span := Tracer(ctx).Start(ctx, "validate workspace export base", telemetry.Internal())
	defer span.End()
	repo := ref.Repo.Self().Backend.(*LocalGitRepository)
	if err := repo.ValidateSelfContained(ctx); err != nil {
		return false, ctx.Err()
	}
	err := repo.mount(ctx, 0, false, nil, func(git *gitutil.GitCLI) error {
		return workspaceExportBaseStorageReady(ctx, git, ref.Ref.SHA)
	})
	if err != nil {
		return false, ctx.Err()
	}
	return true, nil
}

func workspaceExportBaseStorageReady(ctx context.Context, git *gitutil.GitCLI, sha string) error {
	gitDir, err := git.GitDir(ctx)
	if err != nil {
		return err
	}
	// Do not borrow partial/shallow histories, alternates, replacements, or
	// grafts. These can make the same SHA appear to have a different closure.
	for _, name := range []string{"commondir", "shallow", "objects/info/alternates", "objects/info/http-alternates", "info/grafts"} {
		if _, err := os.Lstat(filepath.Join(gitDir, name)); !os.IsNotExist(err) {
			return fmt.Errorf("unsupported export base storage: %s", name)
		}
	}
	config, err := git.Run(ctx, "config", "--local", "--name-only", "--list")
	if err != nil {
		return err
	}
	for key := range strings.Lines(strings.ToLower(string(config))) {
		key = strings.TrimSpace(key)
		if key == "extensions.partialclone" || strings.HasSuffix(key, ".promisor") || key == "include.path" || strings.HasPrefix(key, "includeif.") {
			return fmt.Errorf("unsupported export base configuration")
		}
	}
	promisors, err := filepath.Glob(filepath.Join(gitDir, "objects", "pack", "*.promisor"))
	if err != nil || len(promisors) != 0 {
		return fmt.Errorf("partial export base storage")
	}
	replacements, err := git.Run(ctx, "for-each-ref", "--format=%(objectname)", "refs/replace/")
	if err != nil || len(replacements) != 0 {
		return fmt.Errorf("export base has replacement objects")
	}
	// --missing=print traverses the entire exact HEAD closure, including blob
	// existence, without lazily fetching missing promisor objects. No checkout,
	// pack, fetch, ref mutation, or retained alternate is used to qualify reuse.
	objects, err := git.Run(ctx, "rev-list", "--objects", "--no-object-names", "--missing=print", sha, "--")
	if err != nil {
		return err
	}
	if len(objects) == 0 || strings.HasPrefix(string(objects), "?") || strings.Contains(string(objects), "\n?") {
		return fmt.Errorf("export base has incomplete object closure")
	}
	return nil
}

// workspaceSnapshotCommit records transport-only worktree state. These commits
// are never installed on the user's branch or counted as agent commits.
func workspaceSnapshotCommit(ctx context.Context, dir string, parents ...string) (string, error) {
	// This scratch tree contains only committed files and explicit/approved
	// overlay content. Honor those edits even when a path is gitignored.
	if _, err := runWorkspaceCommitGit(ctx, dir, nil, "add", "-A", "-f"); err != nil {
		return "", err
	}
	tree, err := runWorkspaceCommitGit(ctx, dir, nil, "write-tree")
	if err != nil {
		return "", err
	}
	return workspaceSnapshotTreeCommit(ctx, dir, strings.TrimSpace(tree), parents...)
}

// workspaceSnapshotTreeCommit preserves the transport topology without reading
// the index or worktree. tree may also be a commit's explicit ^{tree} selector.
func workspaceSnapshotTreeCommit(ctx context.Context, dir, tree string, parents ...string) (string, error) {
	args := []string{"commit-tree", tree, "-m", "Dagger worktree transport"}
	for _, parent := range parents {
		args = append(args, "-p", parent)
	}
	env := []string{"GIT_AUTHOR_NAME=Dagger", "GIT_AUTHOR_EMAIL=dagger@localhost", "GIT_COMMITTER_NAME=Dagger", "GIT_COMMITTER_EMAIL=dagger@localhost", "GIT_AUTHOR_DATE=2000-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2000-01-01T00:00:00Z"}
	sha, err := runWorkspaceCommitGit(ctx, dir, env, args...)
	return strings.TrimSpace(sha), err
}

// workspaceExportContentEmpty deliberately retains directory entries, including
// empty directories that Git cannot represent. Only a genuinely empty delta may
// bypass application and the empty-directory validation below.
func workspaceExportContentEmpty(content *changesetContent) bool {
	p := content.paths
	return len(p.Added) == 0 && len(p.Modified) == 0 && len(p.Removed) == 0 && len(p.AllRemoved) == 0 && len(p.Renamed) == 0
}

// workspaceExportSnapshot reuses a known committed tree for an empty overlay,
// leaving the current checkout and index untouched. Nonempty overlays retain the
// full worktree path, including ignored additions and empty-directory checks.
func workspaceExportSnapshot(ctx context.Context, ws *gitMergeWorkspace, head string, content *changesetContent) (string, error) {
	if workspaceExportContentEmpty(content) {
		return workspaceSnapshotTreeCommit(ctx, ws.workDir, head+"^{tree}")
	}
	if _, err := runWorkspacePullGit(ctx, ws.workDir, nil, "reset", "--hard", head); err != nil {
		return "", err
	}
	if err := ws.applyContent(ctx, content); err != nil {
		return "", err
	}
	sha, err := workspaceSnapshotCommit(ctx, ws.workDir)
	if err != nil {
		return "", err
	}
	untracked, err := runWorkspacePullGit(ctx, ws.workDir, nil, "ls-files", "--others", "--directory", "-z")
	if err != nil {
		return "", err
	}
	if untracked != "" {
		return "", fmt.Errorf("git integration cannot export empty directories: %q", splitPullPaths(untracked))
	}
	// Make additions tracked before resetting to another input.
	_, err = runWorkspacePullGit(ctx, ws.workDir, nil, "reset", "--hard", sha)
	return sha, err
}

// WorkspaceSaveDirectory integrates a frozen source into a captured destination
// and packages the result for the client's checked, atomic integration writer.
// The destination capture excludes untracked files: only previously saved
// untracked paths from fromDirty are reconstructed, and the client verifies
// their actual contents before accepting the transport.
func WorkspaceSaveDirectory(ctx context.Context, repo dagql.ObjectResult[*Directory], dirty *Changeset, source *GitRef, sourceDirty *Changeset, from *GitRef, fromDirty *Changeset, opts WorkspacePullOpts) (*Directory, error) {
	ctx, cancel := context.WithTimeout(ctx, WorkspacePullTimeout)
	defer cancel()
	if err := opts.Validate(); err != nil {
		return nil, err
	}
	return withGitMergeWorkspace(ctx, repo, "Prepare workspace export", func(ws *gitMergeWorkspace) error {
		git := func(args ...string) (string, error) {
			out, err := runWorkspacePullGit(ctx, ws.workDir, nil, args...)
			return strings.TrimSpace(out), err
		}
		base, err := git("rev-parse", "HEAD")
		if err != nil {
			return err
		}
		worktreeHead := base
		snapshot := func(ctx context.Context, name string, ref *GitRef, changes *Changeset) (_ string, rerr error) {
			ctx, span := Tracer(ctx).Start(ctx, "construct "+name+" snapshot")
			defer telemetry.EndWithCause(span, &rerr)
			head := base
			if ref != nil {
				head = ref.Ref.SHA
				if err := ref.Repo.Self().Backend.mount(ctx, 0, false, []GitRefBackend{ref.Backend}, func(remote *gitutil.GitCLI) error {
					url, err := remote.URL(ctx)
					if err != nil {
						return err
					}
					_, err = runWorkspacePullGit(ctx, ws.workDir, nil, "fetch", "--no-tags", "--no-write-fetch-head", "--no-recurse-submodules", url, head)
					return err
				}); err != nil {
					return "", err
				}
			}
			content, err := changes.content(ctx)
			if err != nil {
				return "", err
			}
			empty := workspaceExportContentEmpty(content)
			span.SetAttributes(attribute.Bool("dagger.workspace.snapshot.reused_tree", empty))
			sha, err := workspaceExportSnapshot(ctx, ws, head, content)
			if err == nil && !empty {
				worktreeHead = sha
			}
			return sha, err
		}
		sourceTree, err := snapshot(ctx, "source", source, sourceDirty)
		if err != nil {
			return err
		}
		var fromTree string
		if from != nil {
			opts.FromSHA = from.Ref.SHA
			fromTree, err = snapshot(ctx, "previous save", from, fromDirty)
			if err != nil {
				return err
			}
		}
		if worktreeHead != base {
			if _, err := git("reset", "--hard", base); err != nil {
				return err
			}
			worktreeHead = base
		}
		if _, err := snapshot(ctx, "destination", nil, dirty); err != nil {
			return err
		}
		if from != nil {
			if err := workspaceExportRestoreSavedUntracked(ctx, ws.workDir, base, from.Ref.SHA, fromTree); err != nil {
				return err
			}
		}
		// Snapshot construction and saved-untracked restoration both leave their
		// contents staged. Reuse that index without scanning the worktree again.
		beforeTree, err := git("write-tree")
		if err != nil {
			return err
		}
		before, err := workspaceSnapshotTreeCommit(ctx, ws.workDir, beforeTree, base)
		if err != nil {
			return err
		}
		// Even an empty captured destination may have restored saved untracked
		// additions. Do not infer a clean index from its empty changeset alone.
		if worktreeHead != base || from != nil {
			if _, err := git("reset", "--hard", base); err != nil {
				return err
			}
		}
		target, after, err := workspaceExportIntegrate(ctx, ws.workDir, base, before, source.Ref.SHA, sourceTree, fromTree, opts)
		if err != nil {
			return err
		}
		return workspaceExportTransport(ctx, ws.workDir, target, before, after)
	})
}

// workspaceExportTransport materializes only the final merged tree; the
// transport's parents retain both the new history and the checked before state.
func workspaceExportTransport(ctx context.Context, dir, target, before, tree string) (rerr error) {
	ctx, span := Tracer(ctx).Start(ctx, "construct workspace export transport")
	defer telemetry.EndWithCause(span, &rerr)
	transport, err := workspaceSnapshotTreeCommit(ctx, dir, tree, target, before)
	if err != nil {
		return err
	}
	if _, err := runWorkspacePullGit(ctx, dir, nil, "reset", "--hard", transport); err != nil {
		return err
	}
	return normalizeGitDirAfterCommit(ctx, dir)
}

// Only comparator additions absent from destination HEAD may be untracked
// saved files. Never replace captured tracked dirt with comparator contents.
func workspaceExportRestoreSavedUntracked(ctx context.Context, dir, target, from, fromTree string) error {
	added, err := runWorkspacePullGit(ctx, dir, nil, "diff", "--no-renames", "--diff-filter=A", "--name-only", "-z", from, fromTree, "--")
	if err != nil {
		return err
	}
	tracked, err := runWorkspacePullGit(ctx, dir, nil, "ls-tree", "-r", "--name-only", "-z", target)
	if err != nil {
		return err
	}
	// Staged additions are captured too, although they are absent from HEAD.
	// Preserve their actual contents rather than substituting the comparator.
	captured, err := runWorkspacePullGit(ctx, dir, nil, "ls-files", "-z")
	if err != nil {
		return err
	}
	trackedPaths := splitPullPaths(tracked + captured)
	for _, p := range splitPullPaths(added) {
		if len(pullOverlappingPaths([]string{p}, trackedPaths)) > 0 {
			continue
		}
		if _, err := runWorkspacePullGit(ctx, dir, []string{"GIT_LITERAL_PATHSPECS=1"}, "restore", "--source="+fromTree, "--staged", "--worktree", "--", p); err != nil {
			return err
		}
	}
	return nil
}

// Commit integration and worktree integration deliberately have different
// bases. A save baseline is a source value, not destination HEAD: saved pending
// edits may become commits, and destination commits may have different hashes.
func workspaceExportIntegrate(ctx context.Context, dir, base, before, source, sourceTree, fromTree string, opts WorkspacePullOpts) (_ string, _ string, rerr error) {
	ctx, span := Tracer(ctx).Start(ctx, "integrate workspace export commits and pending edits")
	defer telemetry.EndWithCause(span, &rerr)
	if _, err := runWorkspacePullGit(ctx, dir, nil, "merge-base", base, source); err != nil {
		return "", "", fmt.Errorf("export requires related Git histories: %w", err)
	}
	if opts.FromSHA != "" {
		ancestor, err := pullIsAncestor(ctx, dir, opts.FromSHA, source)
		if err != nil {
			return "", "", err
		}
		if !ancestor {
			return "", "", fmt.Errorf("export from must be an ancestor of source HEAD; cannot export rewritten history incrementally")
		}
	}
	picks, err := foldWorkspacePull(ctx, dir, source, nil, opts)
	if err != nil {
		return "", "", err
	}
	for _, pick := range picks {
		if pick.Status == WorkspaceCommitConflict {
			return "", "", fmt.Errorf("cannot export commit %s: conflict on %s", pick.SHA, strings.Join(pick.ConflictPaths, ", "))
		}
	}
	target, err := runWorkspacePullGit(ctx, dir, nil, "rev-parse", "HEAD")
	if err != nil {
		return "", "", err
	}
	target = strings.TrimSpace(target)
	merge := func(base, ours, theirs string) (string, error) {
		out, err := runWorkspacePullGit(ctx, dir, nil, "merge-tree", "--write-tree", "--merge-base="+base, ours, theirs)
		if err != nil {
			return "", fmt.Errorf("workspace export conflicts with checkout changes: %s: %w", out, err)
		}
		return strings.Fields(out)[0], nil
	}
	if fromTree != "" {
		tree, err := merge(fromTree, before, sourceTree)
		return target, tree, err
	}
	// On a first save, consume matching destination dirt into new commits,
	// then merge source pending edits on top without copying unrelated files.
	tree, err := merge(base, target, before)
	if err != nil {
		return "", "", err
	}
	integrated, err := workspaceSnapshotTreeCommit(ctx, dir, tree)
	if err != nil {
		return "", "", err
	}
	tree, err = merge(source, integrated, sourceTree)
	return target, tree, err
}
