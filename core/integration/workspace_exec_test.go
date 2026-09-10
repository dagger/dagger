package core

import (
	"context"
	"fmt"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (WorkspaceSuite) TestExecCLI(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	source := c.Directory().
		WithNewFile("dagger.toml", `[modules.tool]
source = ".dagger/modules/tool"
`).
		WithNewFile(".dagger/modules/tool/dagger.json", `{"name":"tool","sdk":{"source":"dang"}}`).
		WithNewFile(".dagger/modules/tool/main.dang", fmt.Sprintf(`
type Tool {
  pub base: Container! {
    container
      .from(%q)
      .withEnvVariable("WS_EXEC_BASE", "module")
  }
}
`, distconsts.AlpineImage)).
		WithNewFile("root.txt", "root\n").
		WithNewFile("excluded.txt", "keep excluded\n").
		WithNewFile("remove.txt", "remove me\n").
		WithNewFile("assert-args", "#!/bin/sh\nprintf '%s\\n' \"$@\" > /ws/sub/args.txt\n", dagger.DirectoryWithNewFileOpts{Permissions: 0o755}).
		WithNewFile("sub/original.txt", "before\n")
	base := c.Container().From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithDirectory("/selected", source).
		WithEnvVariable("GIT_AUTHOR_NAME", "Workspace Exec").
		WithEnvVariable("GIT_AUTHOR_EMAIL", "workspace-exec@example.com").
		WithEnvVariable("GIT_COMMITTER_NAME", "Workspace Exec").
		WithEnvVariable("GIT_COMMITTER_EMAIL", "workspace-exec@example.com").
		WithWorkdir("/selected").
		WithExec([]string{"git", "init", "-b", "main"}).
		WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "initial"}).
		WithWorkdir("/caller")
	workspace := "/selected/sub"

	t.Run("fixed mount and cwd", func(ctx context.Context, t *testctx.T) {
		result := base.With(workspaceSelectionDaggerExec(
			"-W", workspace, "ws", "exec", "--auto-apply", "sh", "-c",
			`test "$PWD" = /ws/sub && test -f /ws/root.txt && printf '%s\n' "$PWD"`,
		))
		_, err := result.Sync(ctx)
		require.NoError(t, err)
	})

	t.Run("command output uses progress telemetry", func(ctx context.Context, t *testctx.T) {
		var logs safeBuffer
		outputClient := connect(ctx, t, dagger.WithLogOutput(&logs))
		outputBase := outputClient.Container().From(alpineImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, outputClient)).
			WithDirectory("/output", outputClient.Directory()).
			WithWorkdir("/caller")
		result := outputBase.With(workspaceSelectionDaggerExec(
			"--progress=report", "-W", "/output", "ws", "exec",
			"sh", "-c", `printf 'stdout-marker\n'; printf 'stderr-marker\n' >&2`,
		))
		_, err := result.Sync(ctx)
		require.NoError(t, err)
		require.Contains(t, logs.String(), "stdout-marker")
		require.Contains(t, logs.String(), "stderr-marker")
	})

	t.Run("optional separator and command flags", func(ctx context.Context, t *testctx.T) {
		for _, args := range [][]string{
			{"--auto-apply", "/ws/assert-args", "-w", "--from=busybox"},
			{"--auto-apply", "--", "/ws/assert-args", "-w", "--from=busybox"},
		} {
			cliArgs := append([]string{"-W", workspace, "workspace", "exec"}, args...)
			result := base.With(workspaceSelectionDaggerExec(cliArgs...))
			out, err := result.File("/selected/sub/args.txt").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, "-w\n--from=busybox\n", out)
		}
	})

	t.Run("image base and workspace changes", func(ctx context.Context, t *testctx.T) {
		result := base.With(workspaceSelectionDaggerExec(
			"-W", workspace, "ws", "exec", "--auto-apply", "--from="+distconsts.AlpineImage,
			"sh", "-c", `printf 'after\n' > original.txt; printf 'new\n' > new.txt; rm /ws/remove.txt`,
		))
		contents, err := result.File("/selected/sub/original.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "after\n", contents)
		contents, err = result.File("/selected/sub/new.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "new\n", contents)
		_, err = result.File("/selected/remove.txt").Sync(ctx)
		require.Error(t, err)
	})

	t.Run("include and exclude filter the source only", func(ctx context.Context, t *testctx.T) {
		result := base.With(workspaceSelectionDaggerExec(
			"-W", workspace, "ws", "exec", "--auto-apply",
			"--include=sub/**", "--exclude=excluded.txt",
			"sh", "-c", `test -f /ws/sub/original.txt; test ! -e /ws/root.txt; test ! -e /ws/excluded.txt; printf 'created\n' > /ws/excluded.txt`,
		))
		// A filtered source file is absent from both snapshots, so it is not
		// reported as removed. A file created by the command is still changed.
		contents, err := result.File("/selected/root.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "root\n", contents)
		contents, err = result.File("/selected/excluded.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "created\n", contents)
	})

	t.Run("no apply previews changes", func(ctx context.Context, t *testctx.T) {
		result := base.With(workspaceSelectionDaggerExec(
			"-W", workspace, "ws", "exec", "--no-apply", "sh", "-c", `printf 'preview\n' > preview.txt`,
		))
		_, err := result.File("/selected/sub/preview.txt").Sync(ctx)
		require.Error(t, err)
		stderr, err := result.Stderr(ctx)
		require.NoError(t, err)
		require.Contains(t, stderr, "preview.txt")
		require.Contains(t, stderr, "were not applied (--no-apply)")
	})

	t.Run("failed command keeps status and never applies", func(ctx context.Context, t *testctx.T) {
		result := base.WithExec([]string{
			"dagger", "-W", workspace, "ws", "exec", "--auto-apply",
			"sh", "-c", `printf 'partial\n' > partial.txt; exit 23`,
		}, dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
			Expect:                        dagger.ReturnTypeFailure,
		})
		status, err := result.ExitCode(ctx)
		require.NoError(t, err)
		require.Equal(t, 23, status)
		_, err = result.File("/selected/sub/partial.txt").Sync(ctx)
		require.Error(t, err)
		stderr, err := result.Stderr(ctx)
		require.NoError(t, err)
		require.Contains(t, stderr, "partial.txt")
	})

	t.Run("module function base", func(ctx context.Context, t *testctx.T) {
		result := base.With(workspaceSelectionDaggerExec(
			"-W", workspace, "ws", "exec", "--auto-apply", "--from=tool:base",
			"sh", "-c", `printf '%s\n' "$WS_EXEC_BASE" > module-base.txt`,
		))
		out, err := result.File("/selected/sub/module-base.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "module\n", out)
	})

	t.Run("rootless local checks export only when applying changes", func(ctx context.Context, t *testctx.T) {
		rootless := c.Container().From(alpineImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithDirectory("/rootless", c.Directory()).
			WithWorkdir("/caller")
		_, err := rootless.With(workspaceSelectionDaggerExec(
			"-W", "/rootless", "ws", "exec", "true",
		)).Sync(ctx)
		require.NoError(t, err)

		preview := rootless.With(workspaceSelectionDaggerExec(
			"-W", "/rootless", "ws", "exec", "--no-apply", "sh", "-c", `printf 'preview\n' > preview.txt`,
		))
		stderr, err := preview.Stderr(ctx)
		require.NoError(t, err)
		require.Contains(t, stderr, "preview.txt")
		_, err = preview.File("/rootless/preview.txt").Sync(ctx)
		require.Error(t, err)
	})

	t.Run("conflicting apply flags", func(ctx context.Context, t *testctx.T) {
		result := base.WithExec([]string{
			"dagger", "-W", workspace, "ws", "exec", "--auto-apply", "--no-apply", "true",
		}, dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
			Expect:                        dagger.ReturnTypeFailure,
		})
		stderr, err := result.Stderr(ctx)
		require.NoError(t, err)
		require.Contains(t, stderr, "--auto-apply and --no-apply cannot be used together")
	})

	t.Run("remote export check is late", func(ctx context.Context, t *testctx.T) {
		ref := workspaceSelectionRemoteRef(ctx, t, c, source)
		remote := strings.Replace(ref, "/repo.git@", "/repo.git/sub@", 1)

		_, err := base.With(workspaceSelectionDaggerExec(
			"-W", remote, "ws", "exec", "test", "-f", "/ws/root.txt",
		)).Sync(ctx)
		require.NoError(t, err)

		preview := base.With(workspaceSelectionDaggerExec(
			"-W", remote, "ws", "exec", "--no-apply", "sh", "-c", `printf 'remote\n' > remote.txt`,
		))
		stderr, err := preview.Stderr(ctx)
		require.NoError(t, err)
		require.Contains(t, stderr, "remote.txt")

		apply := base.WithExec([]string{
			"dagger", "-W", remote, "ws", "exec", "--auto-apply", "sh", "-c", `printf 'remote\n' > remote.txt`,
		}, dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
			Expect:                        dagger.ReturnTypeFailure,
		})
		stderr, err = apply.Stderr(ctx)
		require.NoError(t, err)
		require.Contains(t, strings.ToLower(stderr), "remote")
	})
}
