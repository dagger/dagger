package core

// These tests cover modules registered in a workspace config. They verify
// `dagger install`, listing, module names, configured sources, and settings for
// workspace-managed modules.
//
// See also:
// - module_dependency_runtime_test.go: runtime use of already-installed dependencies.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger.io/dagger"
	workspacecfg "github.com/dagger/dagger/core/workspace"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// WorkspaceModulesSuite owns configuration-facing module behavior in a
// workspace: installing modules, listing them, naming them, and keeping their
// configured sources correct.
type WorkspaceModulesSuite struct{}

func TestWorkspaceModules(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(WorkspaceModulesSuite{})
}

// TestWorkspaceModuleInstall should cover module installation into a
// workspace, through both CLI commands and CurrentWorkspace.Install.
func (WorkspaceModulesSuite) TestWorkspaceModuleInstall(ctx context.Context, t *testctx.T) {
	t.Run("CurrentWorkspace.Install initializes a workspace for remote modules", func(ctx context.Context, t *testctx.T) {
		workdir := t.TempDir()
		initGitRepo(ctx, t, workdir)

		c := connect(ctx, t, dagger.WithWorkdir(workdir))
		ref := "github.com/dagger/dagger/modules/wolfi@v0.20.2"

		msg, err := c.CurrentWorkspace().Install(ctx, ref, dagger.WorkspaceInstallOpts{Name: "mywolfi"})
		require.NoError(t, err)
		require.Equal(t,
			"Created workspace config in "+filepath.Join(workdir, ".dagger")+"\n"+
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

	t.Run("CurrentWorkspace.Install rewrites local refs relative to .dagger", func(ctx context.Context, t *testctx.T) {
		workdir := t.TempDir()
		depDir := filepath.Join(workdir, "dep")

		require.NoError(t, os.MkdirAll(depDir, 0o755))
		initGitRepo(ctx, t, workdir)

		copyTestdataFixture(ctx, t, depDir, "modules", "go", "minimal-dep")

		c := connect(ctx, t, dagger.WithWorkdir(workdir))
		msg, err := c.CurrentWorkspace().Install(ctx, "./dep")
		require.NoError(t, err)
		require.Equal(t,
			"Created workspace config in "+filepath.Join(workdir, ".dagger")+"\n"+
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

	t.Run("install initializes empty workspace", func(ctx context.Context, t *testctx.T) {
		// With no native workspace config and no legacy dagger.json, `dagger
		// install` owns workspace initialization: it should create
		// .dagger/config.toml and record the dependency there.
		workdir := t.TempDir()
		depDir := filepath.Join(workdir, "dep")

		require.NoError(t, os.MkdirAll(depDir, 0o755))
		initGitRepo(ctx, t, workdir)

		copyTestdataFixture(ctx, t, depDir, "modules", "go", "minimal-dep")

		out, err := hostDaggerExecRaw(ctx, t, workdir, "--silent", "install", "./dep")
		require.NoError(t, err)
		outStr := strings.TrimSpace(string(out))
		require.Contains(t, outStr, "Created workspace config in "+filepath.Join(workdir, workspacecfg.LockDirName))
		require.Contains(t, outStr, `Installed module "dep" in `+filepath.Join(workdir, workspacecfg.LockDirName, workspacecfg.ConfigFileName))

		cfg := readInstalledWorkspaceConfig(t, workdir)
		require.Contains(t, cfg.Modules, "dep")
		require.Equal(t, "../dep", cfg.Modules["dep"].Source)
	})

	t.Run("workspace install writes install lock entries by default", func(ctx context.Context, t *testctx.T) {
		workdir := t.TempDir()
		initGitRepo(ctx, t, workdir)

		ref := "github.com/dagger/dagger/modules/wolfi@v0.20.2"
		out, err := hostDaggerExecRaw(ctx, t, workdir, "--silent", "install", ref)
		require.NoError(t, err)
		require.Equal(t,
			"Created workspace config in "+filepath.Join(workdir, workspacecfg.LockDirName)+"\n"+
				`Installed module "wolfi" in `+filepath.Join(workdir, workspacecfg.LockDirName, workspacecfg.ConfigFileName),
			strings.TrimSpace(string(out)),
		)

		lockBytes, err := os.ReadFile(filepath.Join(workdir, workspacecfg.LockDirName, workspacecfg.LockFileName))
		require.NoError(t, err)
		assertModuleResolveLockEntry(t, lockBytes, ref, workspacecfg.PolicyPin)
	})

	t.Run("absolute local installs preserve absolute source paths", func(ctx context.Context, t *testctx.T) {
		workdir := t.TempDir()
		depDir := t.TempDir()

		initGitRepo(ctx, t, workdir)
		initGitRepo(ctx, t, depDir)

		copyTestdataFixture(ctx, t, depDir, "modules", "go", "minimal-dep")
		require.NoError(t, os.WriteFile(filepath.Join(depDir, "main.go"), []byte(`package main

type Dep struct{}

func (m *Dep) Greet() string {
	return "hello from absolute workspace module"
}
`), 0o644))

		writeWorkspaceConfigFile(t, workdir, `[modules.dep]
source = "`+depDir+`"
entrypoint = true
`)

		out, err := hostDaggerExec(ctx, t, workdir, "--silent", "call", "greet")
		require.NoError(t, err)
		require.Equal(t, "hello from absolute workspace module", strings.TrimSpace(string(out)))
	})

	t.Run("workspace install rejects module-specific flags", func(ctx context.Context, t *testctx.T) {
		workdir := t.TempDir()
		initGitRepo(ctx, t, workdir)

		_, err := hostDaggerExecRaw(ctx, t, workdir, "--silent", "install", "--mod=.", "./dep")
		require.Error(t, err)
		requireErrOut(t, err, "unknown flag: --mod")
	})

	t.Run("install rejects non-module refs without corrupting config", func(ctx context.Context, t *testctx.T) {
		workdir := t.TempDir()
		emptyDir := filepath.Join(workdir, "empty")

		require.NoError(t, os.MkdirAll(emptyDir, 0o755))
		initGitRepo(ctx, t, workdir)

		_, err := hostDaggerExecRaw(ctx, t, workdir, "--silent", "workspace", "init")
		require.NoError(t, err)

		_, err = hostDaggerExecRaw(ctx, t, workdir, "--silent", "install", "./empty")
		require.Error(t, err)
		requireErrOut(t, err, `ref "./empty" does not point to an initialized module`)

		configBytes, err := os.ReadFile(filepath.Join(workdir, workspacecfg.LockDirName, workspacecfg.ConfigFileName))
		require.NoError(t, err)
		require.NotContains(t, string(configBytes), "[modules.]")

		cfg, err := workspacecfg.ParseConfig(configBytes)
		require.NoError(t, err)
		require.Empty(t, cfg.Modules)
	})
}

// TestWorkspaceModuleUninstall should cover removing modules from a workspace,
// via both `dagger uninstall` and the `dagger mod uninstall` alias.
func (WorkspaceModulesSuite) TestWorkspaceModuleUninstall(ctx context.Context, t *testctx.T) {
	t.Run("uninstall removes a module from config", func(ctx context.Context, t *testctx.T) {
		workdir := t.TempDir()
		depDir := filepath.Join(workdir, "dep")

		require.NoError(t, os.MkdirAll(depDir, 0o755))
		initGitRepo(ctx, t, workdir)
		copyTestdataFixture(ctx, t, depDir, "modules", "go", "minimal-dep")

		_, err := hostDaggerExecRaw(ctx, t, workdir, "--silent", "install", "./dep")
		require.NoError(t, err)
		require.Contains(t, readInstalledWorkspaceConfig(t, workdir).Modules, "dep")

		out, err := hostDaggerExecRaw(ctx, t, workdir, "--silent", "uninstall", "dep")
		require.NoError(t, err)
		require.Contains(t, strings.TrimSpace(string(out)),
			`Uninstalled module "dep" from `+filepath.Join(workdir, workspacecfg.LockDirName, workspacecfg.ConfigFileName))

		require.NotContains(t, readInstalledWorkspaceConfig(t, workdir).Modules, "dep")
	})

	t.Run("mod uninstall alias removes a module", func(ctx context.Context, t *testctx.T) {
		workdir := t.TempDir()
		depDir := filepath.Join(workdir, "dep")

		require.NoError(t, os.MkdirAll(depDir, 0o755))
		initGitRepo(ctx, t, workdir)
		copyTestdataFixture(ctx, t, depDir, "modules", "go", "minimal-dep")

		_, err := hostDaggerExecRaw(ctx, t, workdir, "--silent", "mod", "install", "./dep")
		require.NoError(t, err)
		require.Contains(t, readInstalledWorkspaceConfig(t, workdir).Modules, "dep")

		_, err = hostDaggerExecRaw(ctx, t, workdir, "--silent", "mod", "uninstall", "dep")
		require.NoError(t, err)
		require.NotContains(t, readInstalledWorkspaceConfig(t, workdir).Modules, "dep")
	})

	t.Run("uninstalling an unknown module errors", func(ctx context.Context, t *testctx.T) {
		workdir := t.TempDir()
		initGitRepo(ctx, t, workdir)

		_, err := hostDaggerExecRaw(ctx, t, workdir, "--silent", "workspace", "init")
		require.NoError(t, err)

		_, err = hostDaggerExecRaw(ctx, t, workdir, "--silent", "uninstall", "ghost")
		require.Error(t, err)
		requireErrOut(t, err, `module "ghost" is not installed in the workspace`)
	})
}

// TestWorkspaceModuleMutation should cover updates and config-level conflicts
// around configured modules.
func (WorkspaceModulesSuite) TestWorkspaceModuleMutation(ctx context.Context, t *testctx.T) {
	t.Run("name collisions are rejected without rewriting config", func(ctx context.Context, t *testctx.T) {
		workdir := t.TempDir()
		depDir := filepath.Join(workdir, "dep")

		require.NoError(t, os.MkdirAll(depDir, 0o755))
		initGitRepo(ctx, t, workdir)

		copyTestdataFixture(ctx, t, depDir, "modules", "go", "minimal-dep")

		writeWorkspaceConfigFile(t, workdir, `[modules.dep]
source = "../existing"
`)

		_, err := hostDaggerExecRaw(ctx, t, workdir, "--silent", "install", "--name=dep", "./dep")
		require.Error(t, err)
		requireErrOut(t, err, `module "dep" already exists in workspace config with source "../existing" (new source "../dep")`)

		cfg := readInstalledWorkspaceConfig(t, workdir)
		require.Contains(t, cfg.Modules, "dep")
		require.Equal(t, "../existing", cfg.Modules["dep"].Source)
	})
}

// TestWorkspaceManagedModuleBehavior covers runtime behavior that depends on a
// module being configured in a workspace, but is not about entrypoint routing.
func (WorkspaceModulesSuite) TestWorkspaceManagedModuleBehavior(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("main object with prefixed children", func(ctx context.Context, t *testctx.T) {
		base := workspaceFixture(t, c, "workspace-managed")

		out, err := base.With(daggerCall("objects", "object-a", "message")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "Hello from A", strings.TrimSpace(out))

		out, err = base.With(daggerCall("objects", "object-a", "object-b", "message")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "Hello from B", strings.TrimSpace(out))
	})

	t.Run("renamed workspace-installed module", func(ctx context.Context, t *testctx.T) {
		base := workspaceFixture(t, c, "workspace-managed")

		out, err := base.With(daggerCall("greeter", "greet")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello, world!", strings.TrimSpace(out))

		out, err = base.With(daggerCall("greeter", "greet", "--name", "dagger")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello, dagger!", strings.TrimSpace(out))

		out, err = base.With(daggerFunctions()).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "greeter")

		out, err = base.With(daggerShell("greeter | greet")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello, world!", out)
	})

	t.Run("native workspace ignores cwd dagger.json", func(ctx context.Context, t *testctx.T) {
		// Once .dagger/config.toml exists, it is authoritative for workspace
		// module commands. A dagger.json in the current working directory must
		// not steal resolution away from the configured workspace module.
		ctr := workspaceFixture(t, c, "workspace-managed")

		out, err := ctr.
			WithWorkdir("/work/modules/cwd").
			With(daggerCall("greet")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello from configured workspace", strings.TrimSpace(out))
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
