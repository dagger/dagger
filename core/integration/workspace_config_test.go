package core

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

const currentWorkspaceConfigQuery = `{
  currentWorkspace {
    path
    initialized
    hasConfig
    configPath
  }
}
`

func initGitRepo(ctx context.Context, t *testctx.T, workdir string) {
	t.Helper()

	initCmd := exec.CommandContext(ctx, "git", "init", workdir)
	output, err := initCmd.CombinedOutput()
	require.NoError(t, err, string(output))
}

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

func (WorkspaceSuite) TestConfigAlias(ctx context.Context, t *testctx.T) {
	workdir := newWorkspaceConfigWorkdir(ctx, t, `[modules.greeter]
source = "modules/greeter"
entrypoint = true
`)

	out, err := hostDaggerExec(ctx, t, workdir, "--silent", "config", "modules.greeter.source")
	require.NoError(t, err)
	require.Equal(t, "modules/greeter", strings.TrimSpace(string(out)))

	_, err = hostDaggerExec(ctx, t, workdir, "--silent", "config", "modules.greeter.entrypoint", "false")
	require.NoError(t, err)

	out, err = hostDaggerExec(ctx, t, workdir, "--silent", "workspace", "config", "modules.greeter.entrypoint")
	require.NoError(t, err)
	require.Equal(t, "false", strings.TrimSpace(string(out)))
}

func (WorkspaceSuite) TestWorkspaceConfigRequiresInit(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	initGitRepo(ctx, t, workdir)

	_, err := hostDaggerExec(ctx, t, workdir, "--silent", "workspace", "config")
	require.Error(t, err)
	requireErrOut(t, err, "no config.toml found in workspace")
}

func (WorkspaceSuite) TestCurrentWorkspaceConfigBoundary(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	appDir := filepath.Join(workdir, "app")
	nestedDir := filepath.Join(appDir, "sub")

	require.NoError(t, os.MkdirAll(filepath.Join(appDir, workspace.LockDirName), 0o755))
	require.NoError(t, os.MkdirAll(nestedDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(appDir, workspace.LockDirName, workspace.ConfigFileName),
		[]byte("# Dagger workspace configuration\n"),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(nestedDir, "query.graphql"),
		[]byte(currentWorkspaceConfigQuery),
		0o644,
	))

	initGitRepo(ctx, t, workdir)

	out, err := hostDaggerExec(ctx, t, nestedDir, "--silent", "query", "--doc", "query.graphql")
	require.NoError(t, err)
	require.JSONEq(t, fmt.Sprintf(`{
		"currentWorkspace": {
			"path": "app",
			"initialized": true,
			"hasConfig": true,
			"configPath": %q
		}
	}`, filepath.Join("app", workspace.LockDirName, workspace.ConfigFileName)), string(out))
}

func (WorkspaceSuite) TestWorkspaceInitCommand(ctx context.Context, t *testctx.T) {
	t.Run("creates config for the current workspace directory", func(ctx context.Context, t *testctx.T) {
		workdir := t.TempDir()
		nestedDir := filepath.Join(workdir, "app", "sub")

		require.NoError(t, os.MkdirAll(nestedDir, 0o755))
		require.NoError(t, os.WriteFile(
			filepath.Join(nestedDir, "query.graphql"),
			[]byte(currentWorkspaceConfigQuery),
			0o644,
		))
		initGitRepo(ctx, t, workdir)

		out, err := hostDaggerExecRaw(ctx, t, nestedDir, "--silent", "init")
		require.NoError(t, err)
		require.Equal(t, fmt.Sprintf("Initialized workspace in %s", filepath.Join(nestedDir, workspace.LockDirName)), strings.TrimSpace(string(out)))

		configHostPath := filepath.Join(nestedDir, workspace.LockDirName, workspace.ConfigFileName)
		configContents, err := os.ReadFile(configHostPath)
		require.NoError(t, err)
		require.Contains(t, string(configContents), "[modules]")

		out, err = hostDaggerExec(ctx, t, nestedDir, "--silent", "query", "--doc", "query.graphql")
		require.NoError(t, err)
		require.JSONEq(t, fmt.Sprintf(`{
			"currentWorkspace": {
				"path": "app/sub",
				"initialized": true,
				"hasConfig": true,
				"configPath": %q
			}
		}`, filepath.Join("app", "sub", workspace.LockDirName, workspace.ConfigFileName)), string(out))
	})

	t.Run("rejects reinitialization", func(ctx context.Context, t *testctx.T) {
		workdir := t.TempDir()
		nestedDir := filepath.Join(workdir, "app")

		require.NoError(t, os.MkdirAll(nestedDir, 0o755))
		initGitRepo(ctx, t, workdir)

		_, err := hostDaggerExec(ctx, t, nestedDir, "--silent", "workspace", "init")
		require.NoError(t, err)

		_, err = hostDaggerExec(ctx, t, nestedDir, "--silent", "workspace", "init")
		require.Error(t, err)
		requireErrOut(t, err, fmt.Sprintf("workspace already initialized at %s", filepath.Join(nestedDir, workspace.LockDirName)))
	})
}

// TestWorkspaceConfigurationLifecycle is the planning scaffold for the full
// scope this file should eventually own: initializing, editing, and detecting
// workspaces from .dagger/config.toml. Module management, compat, and
// migration belong in their own files.
func (WorkspaceSuite) TestWorkspaceConfigurationLifecycle(ctx context.Context, t *testctx.T) {
	t.Run("CurrentWorkspace.Init creates config for the repo", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement CurrentWorkspace.Init API coverage.

Exercise the API form of workspace initialization and verify it creates the
expected .dagger/config.toml rooted at the current repo.`)
	})

	t.Run("workspace config detects the nearest initialized boundary", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement workspace config boundary detection coverage.

Invoke workspace config commands from nested directories and verify they use
the nearest .dagger/config.toml boundary rather than the current directory.`)
	})
}
