package core

// Workspace alignment: aligned; this file already matches the workspace-era split.
// Scope: Workspace-facing module install, list, naming, and configured source behavior.
// Intent: Keep workspace module settings behavior separate from raw module dependency mutation behavior.

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

func initWorkspaceDangModule(ctx context.Context, t *testctx.T, ctr *dagger.Container, name string) *dagger.Container {
	t.Helper()

	initCtr := ctr.WithExec([]string{"dagger", "module", "init", "--sdk=dang", "--name=" + name}, dagger.ContainerWithExecOpts{
		Expect:                        dagger.ReturnTypeAny,
		ExperimentalPrivilegedNesting: true,
	})
	initCode, err := initCtr.ExitCode(ctx)
	require.NoError(t, err)
	initOut, err := initCtr.Stdout(ctx)
	require.NoError(t, err)
	initErr, err := initCtr.Stderr(ctx)
	require.NoError(t, err)
	require.Zero(t, initCode, "stdout:\n%s\nstderr:\n%s", initOut, initErr)
	require.Contains(t, initOut, `Created module "`+name+`"`)
	require.Contains(t, initOut, `.dagger/modules/`+name)

	return initCtr
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

	t.Run("CurrentWorkspace.Install rewrites local refs relative to .dagger", func(ctx context.Context, t *testctx.T) {
		workdir := t.TempDir()
		depDir := filepath.Join(workdir, "dep")

		require.NoError(t, os.MkdirAll(depDir, 0o755))
		initGitRepo(ctx, t, workdir)

		_, err := hostDaggerModuleExec(ctx, t, depDir, "init", "--name=dep", "--sdk=go")
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

	t.Run("workspace install initializes a workspace when needed", func(ctx context.Context, t *testctx.T) {
		workdir := t.TempDir()
		depDir := filepath.Join(workdir, "dep")

		require.NoError(t, os.MkdirAll(depDir, 0o755))
		initGitRepo(ctx, t, workdir)

		_, err := hostDaggerModuleExec(ctx, t, depDir, "init", "--name=dep", "--sdk=go")
		require.NoError(t, err)

		out, err := hostDaggerExecRaw(ctx, t, workdir, "--silent", "install", "./dep")
		require.NoError(t, err)
		outStr := strings.TrimSpace(string(out))
		require.Contains(t, outStr, "Initialized workspace in "+filepath.Join(workdir, workspacecfg.LockDirName))
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
			"Initialized workspace in "+filepath.Join(workdir, workspacecfg.LockDirName)+"\n"+
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

		_, err := hostDaggerModuleExec(ctx, t, depDir, "init", "--name=dep", "--sdk=go")
		require.NoError(t, err)
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

		out, err := hostDaggerExec(ctx, t, workdir, "--silent", "workspace", "list")
		require.NoError(t, err)
		require.Contains(t, string(out), depDir)

		out, err = hostDaggerExec(ctx, t, workdir, "--silent", "call", "greet")
		require.NoError(t, err)
		require.Equal(t, "hello from absolute workspace module", strings.TrimSpace(string(out)))
	})

	t.Run("workspace install creates workspace config instead of editing dagger.json for standalone modules", func(ctx context.Context, t *testctx.T) {
		workdir := t.TempDir()
		depDir := filepath.Join(workdir, "dep")

		require.NoError(t, os.MkdirAll(depDir, 0o755))
		initGitRepo(ctx, t, workdir)

		_, err := hostDaggerModuleExec(ctx, t, depDir, "init", "--name=dep", "--sdk=go")
		require.NoError(t, err)
		_, err = hostDaggerModuleExec(ctx, t, workdir, "init", "--name=app", "--sdk=go")
		require.NoError(t, err)

		out, err := hostDaggerExecRaw(ctx, t, workdir, "--silent", "install", "./dep")
		require.NoError(t, err)
		outStr := strings.TrimSpace(string(out))
		require.Contains(t, outStr, "Initialized workspace in "+filepath.Join(workdir, workspacecfg.LockDirName))
		require.Contains(t, outStr, `Installed module "dep" in `+filepath.Join(workdir, workspacecfg.LockDirName, workspacecfg.ConfigFileName))

		moduleConfig, err := os.ReadFile(filepath.Join(workdir, workspacecfg.ModuleConfigFileName))
		require.NoError(t, err)
		require.NotContains(t, string(moduleConfig), `"name": "dep"`)

		cfg := readInstalledWorkspaceConfig(t, workdir)
		require.Contains(t, cfg.Modules, "dep")
		require.Equal(t, "../dep", cfg.Modules["dep"].Source)
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

// TestWorkspaceModuleListing should cover how configured modules are rendered
// back to the user.
func (WorkspaceModulesSuite) TestWorkspaceModuleListing(ctx context.Context, t *testctx.T) {
	workdir := newWorkspaceConfigWorkdir(ctx, t, `[modules.greeter]
source = "modules/greeter"
entrypoint = true

[modules.wolfi]
source = "github.com/dagger/dagger/modules/wolfi"
`)

	out, err := hostDaggerExec(ctx, t, workdir, "--silent", "workspace", "list")
	require.NoError(t, err)

	output := string(out)
	require.Contains(t, output, "Source paths below are resolved and shown relative to the workspace root")
	require.Contains(t, output, "* indicates a module is the workspace entrypoint")
	require.Contains(t, output, "greeter*")
	require.Contains(t, output, ".dagger/modules/greeter")
	require.Contains(t, output, "wolfi")
	require.Contains(t, output, "github.com/dagger/dagger/modules/wolfi")
	require.Less(t, strings.Index(output, "greeter*"), strings.Index(output, "wolfi"))
}

// TestWorkspaceModuleMutation should cover updates and config-level conflicts
// around configured modules.
func (WorkspaceModulesSuite) TestWorkspaceModuleMutation(ctx context.Context, t *testctx.T) {
	t.Run("name collisions are rejected without rewriting config", func(ctx context.Context, t *testctx.T) {
		workdir := t.TempDir()
		depDir := filepath.Join(workdir, "dep")

		require.NoError(t, os.MkdirAll(depDir, 0o755))
		initGitRepo(ctx, t, workdir)

		_, err := hostDaggerModuleExec(ctx, t, depDir, "init", "--name=dep", "--sdk=go")
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

	t.Run("local dependency updates are rejected when unsupported", func(ctx context.Context, t *testctx.T) {
		workdir := t.TempDir()
		appDir := filepath.Join(workdir, "app")
		depDir := filepath.Join(workdir, "dep")

		require.NoError(t, os.MkdirAll(appDir, 0o755))
		require.NoError(t, os.MkdirAll(depDir, 0o755))
		initGitRepo(ctx, t, workdir)

		_, err := hostDaggerExecRaw(ctx, t, workdir, "--silent", "workspace", "init")
		require.NoError(t, err)
		_, err = hostDaggerModuleExec(ctx, t, appDir, "init", "--name=app", "--sdk=go", ".")
		require.NoError(t, err)
		_, err = hostDaggerModuleExec(ctx, t, depDir, "init", "--name=dep", "--sdk=go", ".")
		require.NoError(t, err)
		_, err = hostDaggerExecRaw(ctx, t, workdir, "--silent", "module", "install", "--mod=./app", "./dep")
		require.NoError(t, err)

		_, err = hostDaggerExecRaw(ctx, t, workdir, "--silent", "module", "update", "--mod=./app", "dep")
		requireErrOut(t, err, `updating local dependencies is not supported`)

		cfg := readInstalledWorkspaceConfig(t, workdir)
		require.Empty(t, cfg.Modules)
	})
}

// TestWorkspaceModuleInit should cover workspace-oriented module init flows.
func (WorkspaceModulesSuite) TestWorkspaceModuleInit(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := workspaceBase(t, c)

	t.Run("initialized workspace creates a config-owned module", func(ctx context.Context, t *testctx.T) {
		ctr := initWorkspaceDangModule(ctx, t, base.With(daggerExec("workspace", "init")), "mymod").
			WithNewFile(".dagger/modules/mymod/main.dang", `
type Mymod {
  pub greet: String! {
    "hello workspace"
  }
}
`)

		djson, err := ctr.WithExec([]string{"cat", ".dagger/modules/mymod/dagger.json"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, djson, `"name": "mymod"`)

		cfg, err := ctr.WithExec([]string{"cat", ".dagger/config.toml"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, cfg, `source = "modules/mymod"`)

		_, err = ctr.WithExec([]string{"test", "!", "-f", ".dagger/modules/mymod/LICENSE"}).Sync(ctx)
		require.NoError(t, err)

		_, err = ctr.WithExec([]string{"test", "!", "-f", "dagger.json"}).Sync(ctx)
		require.NoError(t, err)
	})

	t.Run("workspace root loads config-owned modules", func(ctx context.Context, t *testctx.T) {
		ctr := initWorkspaceDangModule(ctx, t, base.With(daggerExec("workspace", "init")), "mymod").
			WithNewFile(".dagger/modules/mymod/main.dang", `
type Mymod {
  pub greet: String! {
    "hello workspace"
  }
}
`)

		out, err := ctr.With(daggerFunctions()).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "mymod")

		out, err = ctr.With(daggerCall("mymod", "greet")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello workspace", strings.TrimSpace(out))
	})

	t.Run("explicit path keeps standalone init inside a workspace", func(ctx context.Context, t *testctx.T) {
		initCtr := base.
			With(daggerExec("workspace", "init")).
			WithExec([]string{"dagger", "module", "init", "--sdk=dang", "--name=standalone", "./submod"}, dagger.ContainerWithExecOpts{
				Expect:                        dagger.ReturnTypeAny,
				ExperimentalPrivilegedNesting: true,
			})

		initCode, err := initCtr.ExitCode(ctx)
		require.NoError(t, err)
		initOut, err := initCtr.Stdout(ctx)
		require.NoError(t, err)
		initErr, err := initCtr.Stderr(ctx)
		require.NoError(t, err)
		require.Zero(t, initCode, "stdout:\n%s\nstderr:\n%s", initOut, initErr)
		require.NotContains(t, initErr, "failed to receive stat message")
		require.NotContains(t, initErr, "failed to get content hash")

		ctr := initCtr.
			WithNewFile("submod/main.dang", `
type Standalone {
  pub greet: String! {
    "hello standalone"
  }
}
`)

		djson, err := ctr.WithExec([]string{"cat", "submod/dagger.json"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, djson, `"name": "standalone"`)

		cfg, err := ctr.WithExec([]string{"cat", ".dagger/config.toml"}).Stdout(ctx)
		require.NoError(t, err)
		require.NotContains(t, cfg, "standalone")

		_, err = ctr.WithExec([]string{"test", "!", "-f", ".dagger/modules/standalone/dagger.json"}).Sync(ctx)
		require.NoError(t, err)

		out, err := ctr.With(daggerCallAt("./submod", "greet")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello standalone", strings.TrimSpace(out))
	})
}

// TestModuleScopedDependencyCommands covers module-specific dependency changes
// made from inside a workspace without mutating workspace config.
func (WorkspaceModulesSuite) TestModuleScopedDependencyCommands(ctx context.Context, t *testctx.T) {
	t.Run("module install writes the target module config, not workspace config", func(ctx context.Context, t *testctx.T) {
		workdir := t.TempDir()
		appDir := filepath.Join(workdir, "app")
		depDir := filepath.Join(workdir, "dep")

		require.NoError(t, os.MkdirAll(appDir, 0o755))
		require.NoError(t, os.MkdirAll(depDir, 0o755))
		initGitRepo(ctx, t, workdir)

		_, err := hostDaggerExecRaw(ctx, t, workdir, "--silent", "workspace", "init")
		require.NoError(t, err)
		_, err = hostDaggerModuleExec(ctx, t, appDir, "init", "--name=app", "--sdk=go", ".")
		require.NoError(t, err)
		_, err = hostDaggerModuleExec(ctx, t, depDir, "init", "--name=dep", "--sdk=go", ".")
		require.NoError(t, err)

		out, err := hostDaggerExecRaw(ctx, t, workdir, "--silent", "module", "install", "--mod=./app", "./dep")
		require.NoError(t, err)
		require.Equal(t, `Installed module dependency "dep"`, strings.TrimSpace(string(out)))

		moduleConfig, err := os.ReadFile(filepath.Join(appDir, workspacecfg.ModuleConfigFileName))
		require.NoError(t, err)
		require.Contains(t, string(moduleConfig), `"name": "dep"`)

		cfg := readInstalledWorkspaceConfig(t, workdir)
		require.Empty(t, cfg.Modules)
	})
}

// TestWorkspaceManagedModuleBehavior covers runtime behavior that depends on a
// module being configured in a workspace, but is not about entrypoint routing.
func (WorkspaceModulesSuite) TestWorkspaceManagedModuleBehavior(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	source := `
type Objects {
  pub objectA: ObjectsA! {
    ObjectsA()
  }
}

type ObjectsA {
  pub message: String! {
    "Hello from A"
  }

  pub objectB: ObjectsB! {
    ObjectsB()
  }
}

type ObjectsB {
  pub message: String! {
    "Hello from B"
  }
}
`

	t.Run("main object with prefixed children", func(ctx context.Context, t *testctx.T) {
		base := workspaceBase(t, c).
			With(initDangModule("objects", source))

		out, err := base.With(daggerCall("objects", "object-a", "message")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "Hello from A", strings.TrimSpace(out))

		out, err = base.With(daggerCall("objects", "object-a", "object-b", "message")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "Hello from B", strings.TrimSpace(out))
	})

	t.Run("renamed workspace-installed module", func(ctx context.Context, t *testctx.T) {
		base := workspaceBase(t, c).
			With(initDangModule("hello-world", `
type HelloWorld {
  pub greet(name: String! = "world"): String! {
    "hello, " + name + "!"
  }
}
`)).
			With(daggerExec("workspace", "config", "modules.hello-world.source", "modules/hello-world")).
			With(daggerExec("workspace", "config", "modules.hello-world.entrypoint", "false")).
			With(daggerExec("workspace", "config", "modules.greeter.source", "modules/hello-world"))

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
}

func readInstalledWorkspaceConfig(t *testctx.T, workdir string) *workspacecfg.Config {
	t.Helper()

	configBytes, err := os.ReadFile(filepath.Join(workdir, workspacecfg.LockDirName, workspacecfg.ConfigFileName))
	require.NoError(t, err)

	cfg, err := workspacecfg.ParseConfig(configBytes)
	require.NoError(t, err)
	return cfg
}
