package daggercmd

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/workspace"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestModuleMaxCommandTree(t *testing.T) {
	require.Equal(t, []string{"mod"}, moduleCmd.Aliases)
	for _, name := range []string{"client", "init", "install", "list", "search", "settings", "uninstall", "update"} {
		require.NotNil(t, findCommand(moduleCmd, name), name)
	}
	for _, name := range []string{"add", "list", "rm", "scope"} {
		require.NotNil(t, findCommand(moduleClientCmd, name), name)
	}
	for _, name := range []string{"activity", "config", "config-file", "cwd", "remote", "remotes", "root", "update"} {
		require.NotNil(t, findCommand(workspaceCmd, name), name)
	}
	for _, name := range []string{"list", "scope"} {
		require.NotNil(t, findCommand(sdkCmd, name), name)
	}
	for _, name := range []string{"is-module", "list", "name", "sdk"} {
		require.NotNil(t, findCommand(sdkScopeCmd, name), name)
	}

	require.Nil(t, moduleInitCmd.Flags().Lookup("sdk"), "module init names its SDK positionally")
	require.Nil(t, moduleClientAddCmd.Flags().Lookup("sdk"), "module client add names its SDK as a subcommand")
	require.Empty(t, moduleInitCmd.Example)

	nameFlag := moduleInitCmd.PersistentFlags().Lookup("name")
	require.NotNil(t, nameFlag)
	require.Equal(t, "n", nameFlag.Shorthand)
	require.NotNil(t, moduleInitCmd.PersistentFlags().Lookup("path"))

	workspaceUpdate := findCommand(workspaceCmd, "update")
	require.NotNil(t, workspaceUpdate.Flags().Lookup("no-generate"))
}

func TestModuleInitCustomPathMessage(t *testing.T) {
	require.Empty(t, moduleInitCustomPathMessage(""))
	require.Equal(t, `Initialized module foo/bar/baz
Custom path; module was not installed.
`, moduleInitCustomPathMessage("foo/bar/baz"))
}

func TestModuleSDKCommandSelection(t *testing.T) {
	for _, test := range []struct {
		args    []string
		wantSDK string
		want    bool
	}{
		{args: []string{"module", "init"}, want: true},
		{args: []string{"mod", "init"}, want: true},
		{args: []string{"module", "init", "go"}, wantSDK: "go", want: true},
		{args: []string{"module", "client", "add", "go", "database"}, wantSDK: "go", want: true},
		{args: []string{"help", "module", "client", "add"}, want: true},
		{args: []string{"module", "client", "list"}, want: false},
		{args: []string{"workspace", "update"}, want: false},
	} {
		gotSDK, got := moduleSDKCommandSelection(test.args)
		require.Equal(t, test.want, got, test.args)
		require.Equal(t, test.wantSDK, gotSDK, test.args)
	}
}

func TestModuleSDKCommandSelectionReadsStrippedFlags(t *testing.T) {
	for _, test := range []struct {
		args    []string
		wantSDK string
	}{
		{args: []string{"module", "init", "--name", "demo", "go"}, wantSDK: "go"},
		{args: []string{"module", "init", "go", "--starter", "empty"}, wantSDK: "go"},
		{args: []string{"module", "client", "add", "go", "database", "--runtime", "bun"}, wantSDK: "go"},
	} {
		sdk, ok := moduleSDKCommandSelection(parseGlobalFlags(test.args))
		require.True(t, ok, test.args)
		require.Equal(t, test.wantSDK, sdk, test.args)
	}
}

func TestSDKModuleSettingFlagsAreBare(t *testing.T) {
	cmd := &cobra.Command{Use: "init"}
	sdk := configuredSDK{
		commandName: "typescript",
		entry: workspace.ModuleEntry{
			Settings: map[string]any{"runtime": "node"},
		},
	}
	args := []*modFunctionArg{{
		Name:        "runtime",
		Description: "Runtime to use.",
		TypeDef: &modTypeDef{
			Kind: dagger.TypeDefKindStringKind,
		},
	}}
	require.NoError(t, addSDKModuleSettingFlags(cmd, sdk, args))
	flag := cmd.Flags().Lookup("runtime")
	require.NotNil(t, flag)
	require.Equal(t, "node", flag.DefValue)
	require.Equal(t, []string{"typescript", "runtime"}, flag.Annotations[sdkModuleSettingAnnotation])

	require.NoError(t, cmd.Flags().Set("runtime", "bun"))
	raw, err := sdkModuleSettingsJSON(cmd, "typescript")
	require.NoError(t, err)
	require.JSONEq(t, `{"runtime":"bun"}`, raw)
}

func TestSDKModuleSettingFlagRejectsAnotherSDK(t *testing.T) {
	cmd := &cobra.Command{Use: "init"}
	sdk := configuredSDK{commandName: "go"}
	args := []*modFunctionArg{{
		Name: "compat",
		TypeDef: &modTypeDef{
			Kind: dagger.TypeDefKindBooleanKind,
		},
	}}
	require.NoError(t, addSDKModuleSettingFlags(cmd, sdk, args))
	require.NoError(t, cmd.Flags().Set("compat", "true"))
	_, err := sdkModuleSettingsJSON(cmd, "python")
	require.ErrorContains(t, err, `belongs to SDK "go"`)
}

func findCommand(parent *cobra.Command, name string) *cobra.Command {
	for _, command := range parent.Commands() {
		if command.Name() == name {
			return command
		}
	}
	return nil
}

func TestSDKModuleSettingFlagsAreBareForModuleInit(t *testing.T) {
	cmd := &cobra.Command{Use: "init"}
	sdk := configuredSDK{
		commandName: "go",
		entry: workspace.ModuleEntry{
			Settings: map[string]any{"starter": "default"},
		},
	}
	args := []*modFunctionArg{{
		Name:        "starter",
		Description: "Starter style.",
		TypeDef: &modTypeDef{
			Kind: dagger.TypeDefKindStringKind,
		},
	}}
	require.NoError(t, addSDKModuleSettingFlags(cmd, sdk, args))

	// The SDK is a subcommand, so the flag carries no SDK prefix.
	require.Nil(t, cmd.Flags().Lookup("go-starter"))
	flag := cmd.Flags().Lookup("starter")
	require.NotNil(t, flag)
	require.Equal(t, "default", flag.DefValue)
	require.Equal(t, []string{"go", "starter"}, flag.Annotations[sdkModuleSettingAnnotation])

	require.NoError(t, cmd.Flags().Set("starter", "empty"))
	raw, err := sdkModuleSettingsJSON(cmd, "go")
	require.NoError(t, err)
	require.JSONEq(t, `{"starter":"empty"}`, raw)
}

func TestModuleInitHelpListsInstalledSDKs(t *testing.T) {
	cfg := &workspace.Config{
		Modules: map[string]workspace.ModuleEntry{
			"go-sdk":     {Source: "github.com/dagger/go-sdk"},
			"python-sdk": {Source: "github.com/dagger/python-sdk"},
		},
		SDKs: map[string]workspace.SDKEntry{
			"go":     {Module: "go-sdk"},
			"python": {Module: "python-sdk"},
		},
	}
	require.NoError(t, registerModuleSDKCommandsFromConfig(context.Background(), cfg, "dagger.toml", nil, ""))
	goInit := findCommand(moduleInitCmd, "go")
	pythonInit := findCommand(moduleInitCmd, "python")
	goAdd := findCommand(moduleClientAddCmd, "go")
	pythonAdd := findCommand(moduleClientAddCmd, "python")
	t.Cleanup(func() {
		for _, cmd := range []*cobra.Command{goInit, pythonInit} {
			if cmd != nil {
				moduleInitCmd.RemoveCommand(cmd)
			}
		}
		for _, cmd := range []*cobra.Command{goAdd, pythonAdd} {
			if cmd != nil {
				moduleClientAddCmd.RemoveCommand(cmd)
			}
		}
	})
	require.NotNil(t, goInit)
	require.NotNil(t, pythonInit)
	require.NotNil(t, goAdd)
	require.NotNil(t, pythonAdd)
	require.Equal(t, "go <module>", goAdd.Use)

	for _, parent := range []*cobra.Command{moduleInitCmd, moduleClientAddCmd} {
		var out bytes.Buffer
		parent.SetOut(&out)
		require.NoError(t, parent.Help())
		parent.SetOut(nil)
		help := out.String()
		require.Contains(t, help, "AVAILABLE COMMANDS")
		require.Contains(t, strings.Fields(help), "go")
		require.Contains(t, strings.Fields(help), "python")
		require.NotContains(t, help, "Examples")
	}
}
