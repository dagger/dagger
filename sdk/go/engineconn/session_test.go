package engineconn

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCLISessionArgsIncludeWorkspace verifies that session provisioning forwards
// workspace selection as a dedicated CLI flag.
func TestCLISessionArgsIncludeWorkspace(t *testing.T) {
	t.Parallel()

	args := cliSessionArgs(&Config{
		Workspace: "github.com/acme/ws",
	})

	require.Contains(t, args, "--workspace")
	require.Contains(t, args, "github.com/acme/ws")
}

func TestCLISessionArgsIncludeLoadWorkspaceModules(t *testing.T) {
	t.Parallel()

	args := cliSessionArgs(&Config{
		LoadWorkspaceModules: true,
	})

	require.Contains(t, args, "--load-workspace-modules")
	require.NotContains(t, args, "--skip-workspace-modules")
}

// TestGetRejectsWorkspaceForExistingSession verifies that an existing session's
// workspace binding cannot be overridden by client config.
func TestGetRejectsWorkspaceForExistingSession(t *testing.T) {
	clearEngineSelection(t)
	t.Setenv("DAGGER_SESSION_PORT", "1234")
	t.Setenv("DAGGER_SESSION_TOKEN", "secret")

	_, err := Get(context.Background(), &Config{
		Workspace: "github.com/acme/ws",
	})
	require.ErrorContains(t, err, "cannot configure workspace for existing session")
}

func TestGetRejectsWorkspaceModuleLoadingForExistingSession(t *testing.T) {
	clearEngineSelection(t)
	t.Setenv("DAGGER_SESSION_PORT", "1234")
	t.Setenv("DAGGER_SESSION_TOKEN", "secret")

	_, err := Get(context.Background(), &Config{
		LoadWorkspaceModules: true,
	})
	require.ErrorContains(t, err, "cannot configure workspace module loading for existing session")
}

// clearEngineSelection keeps the test runner's own engine selection, if any,
// from changing which connection Get chooses.
func clearEngineSelection(t *testing.T) {
	t.Helper()
	for _, key := range engineSelectionEnv {
		t.Setenv(key, "")
	}
}

func TestGetUsesNestedSession(t *testing.T) {
	clearEngineSelection(t)
	t.Setenv("DAGGER_SESSION_PORT", "1234")
	t.Setenv("DAGGER_SESSION_TOKEN", "secret")

	conn, err := Get(context.Background(), &Config{})
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1:1234", conn.Host())
}

// TestGetPrefersExplicitEngineOverNestedSession verifies that an explicitly
// selected engine bypasses the session a Dagger exec injects, and that the
// CLI started for it does not inherit that session.
func TestGetPrefersExplicitEngineOverNestedSession(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		cfg  Config
	}{
		{name: "runner host env", env: map[string]string{"_EXPERIMENTAL_DAGGER_RUNNER_HOST": "tcp://dev-engine:1234"}},
		{name: "engine env", env: map[string]string{"DAGGER_ENGINE": "tcp://dev-engine:1234"}},
		{name: "cloud engine env", env: map[string]string{"DAGGER_CLOUD_ENGINE": "true"}},
		{name: "runner host option", cfg: Config{RunnerHost: "tcp://dev-engine:1234"}},
		{name: "extra env", cfg: Config{ExtraEnv: []string{"DAGGER_ENGINE=tcp://dev-engine:1234"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearEngineSelection(t)
			t.Setenv("DAGGER_SESSION_PORT", "1234")
			t.Setenv("DAGGER_SESSION_TOKEN", "secret")
			for k, v := range tc.env {
				t.Setenv(k, v)
			}

			// A fake CLI that records its environment and fails.
			dir := t.TempDir()
			envFile := filepath.Join(dir, "env")
			cli := filepath.Join(dir, "dagger")
			script := "#!/bin/sh\nenv > " + envFile + "\nexit 1\n"
			require.NoError(t, os.WriteFile(cli, []byte(script), 0o700))
			t.Setenv("_EXPERIMENTAL_DAGGER_CLI_BIN", cli)

			cfg := tc.cfg
			cfg.Workdir = dir
			_, err := Get(context.Background(), &cfg)
			require.Error(t, err)
			require.NotContains(t, err.Error(), "existing session")

			env, err := os.ReadFile(envFile)
			require.NoError(t, err)
			for _, kv := range strings.Split(string(env), "\n") {
				key, _, _ := strings.Cut(kv, "=")
				require.NotContains(t, []string{"DAGGER_SESSION_PORT", "DAGGER_SESSION_TOKEN"}, key)
			}
		})
	}
}
