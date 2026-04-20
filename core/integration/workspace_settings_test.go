package core

// Workspace alignment: this file is the user-facing design spec for `dagger settings`.
// Scope: Command grammar, discovery, read/write semantics, env scoping, and the relationship between `dagger settings` and `dagger config`.
// Intent: Make module settings a first-class UX distinct from workspace config while keeping one underlying source of truth.
//
// Storage examples in this file intentionally use `[modules.<alias>.settings]`
// and `[env.<name>.modules.<alias>.settings]`. That terminology is part of the
// design being specified here. Any legacy storage naming, if it needs coverage,
// belongs in migration or compat tests instead of this command spec.

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// TestWorkspaceSettingsCommandGrammar fixes the command shape before any
// implementation details. The command is intentionally positional and requires
// an explicit module alias at every depth so it stays unambiguous.
func (WorkspaceSuite) TestWorkspaceSettingsCommandGrammar(ctx context.Context, t *testctx.T) {
	t.Run("settings supports exactly zero, one, two, or three positional args", func(ctx context.Context, t *testctx.T) {
		workdir := newWorkspaceSettingsWorkdir(ctx, t, `[modules.aws]
source = "../modules/aws"
entrypoint = true

[modules.aws.settings]
region = "us-west-2"
`, workspaceSettingsAWSModule("modules/aws", "aws"))

		out, err := hostDaggerExec(ctx, t, workdir, "--silent", "settings")
		require.NoError(t, err)
		require.Contains(t, string(out), "aws")
		require.Contains(t, string(out), "region")
		require.Contains(t, string(out), "us-west-2")

		out, err = hostDaggerExec(ctx, t, workdir, "--silent", "settings", "aws")
		require.NoError(t, err)
		require.Contains(t, string(out), "MODULE")
		require.Contains(t, string(out), "KEY")
		require.Contains(t, string(out), "VALUE")
		require.Contains(t, string(out), "DESCRIPTION")
		require.Contains(t, string(out), "region")

		out, err = hostDaggerExec(ctx, t, workdir, "--silent", "settings", "aws", "region")
		require.NoError(t, err)
		require.Equal(t, "us-west-2", strings.TrimSpace(string(out)))

		_, err = hostDaggerExec(ctx, t, workdir, "--silent", "settings", "aws", "region", "eu-central-1")
		require.NoError(t, err)

		out, err = hostDaggerExec(ctx, t, workdir, "--silent", "settings", "aws", "region")
		require.NoError(t, err)
		require.Equal(t, "eu-central-1", strings.TrimSpace(string(out)))

		_, err = hostDaggerExec(ctx, t, workdir, "--silent", "settings", "aws", "region", "eu-central-1", "extra")
		require.Error(t, err)
		requireErrOut(t, err, "accepts at most 3 arg")
	})

	t.Run("module omission is never supported, even for a single entrypoint module", func(ctx context.Context, t *testctx.T) {
		workdir := newWorkspaceSettingsWorkdir(ctx, t, `[modules.greeter]
source = "../modules/greeter"
entrypoint = true

[modules.greeter.settings]
greeting = "hello"
`, workspaceSettingsGreeterModule("modules/greeter", "greeter"))

		_, err := hostDaggerExec(ctx, t, workdir, "--silent", "settings", "greeting")
		require.Error(t, err)
		requireErrOut(t, err, `module "greeting" is not installed in the workspace`)
	})

	t.Run("module selection always uses the workspace alias, not the module's intrinsic name", func(ctx context.Context, t *testctx.T) {
		workdir := newWorkspaceSettingsWorkdir(ctx, t, `[modules.prod-aws]
source = "../modules/aws"

[modules.prod-aws.settings]
region = "us-west-2"
`, workspaceSettingsAWSModule("modules/aws", "aws"))

		out, err := hostDaggerExec(ctx, t, workdir, "--silent", "settings", "prod-aws", "region")
		require.NoError(t, err)
		require.Equal(t, "us-west-2", strings.TrimSpace(string(out)))

		_, err = hostDaggerExec(ctx, t, workdir, "--silent", "settings", "aws", "region")
		require.Error(t, err)
		requireErrOut(t, err, `module "aws" is not installed in the workspace`)
	})
}

// TestWorkspaceSettingsDiscovery defines the high-level, compact settings UX
// that `dagger settings` should provide on top of constructor introspection.
func (WorkspaceSuite) TestWorkspaceSettingsDiscovery(ctx context.Context, t *testctx.T) {
	t.Run("settings with no module arg lists installed modules in deterministic order", func(ctx context.Context, t *testctx.T) {
		workdir := newWorkspaceSettingsWorkdir(ctx, t, `[modules.vitest]
source = "../modules/vitest"

[modules.aws]
source = "../modules/aws"
`, workspaceSettingsAWSModule("modules/aws", "aws"), workspaceSettingsVitestModule("modules/vitest", "vitest"))

		out, err := hostDaggerExec(ctx, t, workdir, "--silent", "settings")
		require.NoError(t, err)

		output := string(out)
		require.Contains(t, output, "MODULE")
		require.Contains(t, output, "KEY")
		require.Contains(t, output, "VALUE")
		require.Contains(t, output, "DESCRIPTION")
		require.Contains(t, output, "aws")
		require.Contains(t, output, "vitest")
		require.Contains(t, output, "region")
		require.Contains(t, output, "failFast")
		require.Less(t, strings.Index(output, "aws"), strings.Index(output, "vitest"))
	})

	t.Run("settings MODULE shows compact setting table with current effective values", func(ctx context.Context, t *testctx.T) {
		workdir := newWorkspaceSettingsWorkdir(ctx, t, `[modules.aws]
source = "../modules/aws"
entrypoint = true

[modules.aws.settings]
region = "us-west-2"
secretKey = "op://vault/aws"
`, workspaceSettingsAWSModule("modules/aws", "aws"))

		out, err := hostDaggerExec(ctx, t, workdir, "--silent", "settings", "aws")
		require.NoError(t, err)

		output := string(out)
		require.Contains(t, output, "MODULE")
		require.Contains(t, output, "KEY")
		require.Contains(t, output, "VALUE")
		require.Contains(t, output, "DESCRIPTION")
		require.NotContains(t, output, "TYPE")
		require.Contains(t, output, "aws")
		require.Contains(t, output, "region")
		require.Contains(t, output, "us-west-2")
		require.Contains(t, output, "Region used by this module.")
		require.Contains(t, output, "secretKey")
		require.Contains(t, output, "op://vault/aws")
		require.NotContains(t, output, "source")
		require.NotContains(t, output, "entrypoint")
	})

	t.Run("settings MODULE in env scope shows effective values after overlay", func(ctx context.Context, t *testctx.T) {
		workdir := newWorkspaceSettingsWorkdir(ctx, t, `[modules.aws]
source = "../modules/aws"

[modules.aws.settings]
format = "json"
region = "us-west-2"

[env.ci.modules.aws.settings]
region = "us-east-1"
`, workspaceSettingsAWSModule("modules/aws", "aws"))

		out, err := hostDaggerExec(ctx, t, workdir, "--silent", "--env=ci", "settings", "aws")
		require.NoError(t, err)

		output := string(out)
		require.Contains(t, output, "region")
		require.Contains(t, output, "us-east-1")
		require.Contains(t, output, "format")
		require.Contains(t, output, "json")
		require.NotContains(t, output, "us-west-2")
	})

	t.Run("unknown module fails clearly instead of printing an empty settings surface", func(ctx context.Context, t *testctx.T) {
		workdir := newWorkspaceSettingsWorkdir(ctx, t, `[modules.aws]
source = "../modules/aws"
`, workspaceSettingsAWSModule("modules/aws", "aws"))

		_, err := hostDaggerExec(ctx, t, workdir, "--silent", "settings", "missing")
		require.Error(t, err)
		requireErrOut(t, err, `module "missing" is not installed in the workspace`)
	})
}

// TestWorkspaceSettingsReadSemantics covers scalar reads for one setting at a
// time. These reads should behave like other scope-aware commands: the selected
// env changes what value is considered active.
func (WorkspaceSuite) TestWorkspaceSettingsReadSemantics(ctx context.Context, t *testctx.T) {
	t.Run("settings MODULE KEY reads the base-scope effective value", func(ctx context.Context, t *testctx.T) {
		workdir := newWorkspaceSettingsWorkdir(ctx, t, `[modules.aws]
source = "../modules/aws"

[modules.aws.settings]
region = "us-west-2"
`, workspaceSettingsAWSModule("modules/aws", "aws"))

		out, err := hostDaggerExec(ctx, t, workdir, "--silent", "settings", "aws", "region")
		require.NoError(t, err)
		require.Equal(t, "us-west-2", strings.TrimSpace(string(out)))
	})

	t.Run("settings MODULE KEY with env reads the effective env value with base fallback", func(ctx context.Context, t *testctx.T) {
		workdir := newWorkspaceSettingsWorkdir(ctx, t, `[modules.aws]
source = "../modules/aws"

[modules.aws.settings]
format = "json"
region = "us-west-2"

[env.ci.modules.aws.settings]
region = "us-east-1"
`, workspaceSettingsAWSModule("modules/aws", "aws"))

		out, err := hostDaggerExec(ctx, t, workdir, "--silent", "--env=ci", "settings", "aws", "region")
		require.NoError(t, err)
		require.Equal(t, "us-east-1", strings.TrimSpace(string(out)))

		out, err = hostDaggerExec(ctx, t, workdir, "--silent", "--env=ci", "settings", "aws", "format")
		require.NoError(t, err)
		require.Equal(t, "json", strings.TrimSpace(string(out)))
	})

	t.Run("missing env fails clearly instead of silently falling back to base", func(ctx context.Context, t *testctx.T) {
		workdir := newWorkspaceSettingsWorkdir(ctx, t, `[modules.aws]
source = "../modules/aws"

[modules.aws.settings]
region = "us-west-2"
`, workspaceSettingsAWSModule("modules/aws", "aws"))

		_, err := hostDaggerExec(ctx, t, workdir, "--silent", "--env=ci", "settings", "aws", "region")
		require.Error(t, err)
		requireErrOut(t, err, `workspace env "ci" is not defined`)
	})

	t.Run("unknown setting fails clearly and does not expose non-setting metadata", func(ctx context.Context, t *testctx.T) {
		workdir := newWorkspaceSettingsWorkdir(ctx, t, `[modules.aws]
source = "../modules/aws"

[modules.aws.settings]
region = "us-west-2"
`, workspaceSettingsAWSModule("modules/aws", "aws"))

		_, err := hostDaggerExec(ctx, t, workdir, "--silent", "settings", "aws", "missing")
		require.Error(t, err)
		requireErrOut(t, err, `module "aws" has no setting "missing"`)

		_, err = hostDaggerExec(ctx, t, workdir, "--silent", "settings", "aws", "source")
		require.Error(t, err)
		requireErrOut(t, err, `module "aws" has no setting "source"`)
	})
}

// TestWorkspaceSettingsWriteSemantics defines where writes land. Reads are
// effective in the selected scope; writes mutate that scope's stored settings.
func (WorkspaceSuite) TestWorkspaceSettingsWriteSemantics(ctx context.Context, t *testctx.T) {
	t.Run("base-scope writes update modules.<alias>.settings and affect later effective reads", func(ctx context.Context, t *testctx.T) {
		workdir := newWorkspaceSettingsWorkdir(ctx, t, `[modules.aws]
source = "../modules/aws"

[modules.aws.settings]
region = "us-west-2"
`, workspaceSettingsAWSModule("modules/aws", "aws"))

		_, err := hostDaggerExec(ctx, t, workdir, "--silent", "settings", "aws", "region", "eu-central-1")
		require.NoError(t, err)

		out, err := hostDaggerExec(ctx, t, workdir, "--silent", "settings", "aws", "region")
		require.NoError(t, err)
		require.Equal(t, "eu-central-1", strings.TrimSpace(string(out)))

		out, err = hostDaggerExec(ctx, t, workdir, "--silent", "config", "modules.aws.settings.region")
		require.NoError(t, err)
		require.Equal(t, "eu-central-1", strings.TrimSpace(string(out)))

		cfg := readInstalledWorkspaceConfig(t, workdir)
		require.Equal(t, "eu-central-1", cfg.Modules["aws"].Settings["region"])
	})

	t.Run("env-scoped writes update env.<name>.modules.<alias>.settings and leave base unchanged", func(ctx context.Context, t *testctx.T) {
		workdir := newWorkspaceSettingsWorkdir(ctx, t, `[modules.aws]
source = "../modules/aws"

[modules.aws.settings]
region = "us-west-2"

[env.ci]
`, workspaceSettingsAWSModule("modules/aws", "aws"))

		_, err := hostDaggerExec(ctx, t, workdir, "--silent", "--env=ci", "settings", "aws", "region", "us-east-1")
		require.NoError(t, err)

		cfg := readInstalledWorkspaceConfig(t, workdir)
		require.Equal(t, "us-west-2", cfg.Modules["aws"].Settings["region"])
		require.Equal(t, "us-east-1", cfg.Env["ci"].Modules["aws"].Settings["region"])

		out, err := hostDaggerExec(ctx, t, workdir, "--silent", "settings", "aws", "region")
		require.NoError(t, err)
		require.Equal(t, "us-west-2", strings.TrimSpace(string(out)))

		out, err = hostDaggerExec(ctx, t, workdir, "--silent", "--env=ci", "settings", "aws", "region")
		require.NoError(t, err)
		require.Equal(t, "us-east-1", strings.TrimSpace(string(out)))
	})

	t.Run("typed writes use the same coercion rules as config writes", func(ctx context.Context, t *testctx.T) {
		workdir := newWorkspaceSettingsWorkdir(ctx, t, `[modules.vitest]
source = "../modules/vitest"
`, workspaceSettingsVitestModule("modules/vitest", "vitest"))

		_, err := hostDaggerExec(ctx, t, workdir, "--silent", "settings", "vitest", "failFast", "true")
		require.NoError(t, err)
		_, err = hostDaggerExec(ctx, t, workdir, "--silent", "settings", "vitest", "retries", "3")
		require.NoError(t, err)
		_, err = hostDaggerExec(ctx, t, workdir, "--silent", "settings", "vitest", "tags", "smoke, regression")
		require.NoError(t, err)

		out, err := hostDaggerExec(ctx, t, workdir, "--silent", "config", "modules.vitest.settings.failFast")
		require.NoError(t, err)
		require.Equal(t, "true", strings.TrimSpace(string(out)))

		out, err = hostDaggerExec(ctx, t, workdir, "--silent", "config", "modules.vitest.settings.retries")
		require.NoError(t, err)
		require.Equal(t, "3", strings.TrimSpace(string(out)))

		out, err = hostDaggerExec(ctx, t, workdir, "--silent", "config", "modules.vitest.settings.tags")
		require.NoError(t, err)
		require.Equal(t, "[smoke, regression]", strings.TrimSpace(string(out)))
	})

	t.Run("writes reject unknown modules, unknown settings, and workspace-owned fields", func(ctx context.Context, t *testctx.T) {
		workdir := newWorkspaceSettingsWorkdir(ctx, t, `[modules.aws]
source = "../modules/aws"

[modules.aws.settings]
region = "us-west-2"
`, workspaceSettingsAWSModule("modules/aws", "aws"))

		tests := []struct {
			args []string
			err  string
		}{
			{[]string{"missing", "region", "us-west-2"}, `module "missing" is not installed in the workspace`},
			{[]string{"aws", "missing", "value"}, `module "aws" has no setting "missing"`},
			{[]string{"aws", "source", "github.com/acme/aws"}, `module "aws" has no setting "source"`},
			{[]string{"aws", "entrypoint", "true"}, `module "aws" has no setting "entrypoint"`},
		}

		for _, tt := range tests {
			_, err := hostDaggerExec(ctx, t, workdir, append([]string{"--silent", "settings"}, tt.args...)...)
			require.Error(t, err)
			requireErrOut(t, err, tt.err)
		}
	})
}

// TestWorkspaceSettingsConfigProjection locks in that `dagger settings` is an
// ergonomic, typed projection over workspace config rather than a second
// storage system with independent semantics.
func (WorkspaceSuite) TestWorkspaceSettingsConfigProjection(ctx context.Context, t *testctx.T) {
	t.Run("settings reads agree with config reads for the same effective value", func(ctx context.Context, t *testctx.T) {
		workdir := newWorkspaceSettingsWorkdir(ctx, t, `[modules.aws]
source = "../modules/aws"

[modules.aws.settings]
region = "us-west-2"

[env.ci.modules.aws.settings]
region = "us-east-1"
`, workspaceSettingsAWSModule("modules/aws", "aws"))

		settingsBase, err := hostDaggerExec(ctx, t, workdir, "--silent", "settings", "aws", "region")
		require.NoError(t, err)
		configBase, err := hostDaggerExec(ctx, t, workdir, "--silent", "config", "modules.aws.settings.region")
		require.NoError(t, err)
		require.Equal(t, strings.TrimSpace(string(configBase)), strings.TrimSpace(string(settingsBase)))

		settingsEnv, err := hostDaggerExec(ctx, t, workdir, "--silent", "--env=ci", "settings", "aws", "region")
		require.NoError(t, err)
		configEnv, err := hostDaggerExec(ctx, t, workdir, "--silent", "--env=ci", "config", "modules.aws.settings.region")
		require.NoError(t, err)
		require.Equal(t, strings.TrimSpace(string(configEnv)), strings.TrimSpace(string(settingsEnv)))
	})

	t.Run("writes through settings are visible immediately through config and runtime behavior", func(ctx context.Context, t *testctx.T) {
		workdir := newWorkspaceSettingsWorkdir(ctx, t, `[modules.aws]
source = "../modules/aws"
entrypoint = true

[modules.aws.settings]
region = "us-west-2"
`, workspaceSettingsAWSModule("modules/aws", "aws"))

		_, err := hostDaggerExec(ctx, t, workdir, "--silent", "settings", "aws", "region", "eu-central-1")
		require.NoError(t, err)

		out, err := hostDaggerExec(ctx, t, workdir, "--silent", "config", "modules.aws.settings.region")
		require.NoError(t, err)
		require.Equal(t, "eu-central-1", strings.TrimSpace(string(out)))

		out, err = hostDaggerExec(ctx, t, workdir, "--silent", "call", "region")
		require.NoError(t, err)
		require.Equal(t, "eu-central-1", strings.TrimSpace(string(out)))
	})
}

type workspaceSettingsModuleFixture struct {
	relDir string
	name   string
	main   string
}

func newWorkspaceSettingsWorkdir(ctx context.Context, t *testctx.T, configTOML string, modules ...workspaceSettingsModuleFixture) string {
	t.Helper()

	workdir := t.TempDir()
	initGitRepo(ctx, t, workdir)
	for _, module := range modules {
		writeWorkspaceSettingsModule(t, workdir, module)
	}
	writeWorkspaceConfigFile(t, workdir, configTOML)
	return workdir
}

func writeWorkspaceSettingsModule(t *testctx.T, workdir string, module workspaceSettingsModuleFixture) {
	t.Helper()

	moduleDir := filepath.Join(workdir, module.relDir)
	require.NoError(t, os.MkdirAll(moduleDir, 0o755))

	sourceModule := testModule(t, "go", "defaults/superconstructor")
	goMod, err := os.ReadFile(filepath.Join(sourceModule, "go.mod"))
	require.NoError(t, err)
	goMod = []byte(strings.Replace(string(goMod), "module dagger/superconstructor", "module dagger/"+module.name, 1))

	goSum, err := os.ReadFile(filepath.Join(sourceModule, "go.sum"))
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(moduleDir, "go.mod"), goMod, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(moduleDir, "go.sum"), goSum, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(moduleDir, "dagger.json"), []byte(`{
  "name": "`+module.name+`",
  "engineVersion": "v0.20.1",
  "sdk": {
    "source": "go"
  },
  "disableDefaultFunctionCaching": true
}
`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(moduleDir, "main.go"), []byte(module.main), 0o644))
}

func workspaceSettingsAWSModule(relDir, name string) workspaceSettingsModuleFixture {
	return workspaceSettingsModuleFixture{
		relDir: relDir,
		name:   name,
		main: `package main

type Aws struct {
	RegionValue    string
	FormatValue    string
	SecretKeyValue string
}

func New(
	// Region used by this module.
	region string,
	// Output format for commands.
	// +default="json"
	format string,
	// Secret key reference for credentials.
	// +optional
	secretKey string,
) *Aws {
	return &Aws{
		RegionValue:    region,
		FormatValue:    format,
		SecretKeyValue: secretKey,
	}
}

func (m *Aws) Region() string {
	return m.RegionValue
}

func (m *Aws) Format() string {
	return m.FormatValue
}
`,
	}
}

func workspaceSettingsGreeterModule(relDir, name string) workspaceSettingsModuleFixture {
	return workspaceSettingsModuleFixture{
		relDir: relDir,
		name:   name,
		main: `package main

type Greeter struct {
	GreetingValue string
}

func New(
	// Greeting used by this module.
	greeting string,
) *Greeter {
	return &Greeter{GreetingValue: greeting}
}

func (m *Greeter) Greeting() string {
	return m.GreetingValue
}
`,
	}
}

func workspaceSettingsVitestModule(relDir, name string) workspaceSettingsModuleFixture {
	return workspaceSettingsModuleFixture{
		relDir: relDir,
		name:   name,
		main: `package main

type Vitest struct {
	FailFast bool
	Retries  int
	Tags     []string
}

func New(
	// Whether failed tests should stop the run.
	failFast bool,
	// Retry count for failed tests.
	retries int,
	// Test tags to select.
	tags []string,
) *Vitest {
	return &Vitest{
		FailFast: failFast,
		Retries:  retries,
		Tags:     tags,
	}
}
`,
	}
}
