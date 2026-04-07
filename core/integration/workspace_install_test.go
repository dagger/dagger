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
		require.Equal(t,
			"Initialized workspace in "+filepath.Join(workdir, ".dagger")+"\n"+
				`Installed module "mywolfi" in `+filepath.Join(workdir, ".dagger", workspacecfg.ConfigFileName),
			msg,
		)

		configBytes, err := os.ReadFile(filepath.Join(workdir, ".dagger", workspacecfg.ConfigFileName))
		require.NoError(t, err)

		cfg, err := workspacecfg.ParseConfig(configBytes)
		require.NoError(t, err)
		require.Contains(t, cfg.Modules, "mywolfi")
		require.Equal(t, ref, cfg.Modules["mywolfi"].Source)
		require.False(t, cfg.Modules["mywolfi"].Entrypoint)

		require.NoError(t, c.Close())

		lockBytes, err := os.ReadFile(filepath.Join(workdir, workspacecfg.LockDirName, workspacecfg.LockFileName))
		require.NoError(t, err)
		assertModuleResolveLockEntry(t, lockBytes, ref, workspacecfg.PolicyPin)

		c = connect(ctx, t, dagger.WithWorkdir(workdir))
		msg, err = c.CurrentWorkspace().Install(ctx, ref, dagger.WorkspaceInstallOpts{Name: "mywolfi"})
		require.NoError(t, err)
		require.Equal(t, `Module "mywolfi" is already installed`, msg)
	})

	t.Run("rewrites local module refs relative to .dagger", func(ctx context.Context, t *testctx.T) {
		workdir := t.TempDir()
		depDir := filepath.Join(workdir, "dep")

		require.NoError(t, os.MkdirAll(depDir, 0o755))
		initGitRepo(ctx, t, workdir)

		_, err := hostDaggerExec(ctx, t, depDir, "module", "init", "--name=dep", "--sdk=go")
		require.NoError(t, err)

		c := connect(ctx, t, dagger.WithWorkdir(workdir))
		msg, err := c.CurrentWorkspace().Install(ctx, "./dep")
		require.NoError(t, err)
		require.Equal(t,
			"Initialized workspace in "+filepath.Join(workdir, ".dagger")+"\n"+
				`Installed module "dep" in `+filepath.Join(workdir, ".dagger", workspacecfg.ConfigFileName),
			msg,
		)

		configBytes, err := os.ReadFile(filepath.Join(workdir, ".dagger", workspacecfg.ConfigFileName))
		require.NoError(t, err)

		cfg, err := workspacecfg.ParseConfig(configBytes)
		require.NoError(t, err)
		require.Contains(t, cfg.Modules, "dep")
		require.Equal(t, "../dep", cfg.Modules["dep"].Source)
	})

	t.Run("rejects name collisions without rewriting config", func(ctx context.Context, t *testctx.T) {
		workdir := t.TempDir()
		depDir := filepath.Join(workdir, "dep")

		require.NoError(t, os.MkdirAll(depDir, 0o755))
		initGitRepo(ctx, t, workdir)

		_, err := hostDaggerExec(ctx, t, depDir, "module", "init", "--name=dep", "--sdk=go")
		require.NoError(t, err)

		writeWorkspaceConfigFile(t, workdir, `[modules.dep]
source = "../existing"
`)

		_, err = hostDaggerExecRaw(ctx, t, workdir, "--silent", "install", "--name=dep", "./dep")
		require.Error(t, err)
		requireErrOut(t, err, `module "dep" already exists in workspace config with source "../existing" (new source "../dep")`)

		cfg := readInstalledWorkspaceConfig(t, workdir)
		require.Contains(t, cfg.Modules, "dep")
		require.Equal(t, "../existing", cfg.Modules["dep"].Source)
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
	t.Run("installs into the workspace when no workspace config exists yet", func(ctx context.Context, t *testctx.T) {
		workdir := t.TempDir()
		depDir := filepath.Join(workdir, "dep")

		require.NoError(t, os.MkdirAll(depDir, 0o755))
		initGitRepo(ctx, t, workdir)

		_, err := hostDaggerExec(ctx, t, depDir, "module", "init", "--name=dep", "--sdk=go")
		require.NoError(t, err)

		out, err := hostDaggerExecRaw(ctx, t, workdir, "--silent", "install", "./dep")
		require.NoError(t, err)
		require.Equal(t,
			"Initialized workspace in "+filepath.Join(workdir, workspacecfg.LockDirName)+"\n"+
				`Installed module "dep" in `+filepath.Join(workdir, workspacecfg.LockDirName, workspacecfg.ConfigFileName),
			strings.TrimSpace(string(out)),
		)

		cfg := readInstalledWorkspaceConfig(t, workdir)
		require.Contains(t, cfg.Modules, "dep")
		require.Equal(t, "../dep", cfg.Modules["dep"].Source)
	})

	t.Run("writes install lock entries by default", func(ctx context.Context, t *testctx.T) {
		workdir := t.TempDir()
		initGitRepo(ctx, t, workdir)

		ref := "github.com/dagger/dagger/modules/wolfi@v0.20.2"
		out, err := hostDaggerExecRaw(ctx, t, workdir, "--silent", "install", ref)
		require.NoError(t, err)
		require.Equal(t,
			"Initialized workspace in "+filepath.Join(workdir, workspacecfg.LockDirName)+"\n"+
				`Installed module "wolfi" in `+filepath.Join(workdir, workspacecfg.LockDirName, workspacecfg.ConfigFileName),
			strings.TrimSpace(string(out)),
		)

		lockBytes, err := os.ReadFile(filepath.Join(workdir, workspacecfg.LockDirName, workspacecfg.LockFileName))
		require.NoError(t, err)
		assertModuleResolveLockEntry(t, lockBytes, ref, workspacecfg.PolicyPin)
	})

	t.Run("creates workspace config instead of editing dagger.json for standalone modules", func(ctx context.Context, t *testctx.T) {
		workdir := t.TempDir()
		depDir := filepath.Join(workdir, "dep")

		require.NoError(t, os.MkdirAll(depDir, 0o755))
		initGitRepo(ctx, t, workdir)

		_, err := hostDaggerExec(ctx, t, depDir, "module", "init", "--name=dep", "--sdk=go")
		require.NoError(t, err)
		_, err = hostDaggerExec(ctx, t, workdir, "module", "init", "--name=app", "--sdk=go")
		require.NoError(t, err)

		out, err := hostDaggerExecRaw(ctx, t, workdir, "--silent", "install", "./dep")
		require.NoError(t, err)
		require.Equal(t,
			"Initialized workspace in "+filepath.Join(workdir, workspacecfg.LockDirName)+"\n"+
				`Installed module "dep" in `+filepath.Join(workdir, workspacecfg.LockDirName, workspacecfg.ConfigFileName),
			strings.TrimSpace(string(out)),
		)

		moduleConfig, err := os.ReadFile(filepath.Join(workdir, workspacecfg.ModuleConfigFileName))
		require.NoError(t, err)
		require.NotContains(t, string(moduleConfig), `"name": "dep"`)

		cfg := readInstalledWorkspaceConfig(t, workdir)
		require.Contains(t, cfg.Modules, "dep")
		require.Equal(t, "../dep", cfg.Modules["dep"].Source)
	})

	t.Run("rejects module-specific flags", func(ctx context.Context, t *testctx.T) {
		workdir := t.TempDir()
		initGitRepo(ctx, t, workdir)

		_, err := hostDaggerExecRaw(ctx, t, workdir, "--silent", "install", "--mod=.", "./dep")
		require.Error(t, err)
		requireErrOut(t, err, "unknown flag: --mod")
	})
}

func (WorkspaceSuite) TestModuleInstallCommand(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	appDir := filepath.Join(workdir, "app")
	depDir := filepath.Join(workdir, "dep")

	require.NoError(t, os.MkdirAll(appDir, 0o755))
	require.NoError(t, os.MkdirAll(depDir, 0o755))
	initGitRepo(ctx, t, workdir)

	_, err := hostDaggerExec(ctx, t, workdir, "--silent", "workspace", "init")
	require.NoError(t, err)
	_, err = hostDaggerExec(ctx, t, appDir, "module", "init", "--name=app", "--sdk=go", ".")
	require.NoError(t, err)
	_, err = hostDaggerExec(ctx, t, depDir, "module", "init", "--name=dep", "--sdk=go", ".")
	require.NoError(t, err)

	out, err := hostDaggerExec(ctx, t, workdir, "--silent", "module", "install", "--mod=./app", "./dep")
	require.NoError(t, err)
	require.Equal(t, `Installed module dependency "dep"`, strings.TrimSpace(string(out)))

	moduleConfig, err := os.ReadFile(filepath.Join(appDir, workspacecfg.ModuleConfigFileName))
	require.NoError(t, err)
	require.Contains(t, string(moduleConfig), `"name": "dep"`)

	cfg := readInstalledWorkspaceConfig(t, workdir)
	require.Empty(t, cfg.Modules)
}

func (WorkspaceSuite) TestModuleUpdateCommand(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	appDir := filepath.Join(workdir, "app")
	depDir := filepath.Join(workdir, "dep")

	require.NoError(t, os.MkdirAll(appDir, 0o755))
	require.NoError(t, os.MkdirAll(depDir, 0o755))
	initGitRepo(ctx, t, workdir)

	_, err := hostDaggerExec(ctx, t, workdir, "--silent", "workspace", "init")
	require.NoError(t, err)
	_, err = hostDaggerExec(ctx, t, appDir, "module", "init", "--name=app", "--sdk=go", ".")
	require.NoError(t, err)
	_, err = hostDaggerExec(ctx, t, depDir, "module", "init", "--name=dep", "--sdk=go", ".")
	require.NoError(t, err)
	_, err = hostDaggerExec(ctx, t, workdir, "--silent", "module", "install", "--mod=./app", "./dep")
	require.NoError(t, err)

	_, err = hostDaggerExec(ctx, t, workdir, "--silent", "module", "update", "--mod=./app", "dep")
	requireErrOut(t, err, `updating local dependencies is not supported`)

	cfg := readInstalledWorkspaceConfig(t, workdir)
	require.Empty(t, cfg.Modules)
}
