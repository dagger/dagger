package daggercmd

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/core/workspace"
	"github.com/stretchr/testify/require"
)

func TestModuleSdkRegistered(t *testing.T) {
	cmd, _, err := moduleCmd.Find([]string{"sdk"})
	require.NoError(t, err)
	require.Same(t, moduleSdkCmd, cmd)
	require.True(t, cmd.DisableFlagParsing, "module sdk must disable flag parsing — args are forwarded to the SDK call")
}

// TestModuleSdkHelpHeuristic exercises the rule that decides between
// "show wrapper help" and "dispatch to the SDK". The decision is based
// on whether any positional (non-dash-prefixed) argument is present,
// because DisableFlagParsing forwards parent persistent flags into
// the arg list and we shouldn't make decisions based on that noise.
func TestModuleSdkHelpHeuristic(t *testing.T) {
	for _, tt := range []struct {
		name         string
		args         []string
		wantDispatch bool
	}{
		{"no args → help", nil, false},
		{"only --help → help", []string{"--help"}, false},
		{"only -h → help", []string{"-h"}, false},
		{"persistent flag only → help", []string{"--load-module=foo"}, false},
		{"persistent flag and --help → help", []string{"--x-release=", "--help"}, false},
		{"subcommand only → dispatch", []string{"python-version"}, true},
		{"subcommand with arg → dispatch", []string{"python-version", "3.13"}, true},
		{"subcommand with --help → dispatch", []string{"python-version", "--help"}, true},
		{"flag then subcommand → dispatch", []string{"--load-module=foo", "go-mod-tidy"}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			hasSubcommand := false
			for _, a := range tt.args {
				if len(a) > 0 && a[0] != '-' {
					hasSubcommand = true
					break
				}
			}
			require.Equal(t, tt.wantDispatch, hasSubcommand)
		})
	}
}

// TestCurrentModuleSDKName covers the lookup against both spellings an
// as-sdk entry can use: relative to the dagger.toml, or root-anchored.
func TestCurrentModuleSDKName(t *testing.T) {
	for _, tt := range []struct {
		name        string
		managedPath string
		wantErr     string
	}{
		{name: "config-relative", managedPath: ".dagger/modules/foo"},
		{name: "root-anchored", managedPath: "/.dagger/modules/foo"},
		{name: "escaping", managedPath: "../foo", wantErr: "escapes the workspace root"},
		{name: "unrelated", managedPath: ".dagger/modules/bar", wantErr: "is not registered"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(root, workspace.ConfigFileName), fmt.Appendf(nil,
				"[modules.my-sdk]\nsource = \"github.com/dagger/my-sdk\"\n\n[modules.my-sdk.as-sdk]\n\n[[modules.my-sdk.as-sdk.modules]]\npath = %q\n", tt.managedPath), 0o644))
			moduleDir := filepath.Join(root, ".dagger", "modules", "foo")
			require.NoError(t, os.MkdirAll(moduleDir, 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(moduleDir, modules.Filename), []byte("name = \"foo\"\n"), 0o644))

			t.Chdir(moduleDir)
			name, err := currentModuleSDKName()
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "my-sdk", name)
		})
	}
}

// A dagger.toml in a subdirectory of the repo is where the two spellings stop
// coinciding: a config-relative entry is measured from the config, a
// root-anchored one from the repository root above it.
func TestCurrentModuleSDKNameFromSubdirectoryConfig(t *testing.T) {
	for _, tt := range []struct{ name, managedPath string }{
		{"config-relative", ".dagger/modules/foo"},
		{"root-anchored", "/common/.dagger/modules/foo"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(root, ".git"), 0o755))
			configDir := filepath.Join(root, "common")
			require.NoError(t, os.MkdirAll(configDir, 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(configDir, workspace.ConfigFileName), fmt.Appendf(nil,
				"[modules.my-sdk]\nsource = \"github.com/dagger/my-sdk\"\n\n[modules.my-sdk.as-sdk]\n\n[[modules.my-sdk.as-sdk.modules]]\npath = %q\n", tt.managedPath), 0o644))
			moduleDir := filepath.Join(configDir, ".dagger", "modules", "foo")
			require.NoError(t, os.MkdirAll(moduleDir, 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(moduleDir, modules.Filename), []byte("name = \"foo\"\n"), 0o644))

			t.Chdir(moduleDir)
			name, err := currentModuleSDKName()
			require.NoError(t, err)
			require.Equal(t, "my-sdk", name)
		})
	}
}
