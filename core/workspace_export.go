package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/util/gitutil"
)

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
	args := []string{"commit-tree", strings.TrimSpace(tree), "-m", "Dagger worktree transport"}
	for _, parent := range parents {
		args = append(args, "-p", parent)
	}
	env := []string{"GIT_AUTHOR_NAME=Dagger", "GIT_AUTHOR_EMAIL=dagger@localhost", "GIT_COMMITTER_NAME=Dagger", "GIT_COMMITTER_EMAIL=dagger@localhost", "GIT_AUTHOR_DATE=2000-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2000-01-01T00:00:00Z"}
	sha, err := runWorkspaceCommitGit(ctx, dir, env, args...)
	return strings.TrimSpace(sha), err
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
		snapshot := func(ref *GitRef, changes *Changeset) (string, error) {
			if ref != nil {
				if err := ref.Repo.Self().Backend.mount(ctx, 0, false, []GitRefBackend{ref.Backend}, func(remote *gitutil.GitCLI) error {
					url, err := remote.URL(ctx)
					if err != nil {
						return err
					}
					_, err = git("fetch", "--no-tags", "--no-write-fetch-head", "--no-recurse-submodules", url, ref.Ref.SHA)
					return err
				}); err != nil {
					return "", err
				}
				if _, err := git("reset", "--hard", ref.Ref.SHA); err != nil {
					return "", err
				}
			}
			content, err := changes.content(ctx)
			if err != nil {
				return "", err
			}
			if err := ws.applyContent(ctx, content); err != nil {
				return "", err
			}
			sha, err := workspaceSnapshotCommit(ctx, ws.workDir)
			if err != nil {
				return "", err
			}
			untracked, err := git("ls-files", "--others", "--directory", "-z")
			if err != nil {
				return "", err
			}
			if untracked != "" {
				return "", fmt.Errorf("git integration cannot export empty directories: %q", splitPullPaths(untracked))
			}
			// Make additions tracked before resetting to another input.
			_, err = git("reset", "--hard", sha)
			return sha, err
		}
		sourceTree, err := snapshot(source, sourceDirty)
		if err != nil {
			return err
		}
		var fromTree string
		if from != nil {
			opts.FromSHA = from.Ref.SHA
			fromTree, err = snapshot(from, fromDirty)
			if err != nil {
				return err
			}
		}
		if _, err := git("reset", "--hard", base); err != nil {
			return err
		}
		before, err := snapshot(nil, dirty)
		if err != nil {
			return err
		}
		if from != nil {
			if err := workspaceExportRestoreSavedUntracked(ctx, ws.workDir, base, from.Ref.SHA, fromTree); err != nil {
				return err
			}
		}
		before, err = workspaceSnapshotCommit(ctx, ws.workDir, base)
		if err != nil {
			return err
		}
		if _, err := git("reset", "--hard", before); err != nil {
			return err
		}
		if _, err := git("reset", "--hard", base); err != nil {
			return err
		}
		target, after, err := workspaceExportIntegrate(ctx, ws.workDir, base, before, source.Ref.SHA, sourceTree, fromTree, opts)
		if err != nil {
			return err
		}
		if _, err := git("read-tree", "--reset", "-u", after); err != nil {
			return err
		}
		transport, err := workspaceSnapshotCommit(ctx, ws.workDir, target, before)
		if err != nil {
			return err
		}
		if _, err := git("reset", "--hard", transport); err != nil {
			return err
		}
		return normalizeGitDirAfterCommit(ctx, ws.workDir)
	})
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
func workspaceExportIntegrate(ctx context.Context, dir, base, before, source, sourceTree, fromTree string, opts WorkspacePullOpts) (string, string, error) {
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
	if _, err := runWorkspacePullGit(ctx, dir, nil, "read-tree", "--reset", "-u", tree); err != nil {
		return "", "", err
	}
	integrated, err := workspaceSnapshotCommit(ctx, dir)
	if err != nil {
		return "", "", err
	}
	tree, err = merge(source, integrated, sourceTree)
	return target, tree, err
}

// WorkspaceExportDirectory packages committed history and both worktrees into
// one reachable closure: transport HEAD has parents (new HEAD, before snapshot),
// and the before snapshot has the original checkout HEAD as its sole parent.
func WorkspaceExportDirectory(ctx context.Context, repo dagql.ObjectResult[*Directory], baseSHA string, before, after *Changeset) (*Directory, error) {
	beforeContent, err := before.content(ctx)
	if err != nil {
		return nil, err
	}
	afterContent, err := after.content(ctx)
	if err != nil {
		return nil, err
	}
	return withGitMergeWorkspace(ctx, repo, "Prepare workspace integration export", func(ws *gitMergeWorkspace) error {
		head, err := runWorkspaceCommitGit(ctx, ws.workDir, nil, "rev-parse", "HEAD")
		if err != nil {
			return err
		}
		head = strings.TrimSpace(head)
		if _, err := runWorkspaceCommitGit(ctx, ws.workDir, nil, "reset", "--hard", baseSHA); err != nil {
			return err
		}
		if err := ws.applyContent(ctx, beforeContent); err != nil {
			return err
		}
		beforeSHA, err := workspaceSnapshotCommit(ctx, ws.workDir, baseSHA)
		if err != nil {
			return err
		}
		// Reset from the snapshot as well, so its newly added files disappear.
		if _, err := runWorkspaceCommitGit(ctx, ws.workDir, nil, "reset", "--hard", beforeSHA); err != nil {
			return err
		}
		if _, err := runWorkspaceCommitGit(ctx, ws.workDir, nil, "reset", "--hard", head); err != nil {
			return err
		}
		if err := ws.applyContent(ctx, afterContent); err != nil {
			return err
		}
		afterSHA, err := workspaceSnapshotCommit(ctx, ws.workDir, head, beforeSHA)
		if err != nil {
			return err
		}
		untracked, err := runWorkspaceCommitGit(ctx, ws.workDir, nil, "ls-files", "--others", "--directory", "-z")
		if err != nil {
			return err
		}
		if untracked != "" {
			return fmt.Errorf("git integration cannot export empty directories: %q", splitPullPaths(untracked))
		}
		if _, err := runWorkspaceCommitGit(ctx, ws.workDir, nil, "reset", "--hard", afterSHA); err != nil {
			return err
		}
		return normalizeGitDirAfterCommit(ctx, ws.workDir)
	})
}
