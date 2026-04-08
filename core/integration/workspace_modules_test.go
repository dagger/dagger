package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	t.Run("install initializes a workspace when needed", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement workspace module install-init coverage.

Move the current coverage for installing into a repo with no existing workspace
config into this file.`)
	})

	t.Run("local installs are rewritten relative to .dagger", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement relative local module install coverage.

Move the current coverage that rewrites local module refs relative to .dagger
into this file.`)
	})

	t.Run("absolute local installs preserve absolute source paths", func(ctx context.Context, t *testctx.T) {
		workdir := t.TempDir()
		depDir := t.TempDir()

		initGitRepo(ctx, t, workdir)
		initGitRepo(ctx, t, depDir)

		_, err := hostDaggerExec(ctx, t, depDir, "module", "init", "--name=dep", "--sdk=go")
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

	t.Run("install rejects non-module refs without corrupting config", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement non-module install rejection coverage.

Move the current coverage that rejects non-module directories without writing
[modules.] or otherwise corrupting the workspace config into this file.`)
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
		t.Fatal(`FIXME: implement workspace module collision coverage.

Move the current duplicate-name rejection coverage into this file.`)
	})

	t.Run("local dependency updates are rejected when unsupported", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement workspace module update rejection coverage.

Move the current module update rejection coverage for local dependencies into
this file.`)
	})
}

// TestWorkspaceModuleInit should cover workspace-oriented module init flows.
func (WorkspaceModulesSuite) TestWorkspaceModuleInit(ctx context.Context, t *testctx.T) {
	t.Fatal(`FIXME: implement workspace module init coverage.

Move the current workspace_module_init_test.go coverage into this file once the
desired module-init UX for workspaces is locked.`)
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
