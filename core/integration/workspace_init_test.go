package core

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

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

	initCmd := exec.CommandContext(ctx, "git", "init", workdir)
	output, err := initCmd.CombinedOutput()
	require.NoError(t, err, string(output))

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
