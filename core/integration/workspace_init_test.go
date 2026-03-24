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

func (WorkspaceSuite) TestCurrentWorkspaceInit(ctx context.Context, t *testctx.T) {
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

		out, err := hostDaggerExec(ctx, t, nestedDir, "--silent", "workspace", "init")
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
