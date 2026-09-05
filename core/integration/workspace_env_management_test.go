package core

// These tests cover named workspace environments and the config values scoped to
// each environment. They verify how users select, read, write, and run with
// those environment-specific values.
//
// The standalone `dagger env {create,list,rm}` lifecycle group was removed in
// the CLI 1.0 redesign: an env is now just a path prefix (env.<name>.*) in
// workspace config, so it comes into being when a value is written under it and
// is inspected/edited via `dagger workspace config` (raw) or `dagger module settings
// --env` (typed). There is no longer a discrete create/list/rm command to test.
//
// See also:
// - workspace_settings_test.go: typed module-setting discovery and UX.
// - workspace_config_test.go: config behavior outside env selection.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
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

	t.Run("missing env read error names the defined envs", func(ctx context.Context, t *testctx.T) {
		workdir := newWorkspaceConfigWorkdir(ctx, t, `[modules.aws]
source = "github.com/dagger/aws"

[env.ci]

[env.prod]
`)

		_, err := hostDaggerEnvExec(ctx, t, workdir, "--env=prdo", "workspace", "config")
		require.Error(t, err)
		requireErrOut(t, err, `workspace env "prdo" is not defined (defined envs: ci, prod)`)
	})

	t.Run("selecting an env without a dagger.toml fails instead of printing nothing", func(ctx context.Context, t *testctx.T) {
		workdir := t.TempDir()
		initGitRepo(ctx, t, workdir)

		_, err := hostDaggerEnvExec(ctx, t, workdir, "--env=ci", "workspace", "config")
		require.Error(t, err)
		requireErrOut(t, err, `workspace env "ci" requires dagger.toml`)
	})

	t.Run("listing modules under an env without a dagger.toml fails too", func(ctx context.Context, t *testctx.T) {
		workdir := t.TempDir()
		initGitRepo(ctx, t, workdir)

		_, err := hostDaggerEnvExec(ctx, t, workdir, "--env=ci", "module", "list")
		require.Error(t, err)
		requireErrOut(t, err, `workspace env "ci" requires dagger.toml`)
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

	t.Run("env-scoped writes create missing envs with a notice", func(ctx context.Context, t *testctx.T) {
		workdir := newWorkspaceConfigWorkdir(ctx, t, `[modules.aws]
source = "github.com/dagger/aws"

[modules.aws.settings]
region = "us-west-2"
`)

		out, err := hostDaggerEnvExec(ctx, t, workdir, "--env=staging", "workspace", "config", "modules.aws.settings.region", "us-east-1")
		require.NoError(t, err)
		require.Contains(t, string(out), `Created env "staging"`)

		cfg := readInstalledWorkspaceConfig(t, workdir)
		require.Equal(t, "us-west-2", cfg.Modules["aws"].Settings["region"])
		require.Equal(t, "us-east-1", cfg.Env["staging"].Modules["aws"].Settings["region"])

		// A write into the now-existing env doesn't repeat the notice.
		out, err = hostDaggerEnvExec(ctx, t, workdir, "--env=staging", "workspace", "config", "modules.aws.settings.region", "eu-west-3")
		require.NoError(t, err)
		require.NotContains(t, string(out), "Created env")
	})

	t.Run("env-scoped writes reject unknown module aliases", func(ctx context.Context, t *testctx.T) {
		workdir := newWorkspaceConfigWorkdir(ctx, t, `[modules.aws]
source = "github.com/dagger/aws"

[env.ci]
`)

		_, err := hostDaggerEnvExec(ctx, t, workdir, "--env=ci", "workspace", "config", "modules.missing.settings.region", "us-east-1")
		require.Error(t, err)
		requireErrOut(t, err, `workspace env "ci" cannot set settings for unknown module "missing"`)
	})

	t.Run("env-scoped unsets still require the env to exist", func(ctx context.Context, t *testctx.T) {
		workdir := newWorkspaceConfigWorkdir(ctx, t, `[modules.aws]
source = "github.com/dagger/aws"

[env.ci]
`)

		_, err := hostDaggerEnvExec(ctx, t, workdir, "--env=missing", "workspace", "config", "--unset", "modules.aws.settings.region")
		require.Error(t, err)
		requireErrOut(t, err, `workspace env "missing" is not defined`)
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

		out, err := hostDaggerEnvExec(ctx, t, workdir, "workspace", "config", "env.ci.modules.aws.settings.region", "us-east-1")
		require.NoError(t, err)
		require.Contains(t, string(out), `Created env "ci"`)

		cfg := readInstalledWorkspaceConfig(t, workdir)
		require.Equal(t, "us-east-1", cfg.Env["ci"].Modules["aws"].Settings["region"])

		out, err = hostDaggerEnvExec(ctx, t, workdir, "workspace", "config", "env.ci.modules.aws.settings.format", "json")
		require.NoError(t, err)
		require.NotContains(t, string(out), "Created env")
	})

	t.Run("--here creation notice is detected against the here-targeted config", func(ctx context.Context, t *testctx.T) {
		// The workspace root already defines env.staging, but the subdirectory
		// config the --here write targets does not. The Created-env notice must
		// be detected against the --here target, not the selected (root) config,
		// so it still prints when --here creates the env in the subdir.
		workdir := newWorkspaceConfigWorkdir(ctx, t, `[modules.aws]
source = "github.com/dagger/aws"

[env.staging]
`)
		subdir := filepath.Join(workdir, "sub")
		require.NoError(t, os.MkdirAll(subdir, 0o755))

		out, err := hostDaggerEnvExec(ctx, t, subdir, "workspace", "config", "env.staging.modules.aws.settings.region", "us-east-1", "--here")
		require.NoError(t, err)
		require.Contains(t, string(out), `Created env "staging"`)

		cfg := readInstalledWorkspaceConfig(t, subdir)
		require.Equal(t, "us-east-1", cfg.Env["staging"].Modules["aws"].Settings["region"])

		// A second --here write into the now-existing subdir env doesn't repeat
		// the notice.
		out, err = hostDaggerEnvExec(ctx, t, subdir, "workspace", "config", "env.staging.modules.aws.settings.format", "json", "--here")
		require.NoError(t, err)
		require.NotContains(t, string(out), "Created env")
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

// TestWorkspaceEnvModuleInstall covers env-scoped installs: with --env, `dagger
// install` records the module under env.<name>.modules.* so it only exists when
// that env is selected, creating the env on first write like other env-scoped
// writes. Uninstall under --env removes only the env's overlay entry.
func (WorkspaceSuite) TestWorkspaceEnvModuleInstall(ctx context.Context, t *testctx.T) {
	newEnvInstallWorkdir := func(ctx context.Context, t *testctx.T, configTOML string) string {
		t.Helper()
		workdir := t.TempDir()
		initGitRepo(ctx, t, workdir)
		depDir := filepath.Join(workdir, "dep")
		require.NoError(t, os.MkdirAll(depDir, 0o755))
		copyTestdataFixture(ctx, t, depDir, "modules", "go", "minimal-dep")
		if configTOML != "" {
			require.NoError(t, os.WriteFile(filepath.Join(workdir, "dagger.toml"), []byte(configTOML), 0o644))
		}
		return workdir
	}

	t.Run("env-scoped install creates the env and records the module in its overlay", func(ctx context.Context, t *testctx.T) {
		workdir := newEnvInstallWorkdir(ctx, t, `[modules]
`)

		out, err := hostDaggerExecRaw(ctx, t, workdir, "--silent", "--env=dev", "module", "install", "./dep")
		require.NoError(t, err)
		outStr := string(out)
		require.Contains(t, outStr, `Created env "dev"`)
		require.Contains(t, outStr, `Installed module "dep" into env "dev"`)

		cfg := readInstalledWorkspaceConfig(t, workdir)
		require.NotContains(t, cfg.Modules, "dep")
		require.Equal(t, "dep", cfg.Env["dev"].Modules["dep"].Source)

		// Reinstalling into the now-existing env is a no-op without the notice.
		out, err = hostDaggerExecRaw(ctx, t, workdir, "--silent", "--env=dev", "module", "install", "./dep")
		require.NoError(t, err)
		require.NotContains(t, string(out), "Created env")
		require.Contains(t, string(out), `Module "dep" is already installed in env "dev"`)
	})

	t.Run("env install with no dagger.toml creates config and env", func(ctx context.Context, t *testctx.T) {
		workdir := newEnvInstallWorkdir(ctx, t, "")

		out, err := hostDaggerExecRaw(ctx, t, workdir, "--silent", "--env=dev", "module", "install", "./dep")
		require.NoError(t, err)
		outStr := string(out)
		require.Contains(t, outStr, "Created workspace config in")
		require.Contains(t, outStr, `Created env "dev"`)
		require.Contains(t, outStr, `Installed module "dep" into env "dev"`)

		cfg := readInstalledWorkspaceConfig(t, workdir)
		require.NotContains(t, cfg.Modules, "dep")
		require.Equal(t, "dep", cfg.Env["dev"].Modules["dep"].Source)
	})

	t.Run("--here install writes the env module into the subdirectory config", func(ctx context.Context, t *testctx.T) {
		// The workspace root defines env.dev, but the subdirectory config the
		// --here install targets does not: both the Created-env notice and the
		// overlay entry belong to the here-target, not the selected config.
		workdir := newEnvInstallWorkdir(ctx, t, `[env.dev]
`)
		subdir := filepath.Join(workdir, "sub")
		require.NoError(t, os.MkdirAll(subdir, 0o755))

		out, err := hostDaggerExecRaw(ctx, t, subdir, "--silent", "--env=dev", "module", "install", "../dep", "--here")
		require.NoError(t, err)
		outStr := string(out)
		require.Contains(t, outStr, `Created env "dev"`)
		require.Contains(t, outStr, `Installed module "dep" into env "dev"`)

		cfg := readInstalledWorkspaceConfig(t, subdir)
		require.NotContains(t, cfg.Modules, "dep")
		require.Contains(t, cfg.Env["dev"].Modules["dep"].Source, "dep")

		rootCfg := readInstalledWorkspaceConfig(t, workdir)
		require.NotContains(t, rootCfg.Env["dev"].Modules, "dep")
	})

	t.Run("settings on an env-added module can be written and unset", func(ctx context.Context, t *testctx.T) {
		workdir := newEnvInstallWorkdir(ctx, t, `[modules]
`)

		_, err := hostDaggerExecRaw(ctx, t, workdir, "--silent", "--env=dev", "module", "install", "./dep")
		require.NoError(t, err)

		// minimal-dep has no constructor args, so exercise the raw config path:
		// the point is that the env-scoped unset accepts a module the env itself
		// added, which only exists in the overlay.
		_, err = hostDaggerExecRaw(ctx, t, workdir, "--silent", "workspace", "config", "env.dev.modules.dep.settings.foo", "bar")
		require.NoError(t, err)
		cfg := readInstalledWorkspaceConfig(t, workdir)
		require.Equal(t, "bar", cfg.Env["dev"].Modules["dep"].Settings["foo"])

		_, err = hostDaggerExecRaw(ctx, t, workdir, "--silent", "--env=dev", "workspace", "config", "--unset", "modules.dep.settings.foo")
		require.NoError(t, err)
		cfg = readInstalledWorkspaceConfig(t, workdir)
		require.NotContains(t, cfg.Env["dev"].Modules["dep"].Settings, "foo")
		require.Equal(t, "dep", cfg.Env["dev"].Modules["dep"].Source)
	})

	t.Run("env-scoped modules are listed only under their env", func(ctx context.Context, t *testctx.T) {
		workdir := newEnvInstallWorkdir(ctx, t, `[modules]
`)

		_, err := hostDaggerExecRaw(ctx, t, workdir, "--silent", "--env=dev", "module", "install", "./dep")
		require.NoError(t, err)

		out, err := hostDaggerExecRaw(ctx, t, workdir, "--silent", "--env=dev", "module", "list")
		require.NoError(t, err)
		require.Contains(t, string(out), "dep")

		out, err = hostDaggerExecRaw(ctx, t, workdir, "--silent", "module", "list")
		require.NoError(t, err)
		require.NotContains(t, string(out), "dep")

		// The env-scoped module actually loads and runs under its env.
		out, err = hostDaggerExecRaw(ctx, t, workdir, "--silent", "--env=dev", "call", "dep", "hello")
		require.NoError(t, err)
		require.Equal(t, "hello", strings.TrimSpace(string(out)))
	})

	t.Run("env-scoped uninstall removes the overlay entry and leaves base alone", func(ctx context.Context, t *testctx.T) {
		workdir := newEnvInstallWorkdir(ctx, t, `[modules.dep]
source = "dep"

[env.dev.modules.other]
source = "dep"
`)

		out, err := hostDaggerExecRaw(ctx, t, workdir, "--silent", "--env=dev", "module", "uninstall", "other")
		require.NoError(t, err)
		require.Contains(t, string(out), `Uninstalled module "other" from env "dev"`)

		cfg := readInstalledWorkspaceConfig(t, workdir)
		require.Equal(t, "dep", cfg.Modules["dep"].Source)
		require.NotContains(t, cfg.Env["dev"].Modules, "other")

		// A module only in base is not removable through an env selection.
		_, err = hostDaggerExecRaw(ctx, t, workdir, "--silent", "--env=dev", "module", "uninstall", "dep")
		require.Error(t, err)
		requireErrOut(t, err, `module "dep" is not installed in env "dev"`)

		// And a missing env stays a strict error for uninstall.
		_, err = hostDaggerExecRaw(ctx, t, workdir, "--silent", "--env=missing", "module", "uninstall", "dep")
		require.Error(t, err)
		requireErrOut(t, err, `workspace env "missing" is not defined`)
	})

	t.Run("env uninstall drops the install but keeps settings overrides", func(ctx context.Context, t *testctx.T) {
		workdir := newEnvInstallWorkdir(ctx, t, `[modules.dep]
source = "dep"
`)

		_, err := hostDaggerExecRaw(ctx, t, workdir, "--silent", "--env=dev", "module", "install", "./dep")
		require.NoError(t, err)
		_, err = hostDaggerExecRaw(ctx, t, workdir, "--silent", "--env=dev", "workspace", "config", "modules.dep.settings.foo", "bar")
		require.NoError(t, err)

		_, err = hostDaggerExecRaw(ctx, t, workdir, "--silent", "--env=dev", "module", "uninstall", "dep")
		require.NoError(t, err)

		cfg := readInstalledWorkspaceConfig(t, workdir)
		require.Equal(t, "dep", cfg.Modules["dep"].Source)
		// The install aspect is gone, the override the env still owns is not.
		require.Empty(t, cfg.Env["dev"].Modules["dep"].Source)
		require.Equal(t, "bar", cfg.Env["dev"].Modules["dep"].Settings["foo"])

		// The settings-only leftover is not an install, so uninstall refuses it.
		_, err = hostDaggerExecRaw(ctx, t, workdir, "--silent", "--env=dev", "module", "uninstall", "dep")
		require.Error(t, err)
		requireErrOut(t, err, `module "dep" is not installed in env "dev"`)
	})

	t.Run("env install over a base module announces the source override", func(ctx context.Context, t *testctx.T) {
		workdir := newEnvInstallWorkdir(ctx, t, `[modules.dep]
source = "dep"
`)
		forkDir := filepath.Join(workdir, "fork")
		require.NoError(t, os.MkdirAll(forkDir, 0o755))
		copyTestdataFixture(ctx, t, forkDir, "modules", "go", "minimal-dep")

		out, err := hostDaggerExecRaw(ctx, t, workdir, "--silent", "--env=dev", "module", "install", "--name=dep", "./fork")
		require.NoError(t, err)
		outStr := string(out)
		require.Contains(t, outStr, `Installed module "dep" into env "dev"`)
		require.Contains(t, outStr, `Module "dep" in env "dev" overrides source "dep"`)

		cfg := readInstalledWorkspaceConfig(t, workdir)
		require.Equal(t, "dep", cfg.Modules["dep"].Source)
		require.Equal(t, "fork", cfg.Env["dev"].Modules["dep"].Source)

		// Installing a genuinely new module into the env stays notice-free.
		otherDir := filepath.Join(workdir, "other")
		require.NoError(t, os.MkdirAll(otherDir, 0o755))
		copyTestdataFixture(ctx, t, otherDir, "modules", "go", "minimal-dep")
		out, err = hostDaggerExecRaw(ctx, t, workdir, "--silent", "--env=dev", "module", "install", "--name=other", "./other")
		require.NoError(t, err)
		require.NotContains(t, string(out), "overrides source")
	})

	t.Run("SDK installs reject an env selection", func(ctx context.Context, t *testctx.T) {
		workdir := newEnvInstallWorkdir(ctx, t, `[modules]
`)
		sdkModulePath, err := filepath.Abs("testdata/sdks/module-max-lifecycle")
		require.NoError(t, err)

		_, err = hostDaggerExecRaw(ctx, t, workdir, "--silent", "--env=dev", "module", "install", sdkModulePath)
		require.Error(t, err)
		requireErrOut(t, err, `SDKs cannot be installed in env "dev"`)
	})

	t.Run("SDK uninstall rejects an env selection", func(ctx context.Context, t *testctx.T) {
		workdir := newEnvInstallWorkdir(ctx, t, `[modules.dep]
source = "dep"

[sdks.dep]
module = "dep"

[env.dev]
`)

		_, err := hostDaggerExecRaw(ctx, t, workdir, "--silent", "--env=dev", "module", "uninstall", "dep")
		require.Error(t, err)
		requireErrOut(t, err, "SDKs are not env-scoped")
	})

	t.Run("setup rejects an env selection", func(ctx context.Context, t *testctx.T) {
		workdir := newEnvInstallWorkdir(ctx, t, `[env.dev]
`)

		_, err := hostDaggerExecRaw(ctx, t, workdir, "--silent", "--env=dev", "setup")
		require.Error(t, err)
		requireErrOut(t, err, "setup does not support --env")
	})
}
