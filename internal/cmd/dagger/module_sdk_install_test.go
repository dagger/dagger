package daggercmd

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSDKInstallDefersConstructorInspection(t *testing.T) {
	preserveSDKCommandHelp(t)
	previous := moduleInitInstallSDK
	moduleInitInstallSDK = true
	t.Cleanup(func() { moduleInitInstallSDK = previous })
	// A nil engine client proves that registering a not-installed SDK never
	// fetches its source or writes dependency pins during flag discovery.
	require.NoError(t, registerModuleSDKCommandsFromConfig(context.Background(), nil, "", nil, "go"))
	cmd := findCommand(moduleInitCmd, "go")
	require.NotNil(t, cmd)
	err := cmd.ParseFlags([]string{"--starter=empty"})
	require.Error(t, err)
	err = cmd.FlagErrorFunc()(cmd, err)
	require.ErrorContains(t, err, "SDK setting flags require installing the SDK first")
	require.ErrorContains(t, err, "dagger module install github.com/dagger/go-sdk --name dagger-go-sdk")
	require.ErrorContains(t, err, "dagger module init go [flags]")
}

func TestSDKInstallSettingsFailBeforeDiscovery(t *testing.T) {
	executable, err := os.Executable()
	require.NoError(t, err)
	for _, config := range []string{"", "[modules.other]\nsource = 'github.com/acme/other'\n"} {
		for _, choice := range []string{"-y", "--no-apply"} {
			workdir := t.TempDir()
			if config != "" {
				require.NoError(t, os.WriteFile(filepath.Join(workdir, "dagger.toml"), []byte(config), 0o600))
			}
			args, err := json.Marshal([]string{"module", "init", "go", "--install-sdk", choice, "--template=empty"})
			require.NoError(t, err)
			cmd := exec.CommandContext(t.Context(), executable, "-test.run=^TestSDKInstallAgentBeforeDiscovery$")
			cmd.Dir = workdir
			cmd.Env = append(os.Environ(), "DAGGER_TEST_SDK_INSTALL_ARGS="+string(args), "AI_AGENT=test", "DAGGER_ENGINE=unix:///nonexistent-sdk-install-engine.sock")
			out, err := cmd.CombinedOutput()
			require.Error(t, err)
			require.Contains(t, string(out), "SDK setting flags require installing the SDK first")
			require.Contains(t, string(out), "dagger module init go [flags]")
			require.NotContains(t, string(out), "connect")
			entries, err := os.ReadDir(workdir)
			require.NoError(t, err)
			if config == "" {
				require.Empty(t, entries)
			} else {
				require.Len(t, entries, 1)
				data, err := os.ReadFile(filepath.Join(workdir, "dagger.toml"))
				require.NoError(t, err)
				require.Equal(t, config, string(data))
			}
		}
	}
}

func TestSDKInstallAgentBeforeDiscovery(t *testing.T) {
	const argsEnv = "DAGGER_TEST_SDK_INSTALL_ARGS"
	if encoded := os.Getenv(argsEnv); encoded != "" {
		var args []string
		require.NoError(t, json.Unmarshal([]byte(encoded), &args))
		os.Args = append([]string{"dagger"}, args...)
		Main()
		return
	}
	executable, err := os.Executable()
	require.NoError(t, err)
	for _, tc := range []struct {
		name, config string
		args         []string
	}{
		{"fresh", "", []string{"module", "init", "go", "--install-sdk"}},
		{"existing config without SDK", "[modules.other]\nsource = 'github.com/acme/other'\n", []string{"module", "init", "go", "--install-sdk"}},
		{"before SDK selection", "[modules.other]\nsource = 'github.com/acme/other'\n", []string{"mod", "init", "--install-sdk", "go"}},
		{"before registry settings", "", []string{"module", "init", "go", "--install-sdk", "--starter=empty"}},
		{"before remote workspace", "", []string{"-W", "github.com/acme/nonexistent", "module", "init", "go", "--install-sdk"}},
		{"before trailing remote workspace", "", []string{"module", "init", "go", "--install-sdk", "-W", "github.com/acme/nonexistent"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workdir := t.TempDir()
			if tc.config != "" {
				require.NoError(t, os.WriteFile(filepath.Join(workdir, "dagger.toml"), []byte(tc.config), 0o600))
			}
			encoded, err := json.Marshal(tc.args)
			require.NoError(t, err)
			cmd := exec.CommandContext(t.Context(), executable, "-test.run=^TestSDKInstallAgentBeforeDiscovery$")
			cmd.Dir = workdir
			cmd.Env = append(os.Environ(), argsEnv+"="+string(encoded), "AI_AGENT=test", "DAGGER_ENGINE=unix:///nonexistent-sdk-install-engine.sock")
			out, err := cmd.CombinedOutput()
			require.Error(t, err)
			require.Contains(t, string(out), "requires an explicit changeset choice when run by a coding agent")
			require.Contains(t, string(out), "--no-apply")
			require.NotContains(t, string(out), "connect")
		})
	}
}

func TestSDKInstallChoicePreservesOtherInvocations(t *testing.T) {
	previous := moduleInitInstallSDK
	t.Cleanup(func() { moduleInitInstallSDK = previous })
	root := testRootCommand()
	for _, args := range [][]string{
		{"module", "init", "go", "--starter=empty", "--no-apply=SDK-setting"},
		{"module", "init", "go", "--install-sdk", "--no-apply"},
		{"module", "init", "--install-sdk", "go", "-y"},
		{"-y", "module", "init", "go", "--install-sdk"},
		{"module", "init", "go", "--install-sdk", "--auto-apply"},
		{"module", "init", "go", "--install-sdk=false"},
		{"module", "init", "--help", "--install-sdk", "-y"},
	} {
		require.NoError(t, prepareModuleSDKInstall(root, args), args)
	}
}

func TestSDKInstallUnknownAndAddressErrors(t *testing.T) {
	previous := moduleInitInstallSDK
	t.Cleanup(func() { moduleInitInstallSDK = previous })
	catalog, err := classifyWorkspaceSDKs(nil)
	require.NoError(t, err)
	for _, name := range []string{"missing", "github.com/dagger/go-sdk", "github.com/acme/sdk@v1"} {
		moduleInitInstallSDK = false
		_, withoutInstall := resolveSDKInit(catalog, name)
		moduleInitInstallSDK = true
		_, withInstall := resolveSDKInit(catalog, name)
		require.EqualError(t, withInstall, withoutInstall.Error())
	}
}
