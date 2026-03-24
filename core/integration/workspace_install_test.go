package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	workspacecfg "github.com/dagger/dagger/core/workspace"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (WorkspaceSuite) TestCurrentWorkspaceInstall(ctx context.Context, t *testctx.T) {
	t.Run("installs a remote module and initializes the workspace", func(ctx context.Context, t *testctx.T) {
		workdir := t.TempDir()
		initGitRepo(ctx, t, workdir)

		c := connect(ctx, t, dagger.WithWorkdir(workdir))
		ref := "github.com/dagger/dagger/modules/wolfi@v0.20.2"

		msg, err := c.CurrentWorkspace().Install(ctx, ref, dagger.WorkspaceInstallOpts{Name: "mywolfi"})
		require.NoError(t, err)
		require.Equal(t, `Installed module "mywolfi" in `+filepath.Join(workdir, ".dagger", workspacecfg.ConfigFileName), msg)

		configBytes, err := os.ReadFile(filepath.Join(workdir, ".dagger", workspacecfg.ConfigFileName))
		require.NoError(t, err)

		cfg, err := workspacecfg.ParseConfig(configBytes)
		require.NoError(t, err)
		require.Contains(t, cfg.Modules, "mywolfi")
		require.Equal(t, ref, cfg.Modules["mywolfi"].Source)
		require.False(t, cfg.Modules["mywolfi"].Blueprint)

		msg, err = c.CurrentWorkspace().Install(ctx, ref, dagger.WorkspaceInstallOpts{Name: "mywolfi"})
		require.NoError(t, err)
		require.Equal(t, `Module "mywolfi" is already installed`, msg)
	})

	t.Run("rewrites local module refs relative to .dagger", func(ctx context.Context, t *testctx.T) {
		workdir := t.TempDir()
		depDir := filepath.Join(workdir, "dep")

		require.NoError(t, os.MkdirAll(depDir, 0o755))
		initGitRepo(ctx, t, workdir)

		_, err := hostDaggerExec(ctx, t, depDir, "init", "--name=dep", "--sdk=go")
		require.NoError(t, err)

		c := connect(ctx, t, dagger.WithWorkdir(workdir))
		msg, err := c.CurrentWorkspace().Install(ctx, "./dep")
		require.NoError(t, err)
		require.Equal(t, `Installed module "dep" in `+filepath.Join(workdir, ".dagger", workspacecfg.ConfigFileName), msg)

		configBytes, err := os.ReadFile(filepath.Join(workdir, ".dagger", workspacecfg.ConfigFileName))
		require.NoError(t, err)

		cfg, err := workspacecfg.ParseConfig(configBytes)
		require.NoError(t, err)
		require.Contains(t, cfg.Modules, "dep")
		require.Equal(t, "../dep", cfg.Modules["dep"].Source)
	})
}

func readInstalledWorkspaceConfig(t *testctx.T, workdir string) *workspacecfg.Config {
	t.Helper()

	configBytes, err := os.ReadFile(filepath.Join(workdir, workspacecfg.LockDirName, workspacecfg.ConfigFileName))
	require.NoError(t, err)

	cfg, err := workspacecfg.ParseConfig(configBytes)
	require.NoError(t, err)
	return cfg
}

func (WorkspaceSuite) TestWorkspaceInstallCommand(ctx context.Context, t *testctx.T) {
	t.Run("falls back to workspace install when no module is targeted", func(ctx context.Context, t *testctx.T) {
		workdir := t.TempDir()
		depDir := filepath.Join(workdir, "dep")

		require.NoError(t, os.MkdirAll(depDir, 0o755))
		initGitRepo(ctx, t, workdir)

		_, err := hostDaggerExec(ctx, t, depDir, "init", "--name=dep", "--sdk=go")
		require.NoError(t, err)

		out, err := hostDaggerExec(ctx, t, workdir, "--silent", "install", "./dep")
		require.NoError(t, err)
		require.Equal(t, `Installed module "dep" in `+filepath.Join(workdir, workspacecfg.LockDirName, workspacecfg.ConfigFileName), strings.TrimSpace(string(out)))

		cfg := readInstalledWorkspaceConfig(t, workdir)
		require.Contains(t, cfg.Modules, "dep")
		require.Equal(t, "../dep", cfg.Modules["dep"].Source)
	})

	t.Run("keeps module dependency installs for the current module", func(ctx context.Context, t *testctx.T) {
		workdir := t.TempDir()
		depDir := filepath.Join(workdir, "dep")

		require.NoError(t, os.MkdirAll(depDir, 0o755))
		initGitRepo(ctx, t, workdir)

		_, err := hostDaggerExec(ctx, t, depDir, "init", "--name=dep", "--sdk=go")
		require.NoError(t, err)
		_, err = hostDaggerExec(ctx, t, workdir, "init", "--name=app", "--sdk=go")
		require.NoError(t, err)

		out, err := hostDaggerExec(ctx, t, workdir, "--silent", "install", "./dep")
		require.NoError(t, err)
		require.Equal(t, `Installed module dependency "dep"`, strings.TrimSpace(string(out)))

		moduleConfig, err := os.ReadFile(filepath.Join(workdir, workspacecfg.ModuleConfigFileName))
		require.NoError(t, err)
		require.Contains(t, string(moduleConfig), `"name": "dep"`)

		_, err = os.Stat(filepath.Join(workdir, workspacecfg.LockDirName, workspacecfg.ConfigFileName))
		require.ErrorIs(t, err, os.ErrNotExist)
	})

	t.Run("keeps module dependency installs for explicit --mod targets", func(ctx context.Context, t *testctx.T) {
		workdir := t.TempDir()
		appDir := filepath.Join(workdir, "app")
		depDir := filepath.Join(workdir, "dep")

		require.NoError(t, os.MkdirAll(appDir, 0o755))
		require.NoError(t, os.MkdirAll(depDir, 0o755))
		initGitRepo(ctx, t, workdir)

		_, err := hostDaggerExec(ctx, t, workdir, "--silent", "workspace", "init")
		require.NoError(t, err)
		_, err = hostDaggerExec(ctx, t, appDir, "init", "--name=app", "--sdk=go")
		require.NoError(t, err)
		_, err = hostDaggerExec(ctx, t, depDir, "init", "--name=dep", "--sdk=go")
		require.NoError(t, err)

		out, err := hostDaggerExec(ctx, t, workdir, "--silent", "install", "--mod=./app", "./dep")
		require.NoError(t, err)
		require.Equal(t, `Installed module dependency "dep"`, strings.TrimSpace(string(out)))

		moduleConfig, err := os.ReadFile(filepath.Join(appDir, workspacecfg.ModuleConfigFileName))
		require.NoError(t, err)
		require.Contains(t, string(moduleConfig), `"name": "dep"`)

		cfg := readInstalledWorkspaceConfig(t, workdir)
		require.Empty(t, cfg.Modules)
	})
}
