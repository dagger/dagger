package core

// Scope: Workspace config read or write behavior, config aliasing, boundary handling, and runtime effects on loaded modules.
// Intent: Keep current workspace configuration behavior explicit, including [modules.<name>.settings].

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

func newWorkspaceModuleSettingsCtr(t *testctx.T, c *dagger.Client, configTOML string) *dagger.Container {
	t.Helper()
	return nestedDaggerContainer(t, c, "go", "defaults/superconstructor").
		WithNewFile(".dagger/config.toml", configTOML).
		WithNewFile("/foo/hello.txt", "hello there!").
		WithEnvVariable("PASSWORD", "topsecret").
		WithServiceBinding("www", c.Container().From("nginx").AsService())
}

func (WorkspaceSuite) TestWorkspaceModuleSettingsRuntime(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("workspace module settings drive constructor help and runtime", func(ctx context.Context, t *testctx.T) {
		ctr := newWorkspaceModuleSettingsCtr(t, c, `[modules.superconstructor]
source = "../defaults/superconstructor"
entrypoint = true

[modules.superconstructor.settings]
Count = 7
Greeting = "yay"
dir = "/foo"
file = "/foo/hello.txt"
password = "env://PASSWORD"
service = "tcp://www:80"
`)

		out, err := ctr.WithExec([]string{"dagger", "--progress=report", "call", "--help"}, nestedExec).Stdout(ctx)
		out = trimDaggerFunctionUsageText(out)
		require.NoError(t, err)
		require.Regexp(t, `(?m)--count int *\(default 7\)\s*$`, out)
		require.Regexp(t, `(?m)--greeting string *\(default "yay"\)\s*$`, out)

		out, err = ctr.WithExec([]string{"dagger", "--progress=report", "call", "greeting"}, nestedExec).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "yay", out)

		out, err = ctr.WithExec([]string{"dagger", "--progress=report", "call", "greeting"}, nestedExec).CombinedOutput(ctx)
		require.NoError(t, err)
		require.NotContains(t, out, "user default:")
		require.Contains(t, out, "yay")

		out, err = ctr.WithExec([]string{"dagger", "--progress=report", "call", "count"}, nestedExec).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "7", out)

		out, err = ctr.WithExec([]string{"dagger", "--progress=report", "call", "--greeting=bonjour", "greeting"}, nestedExec).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "bonjour", out)

		out, err = ctr.WithExec([]string{"dagger", "--progress=report", "call", "file", "contents"}, nestedExec).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello there!", out)

		out, err = ctr.WithExec([]string{"dagger", "--progress=report", "call", "dir", "entries"}, nestedExec).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello.txt\n", out)
	})

	t.Run("native workspaces do not fall back to .env", func(ctx context.Context, t *testctx.T) {
		ctr := newWorkspaceModuleSettingsCtr(t, c, `[modules.superconstructor]
source = "../defaults/superconstructor"
entrypoint = true

[modules.superconstructor.settings]
count = 7
dir = "/foo"
file = "/foo/hello.txt"
password = "env://PASSWORD"
service = "tcp://www:80"
`).WithNewFile(".env", "SUPERCONSTRUCTOR_greeting=from-env")

		stderr, err := ctr.WithExec([]string{"dagger", "--progress=report", "call", "greeting"}, dagger.ContainerWithExecOpts{
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
		c := connect(ctx, t)

		ctr := newWorkspaceModuleSettingsCtr(t, c, `[modules.superconstructor]
source = "../defaults/superconstructor"
entrypoint = true

[modules.superconstructor.settings]
greeting = "configured"
count = 7
dir = "/foo"
file = "/foo/hello.txt"
password = "env://PASSWORD"
service = "tcp://www:80"
unknown = "ignored"
`)

		out, err := ctr.WithExec([]string{"dagger", "--progress=report", "call", "greeting"}, nestedExec).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "configured", out)
	})
}

func (WorkspaceSuite) TestWorkspaceModuleSettingsSemantics(ctx context.Context, t *testctx.T) {
	t.Run("settings key normalization and casing are explicit", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		ctr := newWorkspaceModuleSettingsCtr(t, c, `[modules.superconstructor]
source = "../defaults/superconstructor"
entrypoint = true

[modules.superconstructor.settings]
GREETING = "case-insensitive"
COUNT = 9
DIR = "/foo"
FILE = "/foo/hello.txt"
PASSWORD = "env://PASSWORD"
SERVICE = "tcp://www:80"
`)

		out, err := ctr.WithExec([]string{"dagger", "--progress=report", "call", "greeting"}, nestedExec).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "case-insensitive", out)

		out, err = ctr.WithExec([]string{"dagger", "--progress=report", "call", "count"}, nestedExec).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "9", out)
	})

	t.Run("typed settings values validate and coerce predictably", func(ctx context.Context, t *testctx.T) {
		workdir := newWorkspaceSettingsWorkdir(ctx, t, `[modules.vitest]
source = "../modules/vitest"

[modules.vitest.settings]
failFast = true
retries = 3
tags = ["smoke", "nightly"]
`, workspaceSettingsVitestModule("modules/vitest", "vitest"))

		out, err := hostDaggerExec(ctx, t, workdir, "--silent", "call", "vitest", "fail-fast")
		require.NoError(t, err)
		require.Equal(t, "true", strings.TrimSpace(string(out)))

		out, err = hostDaggerExec(ctx, t, workdir, "--silent", "call", "vitest", "retries")
		require.NoError(t, err)
		require.Equal(t, "3", strings.TrimSpace(string(out)))

		out, err = hostDaggerExec(ctx, t, workdir, "--silent", "call", "vitest", "tags")
		require.NoError(t, err)
		require.Contains(t, string(out), "smoke")
		require.Contains(t, string(out), "nightly")

		c := connect(ctx, t)
		ctr := newWorkspaceModuleSettingsCtr(t, c, `[modules.superconstructor]
source = "../defaults/superconstructor"
entrypoint = true

[modules.superconstructor.settings]
greeting = "bad count"
count = "not-an-int"
dir = "/foo"
file = "/foo/hello.txt"
password = "env://PASSWORD"
service = "tcp://www:80"
`)

		errOut, err := ctr.WithExec([]string{"dagger", "--progress=report", "call", "count"}, dagger.ContainerWithExecOpts{
			Expect:                        dagger.ReturnTypeFailure,
			ExperimentalPrivilegedNesting: true,
		}).CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, errOut, "count")
		require.Contains(t, errOut, "not-an-int")
	})

	t.Run("explicit args override only the fields they replace", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		ctr := newWorkspaceModuleSettingsCtr(t, c, `[modules.superconstructor]
source = "../defaults/superconstructor"
entrypoint = true

[modules.superconstructor.settings]
greeting = "configured"
count = 7
dir = "/foo"
file = "/foo/hello.txt"
password = "env://PASSWORD"
service = "tcp://www:80"
`)

		out, err := ctr.WithExec([]string{"dagger", "--progress=report", "call", "--greeting=override", "count"}, nestedExec).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "7", out)

		out, err = ctr.WithExec([]string{"dagger", "--progress=report", "call", "--count=11", "greeting"}, nestedExec).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "configured", out)
	})

	t.Run("module settings are scoped per loaded module", func(ctx context.Context, t *testctx.T) {
		workdir := newWorkspaceSettingsWorkdir(ctx, t, `[modules.alpha]
source = "../modules/alpha"

[modules.alpha.settings]
greeting = "hello alpha"

[modules.beta]
source = "../modules/beta"

[modules.beta.settings]
greeting = "hello beta"
`, workspaceSettingsModuleFixture{
			relDir: "modules/alpha",
			name:   "alpha",
			main: `package main

type Alpha struct {
	GreetingValue string
}

func New(greeting string) *Alpha {
	return &Alpha{GreetingValue: greeting}
}

func (m *Alpha) Greeting() string {
	return m.GreetingValue
}
`,
		}, workspaceSettingsModuleFixture{
			relDir: "modules/beta",
			name:   "beta",
			main: `package main

type Beta struct {
	GreetingValue string
}

func New(greeting string) *Beta {
	return &Beta{GreetingValue: greeting}
}

func (m *Beta) Greeting() string {
	return m.GreetingValue
}
`,
		})

		out, err := hostDaggerExec(ctx, t, workdir, "--silent", "call", "alpha", "greeting")
		require.NoError(t, err)
		require.Equal(t, "hello alpha", strings.TrimSpace(string(out)))

		out, err = hostDaggerExec(ctx, t, workdir, "--silent", "call", "beta", "greeting")
		require.NoError(t, err)
		require.Equal(t, "hello beta", strings.TrimSpace(string(out)))
	})

	t.Run("broken env, file, directory, and service references fail clearly", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		for _, tc := range []struct {
			name   string
			key    string
			value  string
			call   []string
			assert []string
		}{
			{name: "env", key: "password", value: "env://MISSING_PASSWORD", call: []string{"password", "plaintext"}, assert: []string{"password", "secret env var not found"}},
			{name: "file", key: "file", value: "/foo/missing.txt", call: []string{"file", "contents"}, assert: []string{"file", "missing.txt"}},
			{name: "directory", key: "dir", value: "/missing-dir", call: []string{"dir", "entries"}, assert: []string{"dir", "missing-dir"}},
			{name: "service", key: "service", value: "not-a-service-address", call: []string{"service", "endpoint"}, assert: []string{"service", "missing port in address"}},
		} {
			t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
				settings := map[string]string{
					"greeting": "configured",
					"count":    "7",
					"dir":      `"/foo"`,
					"file":     `"/foo/hello.txt"`,
					"password": `"env://PASSWORD"`,
					"service":  `"tcp://www:80"`,
				}
				settings[tc.key] = fmt.Sprintf("%q", tc.value)

				ctr := newWorkspaceModuleSettingsCtr(t, c, fmt.Sprintf(`[modules.superconstructor]
source = "../defaults/superconstructor"
entrypoint = true

[modules.superconstructor.settings]
greeting = %q
count = %s
dir = %s
file = %s
password = %s
service = %s
`, settings["greeting"], settings["count"], settings["dir"], settings["file"], settings["password"], settings["service"]))

				args := append([]string{"dagger", "--progress=report", "call"}, tc.call...)
				errOut, err := ctr.WithExec(args, dagger.ContainerWithExecOpts{
					Expect:                        dagger.ReturnTypeFailure,
					ExperimentalPrivilegedNesting: true,
				}).CombinedOutput(ctx)
				require.NoError(t, err)
				for _, want := range tc.assert {
					require.Contains(t, errOut, want)
				}
			})
		}
	})
}

func (WorkspaceSuite) TestWorkspaceConfigurationLifecycle(ctx context.Context, t *testctx.T) {
	t.Run("CurrentWorkspace.Init creates config for the repo", func(ctx context.Context, t *testctx.T) {
		workdir := t.TempDir()
		initGitRepo(ctx, t, workdir)
		require.NoError(t, os.WriteFile(
			filepath.Join(workdir, "query.graphql"),
			[]byte(`{ currentWorkspace { init } }`),
			0o644,
		))

		out, err := hostDaggerExec(ctx, t, workdir, "--silent", "query", "--doc", "query.graphql")
		require.NoError(t, err)
		require.JSONEq(t, fmt.Sprintf(`{"currentWorkspace":{"init":%q}}`, filepath.Join(workdir, workspace.LockDirName)), string(out))

		configContents, err := os.ReadFile(filepath.Join(workdir, workspace.LockDirName, workspace.ConfigFileName))
		require.NoError(t, err)
		require.Contains(t, string(configContents), "[modules]")
	})

	t.Run("workspace config detects the nearest initialized boundary", func(ctx context.Context, t *testctx.T) {
		workdir := t.TempDir()
		nestedDir := filepath.Join(workdir, "app", "sub")

		initGitRepo(ctx, t, workdir)
		writeWorkspaceConfigFile(t, workdir, `[modules.outer]
source = "modules/outer"
`)
		writeWorkspaceConfigFile(t, filepath.Join(workdir, "app"), `[modules.inner]
source = "modules/inner"
`)
		require.NoError(t, os.MkdirAll(nestedDir, 0o755))

		out, err := hostDaggerExec(ctx, t, nestedDir, "--silent", "workspace", "config", "modules.inner.source")
		require.NoError(t, err)
		require.Equal(t, "modules/inner", strings.TrimSpace(string(out)))

		_, err = hostDaggerExec(ctx, t, nestedDir, "--silent", "workspace", "config", "modules.outer.source")
		require.Error(t, err)
		requireErrOut(t, err, `key "modules.outer.source" is not set`)
	})
}
