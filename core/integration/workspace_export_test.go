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
