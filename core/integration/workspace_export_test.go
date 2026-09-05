package core

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func exportWorkspace(ctx context.Context, c *dagger.Client, source, target *dagger.Workspace) error {
	sourceID, err := source.ID(ctx)
	if err != nil {
		return err
	}
	targetID, err := target.ID(ctx)
	if err != nil {
		return err
	}
	return c.Do(ctx, &dagger.Request{
		Query:     `query($source: ID!, $target: ID!) { node(id: $source) { ... on Workspace { export(to: $target) } } }`,
		Variables: map[string]any{"source": sourceID, "target": targetID},
	}, &dagger.Response{})
}

func workspaceExportCheckout(ctx context.Context, t *testctx.T) (string, func(...string) string) {
	t.Helper()
	checkout := t.TempDir()
	initGitRepo(ctx, t, checkout)
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = checkout
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s", out)
		return strings.TrimSpace(string(out))
	}
	require.NoError(t, os.WriteFile(filepath.Join(checkout, "base.txt"), []byte("base"), 0o644))
	git("add", ".")
	git("commit", "-m", "initial")
	return checkout, git
}

func (WorkspaceSuite) TestWorkspaceExportCommitsAndOverlay(ctx context.Context, t *testctx.T) {
	checkout, git := workspaceExportCheckout(ctx, t)
	c := connect(ctx, t, dagger.WithWorkdir(checkout))
	target := c.CurrentWorkspace()
	base, err := target.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	// Load host data first to prove export invalidates earlier cached reads.
	_, err = target.File("base.txt").Contents(ctx)
	require.NoError(t, err)
	ws := target.Checkpoint().WithNewFile("base.txt", "committed").
		WithNewFile("pending.txt", "pending").
		WithMountedDirectory("mounted", c.Directory().WithNewFile("private.txt", "mount"))
	committed, err := commitWorkspace(ctx, c, ws, "engine commit", []string{"base.txt"})
	require.NoError(t, err)
	frozen := dagger.Ref[*dagger.Workspace](c, committed.ID)
	require.Equal(t, base, git("rev-parse", "HEAD"))
	require.NoError(t, exportWorkspace(ctx, c, frozen, target))
	require.Equal(t, committed.Git.Head.Commit, git("rev-parse", "HEAD"))
	require.Equal(t, "engine commit", git("log", "-1", "--format=%s"))
	require.Equal(t, "?? pending.txt", git("status", "--porcelain"))
	for name, want := range map[string]string{"base.txt": "committed", "pending.txt": "pending"} {
		data, err := os.ReadFile(filepath.Join(checkout, name))
		require.NoError(t, err)
		require.Equal(t, want, string(data))
	}
	_, err = os.Stat(filepath.Join(checkout, "mounted"))
	require.ErrorIs(t, err, os.ErrNotExist)
	contents, err := c.CurrentWorkspace().File("base.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "committed", contents)
	// Repeated exports are effectful and do not produce duplicate commits.
	require.NoError(t, os.WriteFile(filepath.Join(checkout, "pending.txt"), []byte("changed outside"), 0o644))
	require.NoError(t, exportWorkspace(ctx, c, frozen, target))
	data, err := os.ReadFile(filepath.Join(checkout, "pending.txt"))
	require.NoError(t, err)
	require.Equal(t, "pending", string(data))
	require.Equal(t, committed.Git.Head.Commit, git("rev-parse", "HEAD"))
	// The frozen source remains the same after saving.
	sha, err := frozen.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.Equal(t, committed.Git.Head.Commit, sha)
}

func (WorkspaceSuite) TestWorkspaceExportParksCommits(ctx context.Context, t *testctx.T) {
	for _, scenario := range []string{"diverged", "dirty", "behind"} {
		t.Run(scenario, func(ctx context.Context, t *testctx.T) {
			checkout, git := workspaceExportCheckout(ctx, t)
			c := connect(ctx, t, dagger.WithWorkdir(checkout))
			target := c.CurrentWorkspace()
			committed, err := commitWorkspace(ctx, c, target.WithNewFile("base.txt", "engine edit").WithNewFile("pending.txt", "pending"), "engine commit", []string{"base.txt"})
			require.NoError(t, err)
			frozen := dagger.Ref[*dagger.Workspace](c, committed.ID)
			if scenario == "behind" {
				require.NoError(t, exportWorkspace(ctx, c, frozen, target))
				require.NoError(t, os.Remove(filepath.Join(checkout, "pending.txt")))
			}
			require.NoError(t, os.WriteFile(filepath.Join(checkout, "base.txt"), []byte("user edit"), 0o644))
			if scenario != "dirty" {
				git("commit", "-am", "user commit")
			}
			head, status := git("rev-parse", "HEAD"), git("status", "--porcelain")
			err = exportWorkspace(ctx, c, frozen, target)
			require.Error(t, err)
			parked := "refs/dagger/checkpoints/" + committed.Git.Head.Commit[:12]
			require.ErrorContains(t, err, parked)
			require.Equal(t, committed.Git.Head.Commit, git("rev-parse", parked))
			require.Equal(t, head, git("rev-parse", "HEAD"))
			require.Equal(t, status, git("status", "--porcelain"))
			data, err := os.ReadFile(filepath.Join(checkout, "base.txt"))
			require.NoError(t, err)
			require.Equal(t, "user edit", string(data))
			_, err = os.Stat(filepath.Join(checkout, "pending.txt"))
			require.ErrorIs(t, err, os.ErrNotExist, "overlay must not be written after a refused fast-forward")
		})
	}
}

func (WorkspaceSuite) TestWorkspaceExportExplicitTargetAndCwd(ctx context.Context, t *testctx.T) {
	checkout, git := workspaceExportCheckout(ctx, t)
	require.NoError(t, os.MkdirAll(filepath.Join(checkout, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(checkout, "sub", "initial.txt"), []byte("initial"), 0o644))
	git("add", ".")
	git("commit", "-m", "subdirectory")
	linked := filepath.Join(t.TempDir(), "linked")
	git("worktree", "add", "-b", "linked", linked)
	c := connect(ctx, t, dagger.WithWorkdir(filepath.Join(checkout, "sub")))
	committed, err := commitWorkspace(ctx, c, c.CurrentWorkspace().WithNewFile("a.txt", "a").WithNewFile("b.txt", "b"), "nested", []string{"a.txt"})
	require.NoError(t, err)
	frozen := dagger.Ref[*dagger.Workspace](c, committed.ID)
	// Destination is another checkout; cwd must not shift repo-root paths.
	destination := connect(ctx, t, dagger.WithWorkdir(linked))
	targetID, err := destination.CurrentWorkspace().ID(ctx)
	require.NoError(t, err)
	require.NoError(t, exportWorkspace(ctx, c, frozen, dagger.Ref[*dagger.Workspace](c, targetID)))
	for _, name := range []string{"a.txt", "b.txt"} {
		data, err := os.ReadFile(filepath.Join(linked, "sub", name))
		require.NoError(t, err)
		require.Equal(t, strings.TrimSuffix(name, ".txt"), string(data))
	}
	require.NotEqual(t, committed.Git.Head.Commit, git("rev-parse", "HEAD"))
	require.Equal(t, committed.Git.Head.Commit, git("rev-parse", "refs/heads/linked"))
	err = frozen.Export(ctx)
	require.ErrorContains(t, err, "pass a local Git workspace with to")
	err = exportWorkspace(ctx, c, frozen, c.Directory().AsWorkspace())
	require.ErrorContains(t, err, "cannot export a synthetic workspace")
}

func (WorkspaceSuite) TestWorkspaceExportCheckpointWithoutCommits(ctx context.Context, t *testctx.T) {
	checkout, git := workspaceExportCheckout(ctx, t)
	c := connect(ctx, t, dagger.WithWorkdir(checkout))
	target := c.CurrentWorkspace()
	frozen := target.Checkpoint().WithNewFile("base.txt", "pending")
	_, err := frozen.ID(ctx)
	require.NoError(t, err)
	head := git("rev-parse", "HEAD")
	require.NoError(t, exportWorkspace(ctx, c, frozen, target))
	require.Equal(t, head, git("rev-parse", "HEAD"))
	require.Equal(t, "M base.txt", git("status", "--porcelain"))
}

func (WorkspaceSuite) TestWorkspaceExportUnrelatedAndUnborn(ctx context.Context, t *testctx.T) {
	for _, unborn := range []bool{false, true} {
		name := "unrelated"
		if unborn {
			name = "unborn"
		}
		t.Run(name, func(ctx context.Context, t *testctx.T) {
			sourcePath, sourceGit := workspaceExportCheckout(ctx, t)
			// Ensure the independent root commit cannot match the destination's.
			require.NoError(t, os.WriteFile(filepath.Join(sourcePath, "base.txt"), []byte("different root"), 0o644))
			sourceGit("checkout", "--orphan", "independent")
			sourceGit("add", ".")
			sourceGit("commit", "-m", "independent history")
			c := connect(ctx, t, dagger.WithWorkdir(sourcePath))
			frozen := c.CurrentWorkspace().Checkpoint().WithNewFile("pending.txt", "must not export")
			sha, err := frozen.Git().Head().CommitSHA(ctx)
			require.NoError(t, err)
			targetPath := t.TempDir()
			if unborn {
				initGitRepo(ctx, t, targetPath)
			} else {
				targetPath, _ = workspaceExportCheckout(ctx, t)
			}
			destination := connect(ctx, t, dagger.WithWorkdir(targetPath))
			targetID, err := destination.CurrentWorkspace().ID(ctx)
			require.NoError(t, err)
			err = exportWorkspace(ctx, c, frozen, dagger.Ref[*dagger.Workspace](c, targetID))
			require.ErrorContains(t, err, "refs/dagger/checkpoints/"+sha[:12])
			if unborn {
				require.ErrorContains(t, err, "HEAD is unborn")
			}
			cmd := exec.CommandContext(ctx, "git", "-C", targetPath, "rev-parse", "refs/dagger/checkpoints/"+sha[:12])
			out, err := cmd.CombinedOutput()
			require.NoError(t, err, "%s", out)
			require.Equal(t, sha, strings.TrimSpace(string(out)))
			_, err = os.Stat(filepath.Join(targetPath, "pending.txt"))
			require.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}
