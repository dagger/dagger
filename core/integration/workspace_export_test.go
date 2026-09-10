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
	id, err := target.WithCommitsFrom(source).ID(ctx)
	if err != nil {
		return err
	}
	integrated := dagger.Ref[*dagger.Workspace](c, id)
	merged := integrated.Directory("/").Changes(source.Git().Head().Tree(dagger.GitRefTreeOpts{DiscardGitDir: true})).WithChangeset(source.Git().Uncommitted())
	return integrated.WithChanges(merged).Export(ctx)
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

func (WorkspaceSuite) TestWorkspaceExportIndependentAgents(ctx context.Context, t *testctx.T) {
	checkout, git := workspaceExportCheckout(ctx, t)
	c := connect(ctx, t, dagger.WithWorkdir(checkout))
	baseID, err := syncWorkspace(ctx, t, c.CurrentWorkspace()).ID(ctx)
	require.NoError(t, err)
	base := dagger.Ref[*dagger.Workspace](c, baseID)
	a := base.WithNewFile("agent-a.txt", "a").WithCommit("agent A", workspaceCommitDate)
	b := base.WithNewFile("agent-b.txt", "b").WithCommit("agent B", workspaceCommitDate)
	aID, err := a.ID(ctx)
	require.NoError(t, err)
	bID, err := b.ID(ctx)
	require.NoError(t, err)
	a, b = dagger.Ref[*dagger.Workspace](c, aID), dagger.Ref[*dagger.Workspace](c, bID)
	// Prepare both against the same checkout. A stale prepared save must fail;
	// recomposing against currentWorkspace integrates the other agent's work.
	staleID, err := c.CurrentWorkspace().WithCommitsFrom(b).ID(ctx)
	require.NoError(t, err)
	require.NoError(t, c.CurrentWorkspace().WithCommitsFrom(a).Export(ctx))
	first := git("rev-parse", "HEAD")
	require.Error(t, dagger.Ref[*dagger.Workspace](c, staleID).Export(ctx))
	require.Equal(t, first, git("rev-parse", "HEAD"))
	require.NoError(t, c.CurrentWorkspace().WithCommitsFrom(b).Export(ctx))
	second := git("rev-parse", "HEAD")
	require.Contains(t, git("log", "--format=%H"), first)
	sourceSHA, err := b.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	require.NotEqual(t, sourceSHA, second)
	require.Contains(t, git("log", "-1", "--format=%B"), sourceSHA)
	require.NoError(t, c.CurrentWorkspace().WithCommitsFrom(b).Export(ctx))
	require.Equal(t, second, git("rev-parse", "HEAD"))
	require.Empty(t, git("status", "--porcelain"))
	for _, name := range []string{"agent-a.txt", "agent-b.txt"} {
		_, err := os.Stat(filepath.Join(checkout, name))
		require.NoError(t, err)
	}
}

func (WorkspaceSuite) TestWorkspaceExportCapturedDirt(ctx context.Context, t *testctx.T) {
	checkout, git := workspaceExportCheckout(ctx, t)
	c := connect(ctx, t, dagger.WithWorkdir(checkout))
	require.NoError(t, os.WriteFile(filepath.Join(checkout, "base.txt"), []byte("captured dirt"), 0o644))
	agentID, err := syncWorkspace(ctx, t, c.CurrentWorkspace()).WithCommit("commit captured cleanup", workspaceCommitDate).ID(ctx)
	require.NoError(t, err)
	agent := dagger.Ref[*dagger.Workspace](c, agentID)
	require.NoError(t, c.CurrentWorkspace().WithCommitsFrom(agent).Export(ctx))
	require.Empty(t, git("status", "--porcelain"))
	require.Equal(t, "captured dirt", git("show", "HEAD:base.txt"))
	// Pending files are deliberately not part of a commit-only save.
	agent = agent.WithNewFile("pending.txt", "not committed")
	require.NoError(t, c.CurrentWorkspace().WithCommitsFrom(agent).Export(ctx))
	_, err = os.Stat(filepath.Join(checkout, "pending.txt"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func (WorkspaceSuite) TestWorkspaceExportIntegrationConflicts(ctx context.Context, t *testctx.T) {
	checkout, git := workspaceExportCheckout(ctx, t)
	c := connect(ctx, t, dagger.WithWorkdir(checkout))
	agentID, err := syncWorkspace(ctx, t, c.CurrentWorkspace()).WithNewFile("base.txt", "agent").WithCommit("agent", workspaceCommitDate).ID(ctx)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(checkout, "base.txt"), []byte("user"), 0o644))
	git("commit", "-am", "user")
	head := git("rev-parse", "HEAD")
	require.Error(t, c.CurrentWorkspace().WithCommitsFrom(dagger.Ref[*dagger.Workspace](c, agentID)).Export(ctx))
	require.Equal(t, head, git("rev-parse", "HEAD"))
	require.Empty(t, git("status", "--porcelain"))
}

func (WorkspaceSuite) TestWorkspaceExportPendingGitEdges(ctx context.Context, t *testctx.T) {
	checkout, git := workspaceExportCheckout(ctx, t)
	require.NoError(t, os.WriteFile(filepath.Join(checkout, ".gitignore"), []byte("ignored.txt\n"), 0o644))
	git("add", ".gitignore")
	git("commit", "-m", "ignore file")
	c := connect(ctx, t, dagger.WithWorkdir(checkout))
	baseID, err := syncWorkspace(ctx, t, c.CurrentWorkspace()).ID(ctx)
	require.NoError(t, err)
	base := dagger.Ref[*dagger.Workspace](c, baseID)
	head := git("rev-parse", "HEAD")
	require.NoError(t, c.CurrentWorkspace().WithCommitsFrom(base).WithNewFile("ignored.txt", "intentional edit").Export(ctx))
	contents, err := os.ReadFile(filepath.Join(checkout, "ignored.txt"))
	require.NoError(t, err)
	require.Equal(t, "intentional edit", string(contents))
	require.Equal(t, head, git("rev-parse", "HEAD"))
	require.Empty(t, git("status", "--porcelain"))
	empty := c.Directory().WithNewDirectory("empty").Changes(c.Directory())
	err = c.CurrentWorkspace().WithCommitsFrom(base).WithChanges(empty).Export(ctx)
	require.ErrorContains(t, err, "cannot export empty directories")
	_, err = os.Stat(filepath.Join(checkout, "empty"))
	require.ErrorIs(t, err, os.ErrNotExist)
	require.Equal(t, head, git("rev-parse", "HEAD"))
}

func (WorkspaceSuite) TestWorkspaceExportCommitsAndOverlay(ctx context.Context, t *testctx.T) {
	checkout, git := workspaceExportCheckout(ctx, t)
	c := connect(ctx, t, dagger.WithWorkdir(checkout))
	target := c.CurrentWorkspace()
	base, err := target.Git().Head().CommitSHA(ctx)
	require.NoError(t, err)
	// Prime host reads before export; verify the actual disk writes below.
	_, err = target.File("base.txt").Contents(ctx)
	require.NoError(t, err)
	ws := syncWorkspace(ctx, t, target).WithNewFile("base.txt", "committed").
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
	// Repeated exports are effectful and do not produce duplicate commits.
	require.NoError(t, os.WriteFile(filepath.Join(checkout, "pending.txt"), []byte("changed outside"), 0o644))
	require.Error(t, exportWorkspace(ctx, c, frozen, target))
	data, err := os.ReadFile(filepath.Join(checkout, "pending.txt"))
	require.NoError(t, err)
	require.Equal(t, "changed outside", string(data))
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
			preparedID, err := target.WithCommitsFrom(frozen).WithChanges(frozen.Git().Uncommitted()).ID(ctx)
			require.NoError(t, err)
			prepared := dagger.Ref[*dagger.Workspace](c, preparedID)
			if scenario == "behind" {
				require.NoError(t, exportWorkspace(ctx, c, frozen, target))
				require.NoError(t, os.Remove(filepath.Join(checkout, "pending.txt")))
			}
			require.NoError(t, os.WriteFile(filepath.Join(checkout, "base.txt"), []byte("user edit"), 0o644))
			if scenario != "dirty" {
				git("commit", "-am", "user commit")
			}
			head, status := git("rev-parse", "HEAD"), git("status", "--porcelain")
			err = prepared.Export(ctx)
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
	require.NoError(t, exportWorkspace(ctx, destination, dagger.Ref[*dagger.Workspace](destination, committed.ID), destination.CurrentWorkspace()))
	for _, name := range []string{"a.txt", "b.txt"} {
		data, err := os.ReadFile(filepath.Join(linked, "sub", name))
		require.NoError(t, err)
		require.Equal(t, strings.TrimSuffix(name, ".txt"), string(data))
	}
	require.NotEqual(t, committed.Git.Head.Commit, git("rev-parse", "HEAD"))
	require.Equal(t, committed.Git.Head.Commit, git("rev-parse", "refs/heads/linked"))
	err = frozen.Export(ctx)
	require.ErrorContains(t, err, "withCommitsFrom first")
	err = exportWorkspace(ctx, c, frozen, c.Directory().AsWorkspace())
	require.Error(t, err)
}

func (WorkspaceSuite) TestWorkspaceExportCheckpointWithoutCommits(ctx context.Context, t *testctx.T) {
	checkout, git := workspaceExportCheckout(ctx, t)
	c := connect(ctx, t, dagger.WithWorkdir(checkout))
	target := c.CurrentWorkspace()
	frozen := syncWorkspace(ctx, t, target).WithNewFile("base.txt", "pending")
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
			frozen := syncWorkspace(ctx, t, c.CurrentWorkspace()).WithNewFile("pending.txt", "must not export")
			sourceID, err := frozen.ID(ctx)
			require.NoError(t, err)
			targetPath := t.TempDir()
			if unborn {
				initGitRepo(ctx, t, targetPath)
			} else {
				targetPath, _ = workspaceExportCheckout(ctx, t)
			}
			destination := connect(ctx, t, dagger.WithWorkdir(targetPath))
			err = exportWorkspace(ctx, destination, dagger.Ref[*dagger.Workspace](destination, sourceID), destination.CurrentWorkspace())
			require.Error(t, err)
			_, err = os.Stat(filepath.Join(targetPath, "pending.txt"))
			require.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}

func (WorkspaceSuite) TestExportCLI(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	source := c.Directory().
		WithNewFile("dagger.toml", "[modules.broken]\nsource = \"does-not-exist\"\n").
		WithNewFile("root.txt", "from root\n").
		WithNewFile("items/.hidden", "hidden\n").
		WithNewFile("items/a.txt", "a\n").
		WithNewFile("items/skip.txt", "skip\n").
		WithNewFile("items/file with spaces.txt", "spaces\n").
		WithNewFile("items/bin/tool", "#!/bin/sh\n", dagger.DirectoryWithNewFileOpts{Permissions: 0o755}).
		WithNewFile("items/sub/deep.txt", "deep\n")
	base := c.Container().From(alpineImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithDirectory("/selected", source.WithNewDirectory(".git")).
		WithNewFile("/caller/merge/unrelated.txt", "unrelated\n").
		WithNewFile("/caller/existing/marker.txt", "marker\n").
		WithWorkdir("/caller")
	workspace := "/selected/items"

	t.Run("default cwd and directory merge", func(ctx context.Context, t *testctx.T) {
		result := base.With(workspaceSelectionDaggerExec(
			"-W", workspace, "ws", "export", "-o", "/caller/merge",
		))
		contents, err := result.File("/caller/merge/a.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "a\n", contents)
		contents, err = result.File("/caller/merge/.hidden").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "hidden\n", contents)
		contents, err = result.File("/caller/merge/sub/deep.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "deep\n", contents)
		contents, err = result.File("/caller/merge/unrelated.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "unrelated\n", contents)

		mode, err := result.WithExec([]string{"stat", "-c", "%a", "/caller/merge/bin/tool"}).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "755\n", mode)

		stderr, err := result.Stderr(ctx)
		require.NoError(t, err)
		require.Contains(t, stderr, `Saved to "/caller/merge".`)
	})

	t.Run("absolute root file to file", func(ctx context.Context, t *testctx.T) {
		result := base.With(workspaceSelectionDaggerExec(
			"-W", workspace, "workspace", "export", "/root.txt", "-o", "/caller/root-copy.txt",
		))
		contents, err := result.File("/caller/root-copy.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "from root\n", contents)
		stderr, err := result.Stderr(ctx)
		require.NoError(t, err)
		require.Contains(t, stderr, `Saved to "/caller/root-copy.txt".`)
	})

	t.Run("relative destination starts at client cwd", func(ctx context.Context, t *testctx.T) {
		result := base.With(workspaceSelectionDaggerExec(
			"-W", workspace, "ws", "export", "a.txt", "-o", "relative.txt",
		))
		contents, err := result.File("/caller/relative.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "a\n", contents)
	})

	t.Run("file to existing directory", func(ctx context.Context, t *testctx.T) {
		result := base.With(workspaceSelectionDaggerExec(
			"-W", workspace, "ws", "export", "a.txt", "-o", "/caller/existing",
		))
		contents, err := result.File("/caller/existing/a.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "a\n", contents)
		contents, err = result.File("/caller/existing/marker.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "marker\n", contents)
		stderr, err := result.Stderr(ctx)
		require.NoError(t, err)
		require.Contains(t, stderr, `Saved to "/caller/existing".`)
	})

	t.Run("include and exclude", func(ctx context.Context, t *testctx.T) {
		result := base.With(workspaceSelectionDaggerExec(
			"-W", workspace, "ws", "export", ".", "-o", "/caller/filtered",
			"--include=**/*.txt", "--exclude=skip.txt",
		))
		contents, err := result.File("/caller/filtered/a.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "a\n", contents)
		contents, err = result.File("/caller/filtered/sub/deep.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "deep\n", contents)
		_, err = result.File("/caller/filtered/skip.txt").Sync(ctx)
		require.Error(t, err)
		_, err = result.File("/caller/filtered/bin/tool").Sync(ctx)
		require.Error(t, err)
	})

	t.Run("path and destination with spaces", func(ctx context.Context, t *testctx.T) {
		result := base.With(workspaceSelectionDaggerExec(
			"-W", workspace, "ws", "export", "file with spaces.txt", "-o", "/caller/copy with spaces.txt",
		))
		contents, err := result.File("/caller/copy with spaces.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "spaces\n", contents)
	})

	t.Run("file rejects filters", func(ctx context.Context, t *testctx.T) {
		result := base.WithExec([]string{
			"dagger", "-W", workspace, "ws", "export", "a.txt", "-o", "/caller/rejected", "--include=*.txt",
		}, dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
			Expect:                        dagger.ReturnTypeFailure,
		})
		stderr, err := result.Stderr(ctx)
		require.NoError(t, err)
		require.Contains(t, stderr, "--include and --exclude can only be used with a directory")
	})

	t.Run("missing path", func(ctx context.Context, t *testctx.T) {
		result := base.WithExec([]string{
			"dagger", "-W", workspace, "ws", "export", "missing", "-o", "/caller/missing",
		}, dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
			Expect:                        dagger.ReturnTypeFailure,
		})
		stderr, err := result.Stderr(ctx)
		require.NoError(t, err)
		require.Contains(t, stderr, "no such file or directory")
	})

	t.Run("output is required", func(ctx context.Context, t *testctx.T) {
		result := base.WithExec([]string{
			"dagger", "-W", workspace, "ws", "export", "a.txt",
		}, dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
			Expect:                        dagger.ReturnTypeFailure,
		})
		stderr, err := result.Stderr(ctx)
		require.NoError(t, err)
		require.Contains(t, stderr, "--output is required")
	})

	t.Run("remote workspace exports to caller", func(ctx context.Context, t *testctx.T) {
		ref := workspaceSelectionRemoteRef(ctx, t, c, source)
		remote := strings.Replace(ref, "/repo.git@", "/repo.git/items@", 1)
		result := base.With(workspaceSelectionDaggerExec(
			"-W", remote, "ws", "export", "sub", "-o", "/caller/remote",
		))
		contents, err := result.File("/caller/remote/deep.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "deep\n", contents)
	})
}
