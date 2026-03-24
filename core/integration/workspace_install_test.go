package core

import (
	"context"
	"os"
	"path/filepath"

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
