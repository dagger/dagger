package core

// Workspace alignment: aligned structurally, but coverage is still incomplete.
// Scope: Workspace config read or write behavior, config aliasing, boundary handling, and runtime effects on loaded modules.
// Intent: Keep current workspace configuration behavior explicit, including [modules.<name>.settings], and finish the missing API and boundary cases.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
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

func writeWorkspaceConfigFile(t *testctx.T, workdir, configTOML string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Join(workdir, workspace.LockDirName), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(workdir, workspace.LockDirName, workspace.ConfigFileName),
		[]byte(configTOML),
		0o644,
	))
}

func newWorkspaceConfigWorkdir(ctx context.Context, t *testctx.T, configTOML string) string {
	t.Helper()

	workdir := t.TempDir()
	initGitRepo(ctx, t, workdir)
	writeWorkspaceConfigFile(t, workdir, configTOML)
	return workdir
}

func (WorkspaceSuite) TestWorkspaceConfigRead(ctx context.Context, t *testctx.T) {
	workdir := newWorkspaceConfigWorkdir(ctx, t, `[modules.greeter]
source = "modules/greeter"
entrypoint = true

[modules.greeter.settings]
greeting = "hello"

[modules.wolfi]
source = "github.com/dagger/dagger/modules/wolfi"
`)

	t.Run("full config", func(ctx context.Context, t *testctx.T) {
		out, err := hostDaggerExec(ctx, t, workdir, "--silent", "workspace", "config")
		require.NoError(t, err)
		require.Contains(t, string(out), `source = "modules/greeter"`)
		require.Contains(t, string(out), "entrypoint = true")
		require.Contains(t, string(out), `source = "github.com/dagger/dagger/modules/wolfi"`)
	})

	t.Run("scalar value", func(ctx context.Context, t *testctx.T) {
		out, err := hostDaggerExec(ctx, t, workdir, "--silent", "workspace", "config", "modules.greeter.source")
		require.NoError(t, err)
		require.Equal(t, "modules/greeter", strings.TrimSpace(string(out)))
	})

	t.Run("table value", func(ctx context.Context, t *testctx.T) {
		out, err := hostDaggerExec(ctx, t, workdir, "--silent", "workspace", "config", "modules.greeter")
		require.NoError(t, err)
		require.Contains(t, string(out), `source = "modules/greeter"`)
		require.Contains(t, string(out), "entrypoint = true")
	})

	t.Run("missing key", func(ctx context.Context, t *testctx.T) {
		_, err := hostDaggerExec(ctx, t, workdir, "--silent", "workspace", "config", "modules.missing.source")
		require.Error(t, err)
		requireErrOut(t, err, `key "modules.missing.source" is not set`)
	})
}

func (WorkspaceSuite) TestWorkspaceConfigWrite(ctx context.Context, t *testctx.T) {
	t.Run("writes string and bool values", func(ctx context.Context, t *testctx.T) {
		workdir := newWorkspaceConfigWorkdir(ctx, t, `[modules.greeter]
source = "modules/greeter"
entrypoint = true
`)

		_, err := hostDaggerExec(ctx, t, workdir, "--silent", "workspace", "config", "modules.greeter.source", "github.com/acme/greeter")
		require.NoError(t, err)
		_, err = hostDaggerExec(ctx, t, workdir, "--silent", "workspace", "config", "modules.greeter.entrypoint", "false")
		require.NoError(t, err)

		out, err := hostDaggerExec(ctx, t, workdir, "--silent", "workspace", "config", "modules.greeter.source")
		require.NoError(t, err)
		require.Equal(t, "github.com/acme/greeter", strings.TrimSpace(string(out)))

		out, err = hostDaggerExec(ctx, t, workdir, "--silent", "workspace", "config", "modules.greeter.entrypoint")
		require.NoError(t, err)
		require.Equal(t, "false", strings.TrimSpace(string(out)))
	})

	t.Run("writes array values", func(ctx context.Context, t *testctx.T) {
		workdir := newWorkspaceConfigWorkdir(ctx, t, `[modules.greeter]
source = "modules/greeter"
entrypoint = true
`)

		_, err := hostDaggerExec(ctx, t, workdir, "--silent", "workspace", "config", "modules.greeter.settings.tags", "main, develop")
		require.NoError(t, err)

		configContents, err := os.ReadFile(filepath.Join(workdir, workspace.LockDirName, workspace.ConfigFileName))
		require.NoError(t, err)
		require.Contains(t, string(configContents), "tags")
		require.Contains(t, string(configContents), "main")
		require.Contains(t, string(configContents), "develop")
	})

	t.Run("rejects invalid keys", func(ctx context.Context, t *testctx.T) {
		workdir := newWorkspaceConfigWorkdir(ctx, t, `[modules.greeter]
source = "modules/greeter"
entrypoint = true
`)

		_, err := hostDaggerExec(ctx, t, workdir, "--silent", "workspace", "config", "modules.greeter.badfield", "value")
		require.Error(t, err)
		requireErrOut(t, err, "unknown config key")
	})
}

func (WorkspaceSuite) TestWorkspaceModuleSettingsRuntime(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	newConfiguredCtr := func(configTOML string) *dagger.Container {
		t.Helper()
		return nestedDaggerContainer(t, c, "go", "defaults/superconstructor").
			WithNewFile(".dagger/config.toml", configTOML).
			WithNewFile("/foo/hello.txt", "hello there!").
			WithEnvVariable("PASSWORD", "topsecret").
			WithServiceBinding("www", c.Container().From("nginx").AsService())
	}

	t.Run("workspace module settings drive constructor help and runtime", func(ctx context.Context, t *testctx.T) {
		ctr := newConfiguredCtr(`[modules.superconstructor]
source = "defaults/superconstructor"
entrypoint = true

[modules.superconstructor.settings]
Count = 7
Greeting = "yay"
dir = "/foo"
file = "/foo/hello.txt"
password = "env://PASSWORD"
service = "tcp://www:80"
`)

		out, err := ctr.WithExec([]string{"dagger", "call", "--help"}, nestedExec).Stdout(ctx)
		out = trimDaggerFunctionUsageText(out)
		require.NoError(t, err)
		require.Regexp(t, `(?m)--count int *\(default 7\)\s*$`, out)
		require.Regexp(t, `(?m)--greeting string *\(default "yay"\)\s*$`, out)

		out, err = ctr.WithExec([]string{"dagger", "call", "greeting"}, nestedExec).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "yay", out)

		out, err = ctr.WithExec([]string{"dagger", "call", "greeting"}, nestedExec).CombinedOutput(ctx)
		require.NoError(t, err)
		require.NotContains(t, out, "user default:")
		require.Contains(t, out, "yay")

		out, err = ctr.WithExec([]string{"dagger", "call", "count"}, nestedExec).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "7", out)

		out, err = ctr.WithExec([]string{"dagger", "call", "--greeting=bonjour", "greeting"}, nestedExec).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "bonjour", out)

		out, err = ctr.WithExec([]string{"dagger", "call", "file", "contents"}, nestedExec).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello there!", out)

		out, err = ctr.WithExec([]string{"dagger", "call", "dir", "entries"}, nestedExec).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello.txt\n", out)
	})

	t.Run("native workspaces do not fall back to .env", func(ctx context.Context, t *testctx.T) {
		ctr := newConfiguredCtr(`[modules.superconstructor]
source = "defaults/superconstructor"
entrypoint = true

[modules.superconstructor.settings]
count = 7
dir = "/foo"
file = "/foo/hello.txt"
password = "env://PASSWORD"
service = "tcp://www:80"
`).WithNewFile(".env", "SUPERCONSTRUCTOR_greeting=from-env")

		stderr, err := ctr.WithExec([]string{"dagger", "call", "greeting"}, dagger.ContainerWithExecOpts{
			Expect:                        dagger.ReturnTypeFailure,
			ExperimentalPrivilegedNesting: true,
		}).Stderr(ctx)
		require.NoError(t, err)
		require.Contains(t, stderr, "required")
		require.Contains(t, stderr, "greeting")
	})
}

func (WorkspaceSuite) TestConfigAlias(ctx context.Context, t *testctx.T) {
	workdir := newWorkspaceConfigWorkdir(ctx, t, `[modules.greeter]
source = "modules/greeter"
entrypoint = true
`)

	out, err := hostDaggerExec(ctx, t, workdir, "--silent", "config", "modules.greeter.source")
	require.NoError(t, err)
	require.Equal(t, "modules/greeter", strings.TrimSpace(string(out)))

	_, err = hostDaggerExec(ctx, t, workdir, "--silent", "config", "modules.greeter.entrypoint", "false")
	require.NoError(t, err)

	out, err = hostDaggerExec(ctx, t, workdir, "--silent", "workspace", "config", "modules.greeter.entrypoint")
	require.NoError(t, err)
	require.Equal(t, "false", strings.TrimSpace(string(out)))
}

func (WorkspaceSuite) TestWorkspaceConfigRequiresInit(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	initGitRepo(ctx, t, workdir)

	_, err := hostDaggerExec(ctx, t, workdir, "--silent", "workspace", "config")
	require.Error(t, err)
	requireErrOut(t, err, "no config.toml found in workspace")
}

func (WorkspaceSuite) TestCurrentWorkspaceConfigBoundary(ctx context.Context, t *testctx.T) {
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

		out, err := hostDaggerExecRaw(ctx, t, nestedDir, "--silent", "init")
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

func (WorkspaceSuite) TestWorkspaceModuleSettingsPolicy(ctx context.Context, t *testctx.T) {
	t.Run("unknown constructor settings keys have an explicit policy", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: decide whether unknown keys in [modules.<name>.settings] are ignored or rejected at load time.`)
	})
}

// TestWorkspaceModuleSettingsSemantics is the planning scaffold for the
// unresolved behavior in [modules.<name>.settings] beyond the happy-path runtime
// coverage above.
func (WorkspaceSuite) TestWorkspaceModuleSettingsSemantics(ctx context.Context, t *testctx.T) {
	t.Run("settings key normalization and casing are explicit", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement workspace module settings key-normalization coverage.

Use constructor arguments whose runtime names differ in case or word shape
(for example camelCase, snake_case, and acronym-heavy names). Verify one
explicit TOML key-mapping policy and make the accepted forms part of the
contract.`)
	})

	t.Run("typed settings values validate and coerce predictably", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement typed workspace module settings coverage.

Cover scalar and path-like constructor arguments supplied through
[modules.<name>.settings]. Verify successful coercion for supported types and a
clear validation error for unsupported or malformed values.`)
	})

	t.Run("explicit args override only the fields they replace", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement partial-override coverage for workspace module settings.

Seed several constructor values from [modules.<name>.settings], then override one
or two at call time. Verify only the explicitly provided fields change and the
remaining constructor inputs still come from workspace config.`)
	})

	t.Run("module settings are scoped per loaded module", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement sibling-module isolation coverage for workspace module settings.

Configure two loaded modules with overlapping constructor argument names.
Verify each module reads only its own [modules.<name>.settings] table and one
module's settings cannot leak into another.`)
	})

	t.Run("broken env, file, directory, and service references fail clearly", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement invalid reference coverage for workspace module settings.

Point settings-driven constructor values at missing env vars, nonexistent files
or directories, and invalid service addresses. Verify the failure points at the
specific settings key and reference that could not be resolved.`)
	})
}

// TestWorkspaceConfigurationLifecycle is the planning scaffold for the full
// scope this file should eventually own: initializing, editing, detecting, and
// applying .dagger/config.toml. Module management, compat, and migration
// belong in their own files.
func (WorkspaceSuite) TestWorkspaceConfigurationLifecycle(ctx context.Context, t *testctx.T) {
	t.Run("CurrentWorkspace.Init creates config for the repo", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement CurrentWorkspace.Init API coverage.

Exercise the API form of workspace initialization and verify it creates the
expected .dagger/config.toml rooted at the current repo.`)
	})

	t.Run("workspace config detects the nearest initialized boundary", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement workspace config boundary detection coverage.

Invoke workspace config commands from nested directories and verify they use
the nearest .dagger/config.toml boundary rather than the current directory.`)
	})
}
