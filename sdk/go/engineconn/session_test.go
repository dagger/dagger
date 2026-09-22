package engineconn

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCLISessionEnvironmentChild(t *testing.T) {
	if os.Getenv("ENGINECONN_ENV_CHILD") != "1" {
		return
	}
	values := map[string]*string{}
	for _, key := range []string{"REMOVE_ME", "KEEP_ME", "EXPLICIT", "_EXPERIMENTAL_DAGGER_RUNNER_HOST"} {
		if value, ok := os.LookupEnv(key); ok {
			values[key] = &value
		} else {
			values[key] = nil
		}
	}
	data, err := json.Marshal(values)
	if err != nil {
		os.Exit(1)
	}
	if report := os.Getenv("ENGINECONN_ENV_REPORT"); report != "" {
		if os.WriteFile(report, data, 0o600) != nil {
			os.Exit(1)
		}
		fmt.Println(`{"port":12345,"session_token":"test"}`)
		_, _ = io.Copy(io.Discard, os.Stdin)
	} else {
		fmt.Println(string(data))
	}
	os.Exit(0)
}

func TestCLISessionEnvRemovesInheritedVariables(t *testing.T) {
	t.Parallel()
	inherited := []string{"REMOVE_ME=secret", "KEEP_ME=kept", "EXPLICIT=inherited", "ENGINECONN_ENV_CHILD=1"}
	cfg := &Config{UnsetEnv: []string{"REMOVE_ME", "EXPLICIT"}, ExtraEnv: []string{"EXPLICIT=allowed"}, RunnerHost: "tcp://engine:1234"}
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestCLISessionEnvironmentChild$")
	cmd.Env = cliSessionEnv(inherited, cfg)
	out, err := cmd.Output()
	require.NoError(t, err)
	var values map[string]*string
	require.NoError(t, json.Unmarshal(out, &values))
	require.Nil(t, values["REMOVE_ME"], "removed is absent, not merely empty")
	require.Equal(t, "kept", *values["KEEP_ME"])
	require.Equal(t, "allowed", *values["EXPLICIT"], "explicit extra environment wins")
	require.Equal(t, cfg.RunnerHost, *values["_EXPERIMENTAL_DAGGER_RUNNER_HOST"])
	require.Equal(t, "REMOVE_ME=secret", inherited[0], "input slice remains unchanged")
}

func TestGetPreservesUnsetEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test CLI wrapper uses sh")
	}
	if _, nested := os.LookupEnv("DAGGER_SESSION_PORT"); nested {
		t.Skip("Get intentionally prefers an existing session")
	}
	bin, err := os.Executable()
	require.NoError(t, err)
	wrapper := filepath.Join(t.TempDir(), "dagger")
	require.NoError(t, os.WriteFile(wrapper, []byte("#!/bin/sh\nexec \""+bin+"\" -test.run=^TestCLISessionEnvironmentChild$\n"), 0o700))
	t.Setenv("_EXPERIMENTAL_DAGGER_CLI_BIN", wrapper)
	t.Setenv("REMOVE_ME", "secret")
	t.Setenv("KEEP_ME", "kept")
	report := filepath.Join(t.TempDir(), "environment.json")
	conn, err := Get(t.Context(), &Config{UnsetEnv: []string{"REMOVE_ME"}, ExtraEnv: []string{"ENGINECONN_ENV_CHILD=1", "ENGINECONN_ENV_REPORT=" + report}})
	require.NoError(t, err)
	defer conn.Close()
	data, err := os.ReadFile(report)
	require.NoError(t, err)
	var values map[string]*string
	require.NoError(t, json.Unmarshal(data, &values))
	require.Nil(t, values["REMOVE_ME"], "Get must retain UnsetEnv when normalizing config")
	require.Equal(t, "kept", *values["KEEP_ME"])
	require.Equal(t, "secret", os.Getenv("REMOVE_ME"), "parent environment is untouched")
}

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
	t.Setenv("DAGGER_SESSION_PORT", "1234")
	t.Setenv("DAGGER_SESSION_TOKEN", "secret")

	_, err := Get(context.Background(), &Config{
		Workspace: "github.com/acme/ws",
	})
	require.ErrorContains(t, err, "cannot configure workspace for existing session")
}

func TestGetRejectsWorkspaceModuleLoadingForExistingSession(t *testing.T) {
	t.Setenv("DAGGER_SESSION_PORT", "1234")
	t.Setenv("DAGGER_SESSION_TOKEN", "secret")

	_, err := Get(context.Background(), &Config{
		LoadWorkspaceModules: true,
	})
	require.ErrorContains(t, err, "cannot configure workspace module loading for existing session")
}
