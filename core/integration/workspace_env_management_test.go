package core

// These tests cover named workspace environments and the config values scoped to
// each environment. They verify how users select, read, write, and run with
// those environment-specific values.
//
// The standalone `dagger env {create,list,rm}` lifecycle group was removed in
// the CLI 1.0 redesign: an env is now just a path prefix (env.<name>.*) in
// workspace config, so it comes into being when a value is written under it and
// is inspected/edited via `dagger workspace config` (raw) or `dagger settings
// --env` (typed). There is no longer a discrete create/list/rm command to test.
//
// See also:
// - workspace_settings_test.go: typed module-setting discovery and UX.
// - workspace_config_test.go: config behavior outside env selection.

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

const workspaceEnvConfigFixture = `[modules.aws]
source = "github.com/dagger/aws"

[modules.aws.settings]
format = "json"
region = "us-west-2"

[modules.vitest]
source = "github.com/dagger/vitest"

[modules.vitest.settings]
reporter = "dot"

[env.ci.modules.aws.settings]
region = "us-east-1"
`

func hostDaggerEnvExec(ctx context.Context, t *testctx.T, workdir string, args ...string) ([]byte, error) {
	t.Helper()

	cmd := hostDaggerCommandRaw(ctx, t, workdir, append([]string{"--progress=report"}, args...)...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		err = fmt.Errorf("%s%s: %w", stdout.String(), stderr.String(), err)
	}
	return stdout.Bytes(), err
}

// TestWorkspaceEnvConfigReadSemantics defines what users should see from
// `dagger workspace config` when they select an environment. The command is a config UX,
// not a raw TOML browser, so env-scoped reads should default to effective
// merged values.
func (WorkspaceSuite) TestWorkspaceEnvConfigReadSemantics(ctx context.Context, t *testctx.T) {
	t.Run("whole-file read with env shows the effective active config", func(ctx context.Context, t *testctx.T) {
		workdir := newWorkspaceConfigWorkdir(ctx, t, workspaceEnvConfigFixture)

		out, err := hostDaggerEnvExec(ctx, t, workdir, "--env=ci", "workspace", "config")
		require.NoError(t, err)

		output := string(out)
		require.Contains(t, output, "[modules.aws]")
		require.Contains(t, output, `source = "github.com/dagger/aws"`)
		require.Contains(t, output, "[modules.aws.settings]")
		require.Contains(t, output, `format = "json"`)
		require.Contains(t, output, `region = "us-east-1"`)
		require.Contains(t, output, "[modules.vitest]")
		require.Contains(t, output, `source = "github.com/dagger/vitest"`)
		require.Contains(t, output, "[modules.vitest.settings]")
		require.Contains(t, output, `reporter = "dot"`)
		require.NotContains(t, output, "[env.ci")
		require.NotContains(t, output, `region = "us-west-2"`)
	})

	t.Run("scalar reads in env scope return effective values with base fallback", func(ctx context.Context, t *testctx.T) {
		workdir := newWorkspaceConfigWorkdir(ctx, t, workspaceEnvConfigFixture)

		out, err := hostDaggerEnvExec(ctx, t, workdir, "--env=ci", "workspace", "config", "modules.aws.settings.region")
		require.NoError(t, err)
		require.Equal(t, "us-east-1", strings.TrimSpace(string(out)))

		out, err = hostDaggerEnvExec(ctx, t, workdir, "--env=ci", "workspace", "config", "modules.aws.settings.format")
		require.NoError(t, err)
		require.Equal(t, "json", strings.TrimSpace(string(out)))
	})

	t.Run("table reads in env scope merge base entry fields with env settings overrides", func(ctx context.Context, t *testctx.T) {
		workdir := newWorkspaceConfigWorkdir(ctx, t, workspaceEnvConfigFixture)

		out, err := hostDaggerEnvExec(ctx, t, workdir, "--env=ci", "workspace", "config", "modules.aws")
		require.NoError(t, err)
		output := string(out)
		require.Contains(t, output, `source = "github.com/dagger/aws"`)
		require.Contains(t, output, `settings.format = "json"`)
		require.Contains(t, output, `settings.region = "us-east-1"`)
		require.NotContains(t, output, "us-west-2")

		out, err = hostDaggerEnvExec(ctx, t, workdir, "--env=ci", "workspace", "config", "modules.aws.settings")
		require.NoError(t, err)
		output = string(out)
		require.Contains(t, output, `format = "json"`)
		require.Contains(t, output, `region = "us-east-1"`)
		require.NotContains(t, output, "us-west-2")
	})

	t.Run("missing env fails clearly instead of silently falling back to base", func(ctx context.Context, t *testctx.T) {
		workdir := newWorkspaceConfigWorkdir(ctx, t, `[modules.aws]
source = "github.com/dagger/aws"

[modules.aws.settings]
region = "us-west-2"
`)

		_, err := hostDaggerEnvExec(ctx, t, workdir, "--env=ci", "workspace", "config")
		require.Error(t, err)
		requireErrOut(t, err, `workspace env "ci" is not defined`)

		_, err = hostDaggerEnvExec(ctx, t, workdir, "--env=ci", "workspace", "config", "modules.aws.settings.region")
		require.Error(t, err)
		requireErrOut(t, err, `workspace env "ci" is not defined`)
	})
}

// TestWorkspaceEnvConfigWriteSemantics defines where writes land when an env is
// selected. Reads are effective in the selected scope; writes mutate that same
// scope's underlying storage.
func (WorkspaceSuite) TestWorkspaceEnvConfigWriteSemantics(ctx context.Context, t *testctx.T) {
	t.Run("write with env stores the override under env scope and leaves base unchanged", func(ctx context.Context, t *testctx.T) {
		workdir := newWorkspaceConfigWorkdir(ctx, t, `[modules.aws]
source = "github.com/dagger/aws"

[modules.aws.settings]
region = "us-west-2"

[env.ci]
`)

		_, err := hostDaggerEnvExec(ctx, t, workdir, "--env=ci", "workspace", "config", "modules.aws.settings.region", "us-east-1")
		require.NoError(t, err)

		cfg := readInstalledWorkspaceConfig(t, workdir)
		require.Equal(t, "us-west-2", cfg.Modules["aws"].Settings["region"])
		require.Equal(t, "us-east-1", cfg.Env["ci"].Modules["aws"].Settings["region"])

		out, err := hostDaggerEnvExec(ctx, t, workdir, "workspace", "config", "modules.aws.settings.region")
		require.NoError(t, err)
		require.Equal(t, "us-west-2", strings.TrimSpace(string(out)))

		out, err = hostDaggerEnvExec(ctx, t, workdir, "--env=ci", "workspace", "config", "modules.aws.settings.region")
		require.NoError(t, err)
		require.Equal(t, "us-east-1", strings.TrimSpace(string(out)))
	})

	t.Run("env-scoped writes use the same scalar typing rules as base writes", func(ctx context.Context, t *testctx.T) {
		workdir := newWorkspaceConfigWorkdir(ctx, t, `[modules.vitest]
source = "github.com/dagger/vitest"

[env.ci]
`)

		_, err := hostDaggerEnvExec(ctx, t, workdir, "--env=ci", "workspace", "config", "modules.vitest.settings.failFast", "true")
		require.NoError(t, err)
		_, err = hostDaggerEnvExec(ctx, t, workdir, "--env=ci", "workspace", "config", "modules.vitest.settings.retries", "3")
		require.NoError(t, err)
		_, err = hostDaggerEnvExec(ctx, t, workdir, "--env=ci", "workspace", "config", "modules.vitest.settings.tags", "smoke, nightly")
		require.NoError(t, err)

		settings := readInstalledWorkspaceConfig(t, workdir).Env["ci"].Modules["vitest"].Settings
		require.Equal(t, true, settings["failFast"])
		require.Equal(t, int64(3), settings["retries"])
		require.Equal(t, []any{"smoke", "nightly"}, settings["tags"])
	})

	t.Run("env-scoped writes reject keys outside the allowed overlay surface", func(ctx context.Context, t *testctx.T) {
		workdir := newWorkspaceConfigWorkdir(ctx, t, `[modules.aws]
source = "github.com/dagger/aws"

[env.ci]
`)

		tests := [][]string{
			{"modules.aws.source", "github.com/acme/aws"},
			{"modules.aws.entrypoint", "true"},
			{"defaults_from_dotenv", "true"},
		}
		for _, args := range tests {
			_, err := hostDaggerEnvExec(ctx, t, workdir, "--env=ci", "workspace", "config", args[0], args[1])
			require.Error(t, err)
			requireErrOut(t, err, `only modules.<name>.settings.* is supported`)
		}
	})

	t.Run("env-scoped writes reject missing envs and unknown module aliases", func(ctx context.Context, t *testctx.T) {
		workdir := newWorkspaceConfigWorkdir(ctx, t, `[modules.aws]
source = "github.com/dagger/aws"

[env.ci]
`)

		_, err := hostDaggerEnvExec(ctx, t, workdir, "--env=missing", "workspace", "config", "modules.aws.settings.region", "us-east-1")
		require.Error(t, err)
		requireErrOut(t, err, `workspace env "missing" is not defined`)

		_, err = hostDaggerEnvExec(ctx, t, workdir, "--env=ci", "workspace", "config", "modules.missing.settings.region", "us-east-1")
		require.Error(t, err)
		requireErrOut(t, err, `workspace env "ci" cannot set settings for unknown module "missing"`)
	})
}

// TestWorkspaceEnvRawAccessEscapeHatches locks in the low-level escape hatch
// for users who need to inspect or edit the raw env subtree rather than the
// effective active config.
func (WorkspaceSuite) TestWorkspaceEnvRawAccessEscapeHatches(ctx context.Context, t *testctx.T) {
	t.Run("explicit env-prefixed keys address raw stored overlays", func(ctx context.Context, t *testctx.T) {
		workdir := newWorkspaceConfigWorkdir(ctx, t, `[modules.aws]
source = "github.com/dagger/aws"

[modules.aws.settings]
region = "us-west-2"

[env.ci.modules.aws.settings]
region = "us-east-1"
`)

		out, err := hostDaggerEnvExec(ctx, t, workdir, "workspace", "config", "env.ci.modules.aws.settings.region")
		require.NoError(t, err)
		require.Equal(t, "us-east-1", strings.TrimSpace(string(out)))

		out, err = hostDaggerEnvExec(ctx, t, workdir, "--env=ci", "workspace", "config", "modules.aws.settings.region")
		require.NoError(t, err)
		require.Equal(t, "us-east-1", strings.TrimSpace(string(out)))
	})

	t.Run("explicit env-prefixed writes edit raw stored overlays directly", func(ctx context.Context, t *testctx.T) {
		workdir := newWorkspaceConfigWorkdir(ctx, t, `[modules.aws]
source = "github.com/dagger/aws"
`)

		_, err := hostDaggerEnvExec(ctx, t, workdir, "workspace", "config", "env.ci.modules.aws.settings.region", "us-east-1")
		require.NoError(t, err)

		cfg := readInstalledWorkspaceConfig(t, workdir)
		require.Equal(t, "us-east-1", cfg.Env["ci"].Modules["aws"].Settings["region"])
	})

	t.Run("explicit env-prefixed keys remain raw even when a current env is selected", func(ctx context.Context, t *testctx.T) {
		workdir := newWorkspaceConfigWorkdir(ctx, t, `[modules.aws]
source = "github.com/dagger/aws"

[modules.aws.settings]
region = "us-west-2"

[env.ci.modules.aws.settings]
region = "us-east-1"

[env.prod.modules.aws.settings]
region = "eu-central-1"
`)

		out, err := hostDaggerEnvExec(ctx, t, workdir, "--env=prod", "workspace", "config", "env.ci.modules.aws.settings.region")
		require.NoError(t, err)
		require.Equal(t, "us-east-1", strings.TrimSpace(string(out)))
	})
}

// TestWorkspaceEnvConfigRuntimeConsistency keeps the user-facing promise that
// `dagger workspace config` reflects what runtime commands will actually use under the
// same env selection.
func (WorkspaceSuite) TestWorkspaceEnvConfigRuntimeConsistency(ctx context.Context, t *testctx.T) {
	t.Run("effective config reads match the defaults used by runtime commands", func(ctx context.Context, t *testctx.T) {
		workdir := newWorkspaceSettingsWorkdir(ctx, t, `[modules.aws]
source = "modules/aws"
entrypoint = true

[modules.aws.settings]
region = "us-west-2"

[env.ci.modules.aws.settings]
region = "us-east-1"
`, workspaceSettingsAWSModule("modules/aws", "aws"))

		configOut, err := hostDaggerEnvExec(ctx, t, workdir, "--env=ci", "workspace", "config", "modules.aws.settings.region")
		require.NoError(t, err)
		require.Equal(t, "us-east-1", strings.TrimSpace(string(configOut)))

		helpOut, err := hostDaggerEnvExec(ctx, t, workdir, "--env=ci", "call", "--help")
		require.NoError(t, err)
		require.Contains(t, string(helpOut), "--region")
		require.Contains(t, string(helpOut), `default "us-east-1"`)

		callOut, err := hostDaggerEnvExec(ctx, t, workdir, "--env=ci", "call", "region")
		require.NoError(t, err)
		require.Equal(t, "us-east-1", strings.TrimSpace(string(callOut)))
	})

	t.Run("env-scoped writes affect only that envs runtime behavior", func(ctx context.Context, t *testctx.T) {
		workdir := newWorkspaceSettingsWorkdir(ctx, t, `[modules.aws]
source = "modules/aws"
entrypoint = true

[modules.aws.settings]
region = "base-region"

[env.ci]

[env.dev]
`, workspaceSettingsAWSModule("modules/aws", "aws"))

		_, err := hostDaggerEnvExec(ctx, t, workdir, "--env=ci", "workspace", "config", "modules.aws.settings.region", "us-east-1")
		require.NoError(t, err)
		_, err = hostDaggerEnvExec(ctx, t, workdir, "--env=dev", "workspace", "config", "modules.aws.settings.region", "us-west-2")
		require.NoError(t, err)

		out, err := hostDaggerEnvExec(ctx, t, workdir, "--env=ci", "call", "region")
		require.NoError(t, err)
		require.Equal(t, "us-east-1", strings.TrimSpace(string(out)))

		out, err = hostDaggerEnvExec(ctx, t, workdir, "--env=dev", "call", "region")
		require.NoError(t, err)
		require.Equal(t, "us-west-2", strings.TrimSpace(string(out)))

		out, err = hostDaggerEnvExec(ctx, t, workdir, "call", "region")
		require.NoError(t, err)
		require.Equal(t, "base-region", strings.TrimSpace(string(out)))
	})
}
