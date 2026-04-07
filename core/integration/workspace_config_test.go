package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func writeWorkspaceConfigFile(t *testctx.T, workdir, configTOML string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Join(workdir, workspace.LockDirName), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(workdir, workspace.LockDirName, workspace.ConfigFileName),
		[]byte(configTOML),
		0o644,
	))
}

func newWorkspaceConfigWorkdir(ctx context.Context, t *testctx.T, configTOML string) string {
	t.Helper()

	workdir := t.TempDir()
	initGitRepo(ctx, t, workdir)
	writeWorkspaceConfigFile(t, workdir, configTOML)
	return workdir
}

func (WorkspaceSuite) TestWorkspaceConfigRead(ctx context.Context, t *testctx.T) {
	workdir := newWorkspaceConfigWorkdir(ctx, t, `[modules.greeter]
source = "modules/greeter"
entrypoint = true

[modules.greeter.config]
greeting = "hello"

[modules.wolfi]
source = "github.com/dagger/dagger/modules/wolfi"
`)

	t.Run("full config", func(ctx context.Context, t *testctx.T) {
		out, err := hostDaggerExec(ctx, t, workdir, "--silent", "workspace", "config")
		require.NoError(t, err)
		require.Contains(t, string(out), `source = "modules/greeter"`)
		require.Contains(t, string(out), "entrypoint = true")
		require.Contains(t, string(out), `source = "github.com/dagger/dagger/modules/wolfi"`)
	})

	t.Run("scalar value", func(ctx context.Context, t *testctx.T) {
		out, err := hostDaggerExec(ctx, t, workdir, "--silent", "workspace", "config", "modules.greeter.source")
		require.NoError(t, err)
		require.Equal(t, "modules/greeter", strings.TrimSpace(string(out)))
	})

	t.Run("table value", func(ctx context.Context, t *testctx.T) {
		out, err := hostDaggerExec(ctx, t, workdir, "--silent", "workspace", "config", "modules.greeter")
		require.NoError(t, err)
		require.Contains(t, string(out), `source = "modules/greeter"`)
		require.Contains(t, string(out), "entrypoint = true")
	})

	t.Run("missing key", func(ctx context.Context, t *testctx.T) {
		_, err := hostDaggerExec(ctx, t, workdir, "--silent", "workspace", "config", "modules.missing.source")
		require.Error(t, err)
		requireErrOut(t, err, `key "modules.missing.source" is not set`)
	})
}

func (WorkspaceSuite) TestWorkspaceConfigWrite(ctx context.Context, t *testctx.T) {
	t.Run("writes string and bool values", func(ctx context.Context, t *testctx.T) {
		workdir := newWorkspaceConfigWorkdir(ctx, t, `[modules.greeter]
source = "modules/greeter"
entrypoint = true
`)

		_, err := hostDaggerExec(ctx, t, workdir, "--silent", "workspace", "config", "modules.greeter.source", "github.com/acme/greeter")
		require.NoError(t, err)
		_, err = hostDaggerExec(ctx, t, workdir, "--silent", "workspace", "config", "modules.greeter.entrypoint", "false")
		require.NoError(t, err)

		out, err := hostDaggerExec(ctx, t, workdir, "--silent", "workspace", "config", "modules.greeter.source")
		require.NoError(t, err)
		require.Equal(t, "github.com/acme/greeter", strings.TrimSpace(string(out)))

		out, err = hostDaggerExec(ctx, t, workdir, "--silent", "workspace", "config", "modules.greeter.entrypoint")
		require.NoError(t, err)
		require.Equal(t, "false", strings.TrimSpace(string(out)))
	})

	t.Run("writes array values", func(ctx context.Context, t *testctx.T) {
		workdir := newWorkspaceConfigWorkdir(ctx, t, `[modules.greeter]
source = "modules/greeter"
entrypoint = true
`)

		_, err := hostDaggerExec(ctx, t, workdir, "--silent", "workspace", "config", "modules.greeter.config.tags", "main, develop")
		require.NoError(t, err)

		configContents, err := os.ReadFile(filepath.Join(workdir, workspace.LockDirName, workspace.ConfigFileName))
		require.NoError(t, err)
		require.Contains(t, string(configContents), "tags")
		require.Contains(t, string(configContents), "main")
		require.Contains(t, string(configContents), "develop")
	})

	t.Run("rejects invalid keys", func(ctx context.Context, t *testctx.T) {
		workdir := newWorkspaceConfigWorkdir(ctx, t, `[modules.greeter]
source = "modules/greeter"
entrypoint = true
`)

		_, err := hostDaggerExec(ctx, t, workdir, "--silent", "workspace", "config", "modules.greeter.badfield", "value")
		require.Error(t, err)
		requireErrOut(t, err, "unknown config key")
	})
}

func (WorkspaceSuite) TestWorkspaceConfigRequiresInit(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	initGitRepo(ctx, t, workdir)

	_, err := hostDaggerExec(ctx, t, workdir, "--silent", "workspace", "config")
	require.Error(t, err)
	requireErrOut(t, err, "no config.toml found in workspace")
}
