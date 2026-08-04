package core

// These tests cover user-level workspace configuration: personal overrides
// kept outside the repository in the user's Dagger config file
// (~/.config/dagger/config.toml, or $DAGGER_CONFIG), keyed by the workspace's
// normalized Git remote. User-level values merge over the repository's
// dagger.toml without modifying it, and may add personal environments.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

const userConfigWorkspaceFixture = `[modules.aws]
source = "github.com/dagger/aws"

[modules.aws.settings]
profile = "shared"
region = "us-east-1"

[env.staging.modules.aws.settings]
profile = "staging"
`

// newUserConfigWorkdir builds a git workspace with an origin remote and the
// shared fixture config, plus a user-level config file outside the repo.
// It returns the workdir and the user config path to pass via $DAGGER_CONFIG.
func newUserConfigWorkdir(ctx context.Context, t *testctx.T, originURL, userConfigTOML string) (string, string) {
	t.Helper()

	workdir := newWorkspaceConfigWorkdir(ctx, t, userConfigWorkspaceFixture)
	if originURL != "" {
		gitCmd := exec.Command("git", "remote", "add", "origin", originURL)
		gitCmd.Dir = workdir
		gitOutput, err := gitCmd.CombinedOutput()
		require.NoError(t, err, string(gitOutput))
	}

	userConfigPath := filepath.Join(t.TempDir(), "config.toml")
	if userConfigTOML != "" {
		require.NoError(t, os.WriteFile(userConfigPath, []byte(userConfigTOML), 0o600))
	}
	return workdir, userConfigPath
}

// hostDaggerUserConfigExec runs the CLI with $DAGGER_CONFIG pointing at the
// given user-level config path.
func hostDaggerUserConfigExec(ctx context.Context, t *testctx.T, workdir, userConfigPath string, args ...string) ([]byte, error) {
	t.Helper()

	cmd := hostDaggerCommandRaw(ctx, t, workdir, append([]string{"--progress=report"}, args...)...)
	cmd.Env = append(cmd.Env, "DAGGER_CONFIG="+userConfigPath)
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

func (WorkspaceSuite) TestWorkspaceUserConfig(ctx context.Context, t *testctx.T) {
	t.Run("user settings merge over repo config", func(ctx context.Context, t *testctx.T) {
		workdir, userConfigPath := newUserConfigWorkdir(ctx, t,
			"git@github.com:acme/user-config-test.git", `
[workspaces."github.com/acme/user-config-test".modules.aws.settings]
profile = "alice-dev"
`)

		out, err := hostDaggerUserConfigExec(ctx, t, workdir, userConfigPath, "workspace", "config", "modules.aws.settings.profile")
		require.NoError(t, err)
		require.Equal(t, "alice-dev", strings.TrimSpace(string(out)))

		// Keys the user did not override keep their repo values.
		out, err = hostDaggerUserConfigExec(ctx, t, workdir, userConfigPath, "workspace", "config", "modules.aws.settings.region")
		require.NoError(t, err)
		require.Equal(t, "us-east-1", strings.TrimSpace(string(out)))

		// The repository config file itself is never modified.
		repoConfig, err := os.ReadFile(filepath.Join(workdir, "dagger.toml"))
		require.NoError(t, err)
		require.Equal(t, userConfigWorkspaceFixture, string(repoConfig))
	})

	t.Run("equivalent remote URL forms match", func(ctx context.Context, t *testctx.T) {
		// Repo origin is scp-style ssh; the user config keys the same remote
		// in https form with a .git suffix.
		workdir, userConfigPath := newUserConfigWorkdir(ctx, t,
			"git@github.com:acme/user-config-test.git", `
[workspaces."https://github.com/acme/user-config-test.git".modules.aws.settings]
profile = "alice-dev"
`)

		out, err := hostDaggerUserConfigExec(ctx, t, workdir, userConfigPath, "workspace", "config", "modules.aws.settings.profile")
		require.NoError(t, err)
		require.Equal(t, "alice-dev", strings.TrimSpace(string(out)))
	})

	t.Run("user config can add a personal env", func(ctx context.Context, t *testctx.T) {
		workdir, userConfigPath := newUserConfigWorkdir(ctx, t,
			"git@github.com:acme/user-config-test.git", `
[workspaces."github.com/acme/user-config-test".env.dev.modules.aws.settings]
profile = "alice-dev"
region = "us-west-2"
`)

		out, err := hostDaggerUserConfigExec(ctx, t, workdir, userConfigPath, "--env=dev", "workspace", "config", "modules.aws.settings.region")
		require.NoError(t, err)
		require.Equal(t, "us-west-2", strings.TrimSpace(string(out)))

		// Without env selection, the user-added env is visible in the merged
		// config view alongside the repo's own envs.
		out, err = hostDaggerUserConfigExec(ctx, t, workdir, userConfigPath, "workspace", "config")
		require.NoError(t, err)
		require.Contains(t, string(out), "[env.dev.modules.aws.settings]")
		require.Contains(t, string(out), "[env.staging.modules.aws.settings]")
	})

	t.Run("user env merges over repo env", func(ctx context.Context, t *testctx.T) {
		workdir, userConfigPath := newUserConfigWorkdir(ctx, t,
			"git@github.com:acme/user-config-test.git", `
[workspaces."github.com/acme/user-config-test".env.staging.modules.aws.settings]
region = "eu-west-1"
`)

		// Repo env value survives; user env value layers on top.
		out, err := hostDaggerUserConfigExec(ctx, t, workdir, userConfigPath, "--env=staging", "workspace", "config", "modules.aws.settings.profile")
		require.NoError(t, err)
		require.Equal(t, "staging", strings.TrimSpace(string(out)))

		out, err = hostDaggerUserConfigExec(ctx, t, workdir, userConfigPath, "--env=staging", "workspace", "config", "modules.aws.settings.region")
		require.NoError(t, err)
		require.Equal(t, "eu-west-1", strings.TrimSpace(string(out)))
	})

	t.Run("overrides for other workspaces do not apply", func(ctx context.Context, t *testctx.T) {
		workdir, userConfigPath := newUserConfigWorkdir(ctx, t,
			"git@github.com:acme/user-config-test.git", `
[workspaces."github.com/acme/other".modules.aws.settings]
profile = "alice-dev"
`)

		out, err := hostDaggerUserConfigExec(ctx, t, workdir, userConfigPath, "workspace", "config", "modules.aws.settings.profile")
		require.NoError(t, err)
		require.Equal(t, "shared", strings.TrimSpace(string(out)))
	})

	t.Run("workspace without a remote never matches", func(ctx context.Context, t *testctx.T) {
		workdir, userConfigPath := newUserConfigWorkdir(ctx, t, "", `
[workspaces."github.com/acme/user-config-test".modules.aws.settings]
profile = "alice-dev"
`)

		out, err := hostDaggerUserConfigExec(ctx, t, workdir, userConfigPath, "workspace", "config", "modules.aws.settings.profile")
		require.NoError(t, err)
		require.Equal(t, "shared", strings.TrimSpace(string(out)))
	})

	t.Run("settings display includes user values", func(ctx context.Context, t *testctx.T) {
		workdir := newWorkspaceSettingsWorkdir(ctx, t, `[modules.aws]
source = "modules/aws"
entrypoint = true

[modules.aws.settings]
region = "us-east-1"
`, workspaceSettingsAWSModule("modules/aws", "aws"))
		gitCmd := exec.Command("git", "remote", "add", "origin", "git@github.com:acme/user-config-test.git")
		gitCmd.Dir = workdir
		gitOutput, err := gitCmd.CombinedOutput()
		require.NoError(t, err, string(gitOutput))

		userConfigPath := filepath.Join(t.TempDir(), "config.toml")
		require.NoError(t, os.WriteFile(userConfigPath, []byte(`
[workspaces."github.com/acme/user-config-test".modules.aws.settings]
region = "us-west-2"

[workspaces."github.com/acme/user-config-test".env.dev.modules.aws.settings]
region = "eu-west-1"
`), 0o600))

		out, err := hostDaggerUserConfigExec(ctx, t, workdir, userConfigPath, "settings", "aws", "region")
		require.NoError(t, err)
		require.Equal(t, "us-west-2", strings.TrimSpace(string(out)))

		// The selected env overlay still layers over the user-level value.
		out, err = hostDaggerUserConfigExec(ctx, t, workdir, userConfigPath, "--env=dev", "settings", "aws", "region")
		require.NoError(t, err)
		require.Equal(t, "eu-west-1", strings.TrimSpace(string(out)))
	})

	t.Run("missing user config file behaves as before", func(ctx context.Context, t *testctx.T) {
		workdir, userConfigPath := newUserConfigWorkdir(ctx, t,
			"git@github.com:acme/user-config-test.git", "")

		out, err := hostDaggerUserConfigExec(ctx, t, workdir, userConfigPath, "workspace", "config", "modules.aws.settings.profile")
		require.NoError(t, err)
		require.Equal(t, "shared", strings.TrimSpace(string(out)))
	})
}

func (WorkspaceSuite) TestWorkspaceUserConfigWrites(ctx context.Context, t *testctx.T) {
	t.Run("workspace config --global set, read back, unset", func(ctx context.Context, t *testctx.T) {
		workdir, userConfigPath := newUserConfigWorkdir(ctx, t,
			"git@github.com:acme/user-config-test.git", "")

		_, err := hostDaggerUserConfigExec(ctx, t, workdir, userConfigPath, "workspace", "config", "-g", "modules.aws.settings.profile", "alice-dev")
		require.NoError(t, err)

		// The write landed in the user config file, keyed canonically, and the
		// repository config is untouched.
		userConfig, err := os.ReadFile(userConfigPath)
		require.NoError(t, err)
		require.Contains(t, string(userConfig), `github.com/acme/user-config-test`)
		require.Contains(t, string(userConfig), `profile = "alice-dev"`)
		repoConfig, err := os.ReadFile(filepath.Join(workdir, "dagger.toml"))
		require.NoError(t, err)
		require.Equal(t, userConfigWorkspaceFixture, string(repoConfig))

		// Effective reads show the user-level value.
		out, err := hostDaggerUserConfigExec(ctx, t, workdir, userConfigPath, "workspace", "config", "modules.aws.settings.profile")
		require.NoError(t, err)
		require.Equal(t, "alice-dev", strings.TrimSpace(string(out)))

		// Unset removes it and the effective value falls back to the repo's.
		_, err = hostDaggerUserConfigExec(ctx, t, workdir, userConfigPath, "workspace", "config", "-g", "-u", "modules.aws.settings.profile")
		require.NoError(t, err)
		out, err = hostDaggerUserConfigExec(ctx, t, workdir, userConfigPath, "workspace", "config", "modules.aws.settings.profile")
		require.NoError(t, err)
		require.Equal(t, "shared", strings.TrimSpace(string(out)))
	})

	t.Run("workspace config --global with --env targets a personal env", func(ctx context.Context, t *testctx.T) {
		workdir, userConfigPath := newUserConfigWorkdir(ctx, t,
			"git@github.com:acme/user-config-test.git", "")

		_, err := hostDaggerUserConfigExec(ctx, t, workdir, userConfigPath, "--env=dev", "workspace", "config", "-g", "modules.aws.settings.region", "us-west-2")
		require.NoError(t, err)

		out, err := hostDaggerUserConfigExec(ctx, t, workdir, userConfigPath, "--env=dev", "workspace", "config", "modules.aws.settings.region")
		require.NoError(t, err)
		require.Equal(t, "us-west-2", strings.TrimSpace(string(out)))

		// Without the env selected, the base value still applies.
		out, err = hostDaggerUserConfigExec(ctx, t, workdir, userConfigPath, "workspace", "config", "modules.aws.settings.region")
		require.NoError(t, err)
		require.Equal(t, "us-east-1", strings.TrimSpace(string(out)))
	})

	t.Run("workspace config --global rejects reads and non-settings keys", func(ctx context.Context, t *testctx.T) {
		workdir, userConfigPath := newUserConfigWorkdir(ctx, t,
			"git@github.com:acme/user-config-test.git", "")

		_, err := hostDaggerUserConfigExec(ctx, t, workdir, userConfigPath, "workspace", "config", "-g", "modules.aws.settings.profile")
		require.Error(t, err)
		requireErrOut(t, err, "--global writes to user-level config")

		_, err = hostDaggerUserConfigExec(ctx, t, workdir, userConfigPath, "workspace", "config", "-g", "modules.aws.source", "elsewhere")
		require.Error(t, err)
		requireErrOut(t, err, "cannot be stored in user-level config")
	})

	t.Run("workspace config --global requires a git remote", func(ctx context.Context, t *testctx.T) {
		workdir, userConfigPath := newUserConfigWorkdir(ctx, t, "", "")

		_, err := hostDaggerUserConfigExec(ctx, t, workdir, userConfigPath, "workspace", "config", "-g", "modules.aws.settings.profile", "alice-dev")
		require.Error(t, err)
		requireErrOut(t, err, "user-level config is keyed by the workspace's git remote")
	})

	t.Run("settings --global set, read back, unset", func(ctx context.Context, t *testctx.T) {
		// [env.dev] exists in repo config: like repository-side env-scoped
		// settings writes, --env with settings requires the env to exist
		// (create one from scratch with `workspace config -g env.<name>...`).
		workdir := newWorkspaceSettingsWorkdir(ctx, t, `[modules.aws]
source = "modules/aws"
entrypoint = true

[modules.aws.settings]
region = "us-east-1"

[env.dev]
`, workspaceSettingsAWSModule("modules/aws", "aws"))
		gitCmd := exec.Command("git", "remote", "add", "origin", "git@github.com:acme/user-config-test.git")
		gitCmd.Dir = workdir
		gitOutput, err := gitCmd.CombinedOutput()
		require.NoError(t, err, string(gitOutput))
		userConfigPath := filepath.Join(t.TempDir(), "config.toml")

		_, err = hostDaggerUserConfigExec(ctx, t, workdir, userConfigPath, "settings", "-g", "aws", "region", "us-west-2")
		require.NoError(t, err)

		userConfig, err := os.ReadFile(userConfigPath)
		require.NoError(t, err)
		require.Contains(t, string(userConfig), `region = "us-west-2"`)

		out, err := hostDaggerUserConfigExec(ctx, t, workdir, userConfigPath, "settings", "aws", "region")
		require.NoError(t, err)
		require.Equal(t, "us-west-2", strings.TrimSpace(string(out)))

		// --env scoping composes with --global.
		_, err = hostDaggerUserConfigExec(ctx, t, workdir, userConfigPath, "--env=dev", "settings", "-g", "aws", "region", "eu-west-1")
		require.NoError(t, err)
		out, err = hostDaggerUserConfigExec(ctx, t, workdir, userConfigPath, "--env=dev", "settings", "aws", "region")
		require.NoError(t, err)
		require.Equal(t, "eu-west-1", strings.TrimSpace(string(out)))

		// Unset targets only the user-level value.
		_, err = hostDaggerUserConfigExec(ctx, t, workdir, userConfigPath, "settings", "-g", "-u", "aws", "region")
		require.NoError(t, err)
		out, err = hostDaggerUserConfigExec(ctx, t, workdir, userConfigPath, "settings", "aws", "region")
		require.NoError(t, err)
		require.Equal(t, "us-east-1", strings.TrimSpace(string(out)))

		// A read invocation with --global is rejected.
		_, err = hostDaggerUserConfigExec(ctx, t, workdir, userConfigPath, "settings", "-g", "aws", "region")
		require.Error(t, err)
		requireErrOut(t, err, "--global stores a setting in user-level config")
	})
}
