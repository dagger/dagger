package core

import (
	"context"
	"strings"
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
