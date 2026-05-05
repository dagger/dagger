package core

// Scope: Explicit workspace selection and binding via --workspace/-W, including declared local or remote refs, --workdir interaction, command policy, metadata-only commands, and explicit env overlays.
// Intent: Own the declared-workspace contract end to end so contextual inference, compat opt-in, and native loading arbitration can evolve independently.

import (
	"context"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// WorkspaceSelectionSuite owns the explicit workspace-selection contract:
// how a declared workspace is chosen, which commands accept it, and how that
// binding propagates through the session once selected.
type WorkspaceSelectionSuite struct{}

func TestWorkspaceSelection(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(WorkspaceSelectionSuite{})
}

func workspaceSelectionDaggerExec(args ...string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.WithExec(append([]string{"dagger", "--progress=report"}, args...), dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		})
	}
}

func workspaceSelectionDaggerExecFail(args ...string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.WithExec(append([]string{"dagger", "--progress=report"}, args...), dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
			Expect:                        dagger.ReturnTypeFailure,
		})
	}
}

func workspaceSelectionDaggerCall(args ...string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.WithExec(append([]string{"dagger", "--progress=report", "call"}, args...), dagger.ContainerWithExecOpts{
			UseEntrypoint:                 true,
			ExperimentalPrivilegedNesting: true,
		})
	}
}

func workspaceSelectionDaggerCallFail(args ...string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.WithExec(append([]string{"dagger", "--progress=report", "call"}, args...), dagger.ContainerWithExecOpts{
			UseEntrypoint:                 true,
			ExperimentalPrivilegedNesting: true,
			Expect:                        dagger.ReturnTypeFailure,
		})
	}
}

func workspaceSelectionDaggerQuery(query string, args ...string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.WithExec(append([]string{"dagger", "--progress=report", "query"}, args...), dagger.ContainerWithExecOpts{
			Stdin:                         query,
			ExperimentalPrivilegedNesting: true,
		})
	}
}

func workspaceSelectionDaggerQueryFail(query string, args ...string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.WithExec(append([]string{"dagger", "--progress=report", "query"}, args...), dagger.ContainerWithExecOpts{
			Stdin:                         query,
			ExperimentalPrivilegedNesting: true,
			Expect:                        dagger.ReturnTypeFailure,
		})
	}
}

func workspaceSelectionDangSource(typeName, fnName, result string) string {
	return `
type ` + typeName + ` {
  pub ` + fnName + `: String! {
    "` + result + `"
  }
}
`
}

func workspaceSelectionSimpleWorkspace(dir, name, typeName, result string) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		moduleDir := dir + "/.dagger/modules/" + name
		return ctr.
			WithNewFile(dir+"/.dagger/config.toml", `[modules.`+name+`]
source = "modules/`+name+`"
entrypoint = true
`).
			WithNewFile(moduleDir+"/dagger.json", `{"name":"`+name+`","sdk":{"source":"dang"}}`).
			WithNewFile(moduleDir+"/main.dang", workspaceSelectionDangSource(typeName, "identify", result))
	}
}

func workspaceSelectionSimpleWorkspaceDir(c *dagger.Client, name, typeName, result string) *dagger.Directory {
	moduleDir := ".dagger/modules/" + name
	return c.Directory().
		WithNewFile(".dagger/config.toml", `[modules.`+name+`]
source = "modules/`+name+`"
entrypoint = true
`).
		WithNewFile(moduleDir+"/dagger.json", `{"name":"`+name+`","sdk":{"source":"dang"}}`).
		WithNewFile(moduleDir+"/main.dang", workspaceSelectionDangSource(typeName, "identify", result))
}

func workspaceSelectionEnvWorkspace(dir, base, ci string) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		moduleDir := dir + "/.dagger/modules/greeter"
		return ctr.
			WithNewFile(dir+"/.dagger/config.toml", `[modules.greeter]
source = "modules/greeter"
entrypoint = true

[modules.greeter.settings]
greeting = "`+base+`"

[env.ci.modules.greeter.settings]
greeting = "`+ci+`"
`).
			WithNewFile(moduleDir+"/dagger.json", `{"name":"greeter","sdk":{"source":"dang"}}`).
			WithNewFile(moduleDir+"/main.dang", `
type Greeter {
  pub greeting: String!

  new(greeting: String! = "default") {
    self.greeting = greeting
    self
  }
}
`)
	}
}

func workspaceSelectionRemoteRef(ctx context.Context, t *testctx.T, c *dagger.Client, content *dagger.Directory) string {
	t.Helper()

	gitSrv, _ := gitSmartHTTPServiceDirAuth(ctx, t, c, "", makeGitDir(c, content, "main"), "", nil)
	gitSrv, err := gitSrv.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = gitSrv.Stop(ctx) })

	shortHost, err := gitSrv.Hostname(ctx)
	require.NoError(t, err)

	getentOut, err := c.Container().From(alpineImage).
		WithExec([]string{"getent", "hosts", shortHost}).
		Stdout(ctx)
	require.NoError(t, err, "could not resolve git service hostname %q", shortHost)

	fields := strings.Fields(getentOut)
	require.NotEmpty(t, fields, "unexpected getent output: %q", getentOut)
	return "http://" + fields[0] + "/repo.git@main"
}

func workspaceSelectionNestedModuleSource() string {
	return `package main

import (
	"context"
	"strings"

	"dagger/nester/internal/dagger"
)

func New(greeting string) *Nester {
	return &Nester{Message: greeting}
}

type Nester struct {
	Message string
}

func (m *Nester) Greeting() string {
	return m.Message
}

func (m *Nester) NestedWorkspace(ctx context.Context, cli *dagger.File) (string, error) {
	return m.nested(ctx, cli, "query", "{currentWorkspace{path configPath}}")
}

func (m *Nester) NestedGreeting(ctx context.Context, cli *dagger.File) (string, error) {
	return m.nested(ctx, cli, "call", "greeting")
}

func (m *Nester) nested(ctx context.Context, cli *dagger.File, args ...string) (string, error) {
	execArgs := append([]string{"dagger", "--progress=report"}, args...)
	out, err := dag.Container().
		From("` + alpineImage + `").
		WithMountedFile("/bin/dagger", cli).
		WithExec([]string{"mkdir", "-p", "/empty"}).
		WithWorkdir("/empty").
		WithExec(execArgs, dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		}).
		Stdout(ctx)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}
`
}

// TestDeclaredWorkspaceSelection should pin down the main user-visible
// selection contract for --workspace/-W before any compat or ambient find-up
// behavior is involved.
func (WorkspaceSelectionSuite) TestDeclaredWorkspaceSelection(ctx context.Context, t *testctx.T) {
	t.Run("local -W selects that workspace instead of cwd inference", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := workspaceBase(t, c).
			With(workspaceSelectionSimpleWorkspace("/work/caller", "caller", "Caller", "caller workspace")).
			With(workspaceSelectionSimpleWorkspace("/work/selected", "selected", "Selected", "selected workspace")).
			WithWorkdir("/work/caller")

		out, err := ctr.With(workspaceSelectionDaggerCall("-W", "../selected", "identify")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "selected workspace", strings.TrimSpace(out))

		out, err = ctr.With(workspaceSelectionDaggerQuery(`{currentWorkspace{path configPath}}`, "-W", "../selected")).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"currentWorkspace":{"path":"selected","configPath":"selected/.dagger/config.toml"}}`, out)

		out, err = ctr.With(workspaceSelectionDaggerCall("-W", "../selected", "selected", "identify")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "selected workspace", strings.TrimSpace(out))
	})

	t.Run("remote -W selects a git workspace without relying on host cwd", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		remoteRef := workspaceSelectionRemoteRef(ctx, t, c, workspaceSelectionSimpleWorkspaceDir(c, "remote", "Remote", "remote workspace"))

		ctr := c.Container().From(alpineImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/empty")

		out, err := ctr.With(workspaceSelectionDaggerCall("-W", remoteRef, "identify")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "remote workspace", strings.TrimSpace(out))

		out, err = ctr.With(workspaceSelectionDaggerExec("-W", remoteRef, "workspace", "info")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "Address: "+remoteRef)
		require.Contains(t, out, "Path:    .")
		require.Contains(t, out, "Config:  .dagger/config.toml")
	})

	t.Run("relative -W is resolved after --workdir changes cwd", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := workspaceBase(t, c).
			With(workspaceSelectionSimpleWorkspace("/work/shell/ws", "selected", "Selected", "post-workdir workspace")).
			With(workspaceSelectionSimpleWorkspace("/work/ws", "wrong", "Wrong", "original cwd workspace")).
			WithWorkdir("/work")

		out, err := ctr.With(workspaceSelectionDaggerCall("--workdir", "/work/shell", "-W", "./ws", "identify")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "post-workdir workspace", strings.TrimSpace(out))

		out, err = ctr.With(workspaceSelectionDaggerQuery(`{currentWorkspace{path configPath}}`, "--workdir", "/work/shell", "-W", "./ws")).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"currentWorkspace":{"path":"shell/ws","configPath":"shell/ws/.dagger/config.toml"}}`, out)
	})

	t.Run("declared workspace wins over ambient workspace and cwd module nomination", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := workspaceBase(t, c).
			With(workspaceSelectionSimpleWorkspace("/work/caller", "caller", "Caller", "ambient workspace")).
			With(workspaceSelectionSimpleWorkspace("/work/selected", "selected", "Selected", "declared workspace")).
			WithNewFile("/work/caller/nested/dagger.json", `{"name":"nested","sdk":{"source":"dang"}}`).
			WithNewFile("/work/caller/nested/main.dang", workspaceSelectionDangSource("Nested", "identify", "cwd module")).
			WithWorkdir("/work/caller/nested")

		out, err := ctr.With(workspaceSelectionDaggerCall("-W", "../../selected", "identify")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "declared workspace", strings.TrimSpace(out))

		out, err = ctr.With(workspaceSelectionDaggerQuery(`{currentWorkspace{path configPath}}`, "-W", "../../selected")).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"currentWorkspace":{"path":"selected","configPath":"selected/.dagger/config.toml"}}`, out)
	})
}

// TestWorkspaceSelectionCommandPolicy should pin down which commands accept
// --workspace and where local-only restrictions are enforced.
func (WorkspaceSelectionSuite) TestWorkspaceSelectionCommandPolicy(ctx context.Context, t *testctx.T) {
	t.Run("module-centric commands reject -W in integration", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := workspaceBase(t, c)

		out, err := ctr.With(workspaceSelectionDaggerExecFail("-W", ".", "module", "develop")).CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, `--workspace is not supported for "dagger module develop"`)

		out, err = ctr.With(workspaceSelectionDaggerExecFail("-W", ".", "migrate")).CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, `--workspace is not supported for "dagger migrate"`)
	})

	t.Run("local-only workspace mutations accept a local selected workspace", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := workspaceBase(t, c).
			WithExec([]string{"mkdir", "-p", "/work/caller", "/work/selected"}).
			WithWorkdir("/work/caller").
			With(workspaceSelectionDaggerExec("-W", "../selected", "workspace", "init"))

		_, err := ctr.WithExec([]string{"test", "-f", "/work/selected/.dagger/config.toml"}).Sync(ctx)
		require.NoError(t, err)
		_, err = ctr.WithExec([]string{"test", "!", "-e", "/work/caller/.dagger/config.toml"}).Sync(ctx)
		require.NoError(t, err)
	})

	t.Run("local-only workspace mutations reject a remote selected workspace at execution time", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		remoteRef := workspaceSelectionRemoteRef(ctx, t, c, workspaceSelectionSimpleWorkspaceDir(c, "remote", "Remote", "remote workspace"))

		out, err := c.Container().From(alpineImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/empty").
			With(workspaceSelectionDaggerQueryFail(`{currentWorkspace{init}}`, "-W", remoteRef)).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "workspace init is local-only")
		require.NotContains(t, out, "--workspace must be a local path")
	})
}

// TestSelectedWorkspaceMetadataCommands should own commands whose purpose is to
// inspect the selected workspace rather than to run one of its modules.
func (WorkspaceSelectionSuite) TestSelectedWorkspaceMetadataCommands(ctx context.Context, t *testctx.T) {
	t.Run("workspace info reports the selected local workspace", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := workspaceBase(t, c).
			With(workspaceSelectionSimpleWorkspace("/work/caller", "caller", "Caller", "caller workspace")).
			With(workspaceSelectionSimpleWorkspace("/work/selected", "selected", "Selected", "selected workspace")).
			WithWorkdir("/work/caller")

		out, err := ctr.With(workspaceSelectionDaggerExec("-W", "../selected", "workspace", "info")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "Address: file:///work/selected")
		require.Contains(t, out, "Path:    selected")
		require.Contains(t, out, "Config:  selected/.dagger/config.toml")
	})

	t.Run("workspace info reports the selected remote workspace", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		remoteRef := workspaceSelectionRemoteRef(ctx, t, c, workspaceSelectionSimpleWorkspaceDir(c, "remote", "Remote", "remote workspace"))

		out, err := c.Container().From(alpineImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/empty").
			With(workspaceSelectionDaggerExec("-W", remoteRef, "workspace", "info")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "Address: "+remoteRef)
		require.Contains(t, out, "Path:    .")
		require.Contains(t, out, "Config:  .dagger/config.toml")
	})

	t.Run("workspace list uses the selected workspace instead of cwd", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := workspaceBase(t, c).
			With(workspaceSelectionSimpleWorkspace("/work/caller", "caller", "Caller", "caller workspace")).
			With(workspaceSelectionSimpleWorkspace("/work/selected", "selected", "Selected", "selected workspace")).
			WithWorkdir("/work/caller")

		out, err := ctr.With(workspaceSelectionDaggerExec("-W", "../selected", "workspace", "list")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "selected*")
		require.Contains(t, out, ".dagger/modules/selected")
		require.NotContains(t, out, "caller*")
		require.NotContains(t, out, ".dagger/modules/caller")
	})
}

// TestSelectedWorkspaceEnvOverlay should cover the end-to-end interaction
// between declared workspace selection and --env.
func (WorkspaceSelectionSuite) TestSelectedWorkspaceEnvOverlay(ctx context.Context, t *testctx.T) {
	t.Run("env overlay applies to the explicitly selected workspace", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := workspaceBase(t, c).
			With(workspaceSelectionEnvWorkspace("/work/caller", "caller-base", "caller-ci")).
			With(workspaceSelectionEnvWorkspace("/work/selected", "selected-base", "selected-ci")).
			WithWorkdir("/work/caller")

		out, err := ctr.With(workspaceSelectionDaggerCall("-W", "../selected", "--env", "ci", "greeting")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "selected-ci", strings.TrimSpace(out))
	})

	t.Run("undefined env name fails against the selected workspace", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := workspaceBase(t, c).
			With(workspaceSelectionEnvWorkspace("/work/caller", "caller-base", "caller-ci")).
			With(workspaceSelectionEnvWorkspace("/work/selected", "selected-base", "selected-ci")).
			WithWorkdir("/work/caller")

		out, err := ctr.With(workspaceSelectionDaggerCallFail("-W", "../selected", "--env", "missing", "greeting")).CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, `workspace env "missing" is not defined`)
	})

	t.Run("env overlay does not work for selections without native workspace config", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := workspaceBase(t, c).
			WithExec([]string{"mkdir", "-p", "/work/caller", "/work/bare"}).
			WithWorkdir("/work/caller")

		out, err := ctr.With(workspaceSelectionDaggerCallFail("-W", "../bare", "--env", "ci", "identify")).CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, `workspace env "ci" requires .dagger/config.toml`)
	})
}

// TestDeclaredWorkspaceBindingPropagation should pin down how an explicit
// workspace binding survives once a session is established and other clients
// are created from it.
func (WorkspaceSelectionSuite) TestDeclaredWorkspaceBindingPropagation(ctx context.Context, t *testctx.T) {
	t.Run("nested clients inherit the declared workspace binding", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := workspaceBase(t, c).
			WithExec([]string{"mkdir", "-p", "/work/caller", "/work/selected"}).
			With(workspaceSelectionEnvWorkspace("/work/ambient", "ambient-base", "ambient-ci")).
			WithWorkdir("/work/selected").
			With(workspaceSelectionDaggerExec("workspace", "init")).
			With(workspaceSelectionDaggerExec("module", "init", "--sdk=go", "--name=nester")).
			WithNewFile("/work/selected/.dagger/modules/nester/main.go", workspaceSelectionNestedModuleSource()).
			WithNewFile("/work/selected/.dagger/config.toml", `[modules.nester]
source = "modules/nester"
entrypoint = true

[modules.nester.settings]
greeting = "selected-base"

[env.ci.modules.nester.settings]
greeting = "selected-ci"
`).
			WithWorkdir("/work/caller")

		out, err := ctr.With(workspaceSelectionDaggerCall("-W", "../selected", "nested-workspace", "--cli", testCLIBinPath)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"currentWorkspace":{"path":"selected","configPath":"selected/.dagger/config.toml"}}`, out)
	})

	t.Run("nested clients inherit the declared workspace env overlay", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := workspaceBase(t, c).
			WithExec([]string{"mkdir", "-p", "/work/caller", "/work/selected"}).
			With(workspaceSelectionEnvWorkspace("/work/ambient", "ambient-base", "ambient-ci")).
			WithWorkdir("/work/selected").
			With(workspaceSelectionDaggerExec("workspace", "init")).
			With(workspaceSelectionDaggerExec("module", "init", "--sdk=go", "--name=nester")).
			WithNewFile("/work/selected/.dagger/modules/nester/main.go", workspaceSelectionNestedModuleSource()).
			WithNewFile("/work/selected/.dagger/config.toml", `[modules.nester]
source = "modules/nester"
entrypoint = true

[modules.nester.settings]
greeting = "selected-base"

[env.ci.modules.nester.settings]
greeting = "selected-ci"
`).
			WithWorkdir("/work/caller")

		out, err := ctr.With(workspaceSelectionDaggerCall("-W", "../selected", "--env", "ci", "nested-greeting", "--cli", testCLIBinPath)).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "selected-ci", strings.TrimSpace(out))
	})
}
