package core

// These tests cover modules registered in a workspace config. They verify
// `dagger install`, listing, module names, configured sources, and settings for
// workspace-managed modules.
//
// See also:
// - module_dependency_runtime_test.go: runtime use of already-installed dependencies.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger.io/dagger"
	workspacecfg "github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/engine"
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

// TestWorkspaceModuleInstall covers module installation through both the CLI
// and the Workspace overlay/export API.
func (WorkspaceModulesSuite) TestWorkspaceModuleInstall(ctx context.Context, t *testctx.T) {
	t.Run("module init creates its explicit path with standard permissions", func(ctx context.Context, t *testctx.T) {
		workdir := t.TempDir()
		initGitRepo(ctx, t, workdir)

		_, err := hostDaggerExecRaw(ctx, t, workdir, "--silent", "install", "github.com/dagger/dang-sdk")
		require.NoError(t, err)

		_, err = hostDaggerExecRaw(ctx, t, workdir, "--silent", "--auto-apply", "sdk", "dang", "module", "init", "editor", "--path", "editor")
		require.NoError(t, err)

		info, err := os.Stat(filepath.Join(workdir, "editor"))
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o755), info.Mode().Perm(),
			"module directory mode: got %#o, want %#o", info.Mode().Perm(), os.FileMode(0o755))

		config, err := os.ReadFile(filepath.Join(workdir, "editor", workspacecfg.ModuleConfigFileName))
		require.NoError(t, err, "engine-authored module config should be preserved")
		require.Contains(t, string(config), fmt.Sprintf("engineVersion = %q", engine.Version))
		_, err = os.Stat(filepath.Join(workdir, "editor", "main.dang"))
		require.NoError(t, err, "SDK-authored starter source should be preserved")
	})

	t.Run("Workspace.WithModule initializes config and lock for remote modules", func(ctx context.Context, t *testctx.T) {
		workdir := t.TempDir()
		initGitRepo(ctx, t, workdir)

		c := connect(ctx, t, dagger.WithWorkdir(workdir))
		ref := "github.com/dagger/dagger/modules/wolfi@v0.20.2"

		current := c.CurrentWorkspace()
		updated := current.WithModule(ref, dagger.WorkspaceWithModuleOpts{Name: "mywolfi"})
		added, err := updated.Changes(dagger.WorkspaceChangesOpts{From: current}).AddedPaths(ctx)
		require.NoError(t, err)
		require.ElementsMatch(t, []string{workspacecfg.ConfigFileName, workspacecfg.LockFileName}, added)
		require.NoError(t, updated.Export(ctx))

		configBytes, err := os.ReadFile(filepath.Join(workdir, workspacecfg.ConfigFileName))
		require.NoError(t, err)

		cfg, err := workspacecfg.ParseConfig(configBytes)
		require.NoError(t, err)
		require.Contains(t, cfg.Modules, "mywolfi")
		require.Equal(t, ref, cfg.Modules["mywolfi"].Source)
		require.False(t, cfg.Modules["mywolfi"].Entrypoint)

		require.NoError(t, c.Close())

		lockBytes, err := os.ReadFile(filepath.Join(workdir, workspacecfg.LockFileName))
		require.NoError(t, err)
		assertNoModuleResolveLockEntry(t, lockBytes)
		require.Contains(t, string(lockBytes), `"git.ref"`)

		c = connect(ctx, t, dagger.WithWorkdir(workdir))
		current = c.CurrentWorkspace()
		updated = current.WithModule(ref, dagger.WorkspaceWithModuleOpts{Name: "mywolfi"})
		empty, err := updated.Changes(dagger.WorkspaceChangesOpts{From: current}).IsEmpty(ctx)
		require.NoError(t, err)
		require.True(t, empty)
	})

	t.Run("Workspace.WithModule rewrites local refs relative to dagger.toml", func(ctx context.Context, t *testctx.T) {
		workdir := t.TempDir()
		depDir := filepath.Join(workdir, "dep")

		require.NoError(t, os.MkdirAll(depDir, 0o755))
		initGitRepo(ctx, t, workdir)

		copyTestdataFixture(ctx, t, depDir, "modules", "go", "minimal-dep")

		c := connect(ctx, t, dagger.WithWorkdir(workdir))
		current := c.CurrentWorkspace()
		updated := current.WithModule("./dep")
		added, err := updated.Changes(dagger.WorkspaceChangesOpts{From: current}).AddedPaths(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{workspacecfg.ConfigFileName}, added)
		require.NoError(t, updated.Export(ctx))

		configBytes, err := os.ReadFile(filepath.Join(workdir, workspacecfg.ConfigFileName))
		require.NoError(t, err)

		cfg, err := workspacecfg.ParseConfig(configBytes)
		require.NoError(t, err)
		require.Contains(t, cfg.Modules, "dep")
		require.Equal(t, "dep", cfg.Modules["dep"].Source)
	})

	t.Run("install initializes empty workspace", func(ctx context.Context, t *testctx.T) {
		// With no native workspace config and no legacy dagger.json, `dagger
		// install` owns workspace initialization: it should create
		// dagger.toml and record the dependency there.
		workdir := t.TempDir()
		depDir := filepath.Join(workdir, "dep")

		require.NoError(t, os.MkdirAll(depDir, 0o755))
		initGitRepo(ctx, t, workdir)

		copyTestdataFixture(ctx, t, depDir, "modules", "go", "minimal-dep")

		out, err := hostDaggerExecRaw(ctx, t, workdir, "--silent", "install", "./dep")
		require.NoError(t, err)
		outStr := strings.TrimSpace(string(out))
		require.Contains(t, outStr, "Created workspace config in "+workdir)
		require.Contains(t, outStr, `Installed module "dep" in `+filepath.Join(workdir, workspacecfg.ConfigFileName))

		cfg := readInstalledWorkspaceConfig(t, workdir)
		require.Contains(t, cfg.Modules, "dep")
		require.Equal(t, "dep", cfg.Modules["dep"].Source)
	})

	t.Run("install omits commented settings hints", func(ctx context.Context, t *testctx.T) {
		workdir := t.TempDir()
		depDir := filepath.Join(workdir, "dep")

		require.NoError(t, os.MkdirAll(depDir, 0o755))
		initGitRepo(ctx, t, workdir)
		copyTestdataFixture(ctx, t, depDir, "modules", "go", "defaults", "superconstructor")

		_, err := hostDaggerExecRaw(ctx, t, workdir, "--silent", "install", "./dep")
		require.NoError(t, err)

		configBytes, err := os.ReadFile(filepath.Join(workdir, workspacecfg.ConfigFileName))
		require.NoError(t, err)
		require.NotContains(t, string(configBytes), "# settings.")
	})

	t.Run("workspace install pins Git resolution without a modules.resolve entry", func(ctx context.Context, t *testctx.T) {
		workdir := t.TempDir()
		initGitRepo(ctx, t, workdir)

		ref := "github.com/dagger/dagger/modules/wolfi@v0.20.2"
		out, err := hostDaggerExecRaw(ctx, t, workdir, "--silent", "install", ref)
		require.NoError(t, err)
		require.Equal(t,
			"Created workspace config in "+workdir+"\n"+
				`Installed module "wolfi" in `+filepath.Join(workdir, workspacecfg.ConfigFileName),
			strings.TrimSpace(string(out)),
		)

		lockBytes, err := os.ReadFile(filepath.Join(workdir, workspacecfg.LockFileName))
		require.NoError(t, err)
		assertNoModuleResolveLockEntry(t, lockBytes)
		require.Contains(t, string(lockBytes), `"git.ref"`)
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

		_, err := hostDaggerExecRaw(ctx, t, workdir, "--silent", "install", "--load-module=.", "./dep")
		require.Error(t, err)
		requireErrOut(t, err, "unknown flag: --load-module")
	})

	t.Run("install rejects non-module refs without corrupting config", func(ctx context.Context, t *testctx.T) {
		workdir := t.TempDir()
		emptyDir := filepath.Join(workdir, "empty")

		require.NoError(t, os.MkdirAll(emptyDir, 0o755))
		initGitRepo(ctx, t, workdir)
		// `dagger workspace init` was removed in CLI 1.0; seed an empty native
		// workspace config directly so the failed install has something to
		// (not) corrupt.
		writeWorkspaceConfigFile(t, workdir, "[modules]\n")

		_, err := hostDaggerExecRaw(ctx, t, workdir, "--silent", "install", "./empty")
		require.Error(t, err)
		requireErrOut(t, err, `ref "./empty" does not point to an initialized module`)

		configBytes, err := os.ReadFile(filepath.Join(workdir, workspacecfg.ConfigFileName))
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
			`Uninstalled module "dep" from `+filepath.Join(workdir, workspacecfg.ConfigFileName))

		require.NotContains(t, readInstalledWorkspaceConfig(t, workdir).Modules, "dep")
	})

	t.Run("mod uninstall alias removes a module", func(ctx context.Context, t *testctx.T) {
		workdir := t.TempDir()
		depDir := filepath.Join(workdir, "dep")

		require.NoError(t, os.MkdirAll(depDir, 0o755))
		initGitRepo(ctx, t, workdir)
		copyTestdataFixture(ctx, t, depDir, "modules", "go", "minimal-dep")

		_, err := hostDaggerExecRaw(ctx, t, workdir, "--silent", "install", "./dep")
		require.NoError(t, err)
		require.Contains(t, readInstalledWorkspaceConfig(t, workdir).Modules, "dep")

		_, err = hostDaggerExecRaw(ctx, t, workdir, "--silent", "uninstall", "dep")
		require.NoError(t, err)
		require.NotContains(t, readInstalledWorkspaceConfig(t, workdir).Modules, "dep")
	})

	t.Run("uninstall removes an SDK-managed default-path module", func(ctx context.Context, t *testctx.T) {
		workdir := t.TempDir()
		initGitRepo(ctx, t, workdir)

		_, err := hostDaggerExecRaw(ctx, t, workdir, "--silent", "install", "github.com/dagger/go-sdk")
		require.NoError(t, err)

		_, err = hostDaggerExecRaw(ctx, t, workdir, "--silent", "--auto-apply", "sdk", "go", "module", "init", "myapp")
		require.NoError(t, err)

		moduleDir := filepath.Join(workdir, ".dagger", "modules", "myapp")
		info, err := os.Stat(moduleDir)
		require.NoError(t, err)
		require.True(t, info.IsDir())

		cfg := readInstalledWorkspaceConfig(t, workdir)
		require.Contains(t, cfg.Modules, "myapp")
		goSDK := cfg.SDKs["go"]
		require.Equal(t, "go-sdk", goSDK.Module)
		require.Equal(t, []string{".dagger/modules/myapp"}, goSDK.Claimed.Modules)

		_, err = hostDaggerExecRaw(ctx, t, workdir, "--silent", "uninstall", "myapp")
		require.NoError(t, err)

		cfg = readInstalledWorkspaceConfig(t, workdir)
		require.NotContains(t, cfg.Modules, "myapp")
		goSDK = cfg.SDKs["go"]
		require.Equal(t, "go-sdk", goSDK.Module)
		require.Empty(t, goSDK.Claimed.Modules)

		_, err = os.Stat(moduleDir)
		require.True(t, os.IsNotExist(err), "expected %s to be removed, got %v", moduleDir, err)
	})

	t.Run("uninstalling an unknown module errors", func(ctx context.Context, t *testctx.T) {
		workdir := t.TempDir()
		initGitRepo(ctx, t, workdir)
		// `dagger workspace init` was removed in CLI 1.0; seed an empty native
		// workspace config directly so uninstall has a workspace to look in.
		writeWorkspaceConfigFile(t, workdir, "[modules]\n")

		_, err := hostDaggerExecRaw(ctx, t, workdir, "--silent", "uninstall", "ghost")
		require.Error(t, err)
		requireErrOut(t, err, `module "ghost" is not installed in the workspace`)
	})
}

// TestWorkspaceModuleGenerate covers generation for modules registered in a
// workspace.
func (WorkspaceModulesSuite) TestWorkspaceModuleGenerate(ctx context.Context, t *testctx.T) {
	setupSDKManagedGoModule := func(ctx context.Context, t *testctx.T) (string, string) {
		workdir := t.TempDir()
		initGitRepo(ctx, t, workdir)

		_, err := hostDaggerExecRaw(ctx, t, workdir, "--silent", "install", "github.com/dagger/go-sdk")
		require.NoError(t, err)

		_, err = hostDaggerExecRaw(ctx, t, workdir, "--silent", "--auto-apply", "sdk", "go", "module", "init", "myapp")
		require.NoError(t, err)

		moduleDir := filepath.Join(workdir, ".dagger", "modules", "myapp")
		require.NoError(t, os.WriteFile(filepath.Join(moduleDir, "main.go"), []byte(`package main

type Myapp struct{}
`), 0o644))

		return workdir, moduleDir
	}

	workdir, moduleDir := setupSDKManagedGoModule(ctx, t)
	cwd := filepath.Join(workdir, ".dagger")

	out, err := hostDaggerExecRaw(ctx, t, cwd, "--silent", "generate", "-y")
	require.NoError(t, err, "%s: %s", ".dagger", string(out))

	_, err = os.Stat(filepath.Join(moduleDir, "internal", "dagger", "dagger.gen.go"))
	require.NoError(t, err)

	nestedGeneratedClient := filepath.Join(cwd, ".dagger", "modules", "myapp", "internal", "dagger", "dagger.gen.go")
	_, err = os.Stat(nestedGeneratedClient)
	require.True(t, os.IsNotExist(err), "expected generate from .dagger to write at workspace root, not %s", nestedGeneratedClient)
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
source = "existing"
`)

		_, err := hostDaggerExecRaw(ctx, t, workdir, "--silent", "install", "--name=dep", "./dep")
		require.Error(t, err)
		requireErrOut(t, err, `module "dep" already exists in workspace config with source "existing" (new source "dep")`)

		cfg := readInstalledWorkspaceConfig(t, workdir)
		require.Contains(t, cfg.Modules, "dep")
		require.Equal(t, "existing", cfg.Modules["dep"].Source)
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
		// Once dagger.toml exists, it is authoritative for workspace
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

	configBytes, err := os.ReadFile(filepath.Join(workdir, workspacecfg.ConfigFileName))
	require.NoError(t, err)

	cfg, err := workspacecfg.ParseConfig(configBytes)
	require.NoError(t, err)
	return cfg
}
