package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/dagger/dagger/dagql"
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
