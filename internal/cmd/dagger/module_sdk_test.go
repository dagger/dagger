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
	for _, cmd := range []*cobra.Command{moduleClientAddCmd, moduleClientRemoveCmd} {
		require.NotNil(t, cmd.Flags().Lookup("sdk"))
		require.Empty(t, cmd.Commands())
		require.NoError(t, cmd.Args(cmd, []string{"target"}))
		require.Error(t, cmd.Args(cmd, nil))
		require.Error(t, cmd.Args(cmd, []string{"go", "target"}))
	}
	require.Empty(t, moduleInitCmd.Example)
	require.Equal(t, "init SDK [flags]", moduleInitCmd.Use)
	require.Equal(t, "Initialize a new module for development with an SDK", moduleInitCmd.Long)

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
		{args: []string{"module", "client", "add", "database"}, want: true},
		{args: []string{"module", "client", "add", "go", "--sdk=go"}, want: true},
		{args: []string{"help", "module", "client", "add"}, want: true},
		{args: []string{"module", "client", "rm", "database"}, want: true},
		{args: []string{"help", "mod", "client", "rm"}, want: true},
		{args: []string{"module", "client", "list"}, want: false},
		{args: []string{"workspace", "update"}, want: false},
	} {
		gotSDK, got := moduleSDKCommandSelection(test.args)
		require.Equal(t, test.want, got, test.args)
		require.Equal(t, test.wantSDK, gotSDK, test.args)
	}
}

func TestModuleSDKCommandSelectionReadsStrippedFlags(t *testing.T) {
	root := testRootCommand()
	oldAutoApply := autoApply
	oldAddSDK, oldRemoveSDK := moduleClientAddSDK, moduleClientRemoveSDK
	t.Cleanup(func() {
		autoApply = oldAutoApply
		moduleClientAddSDK, moduleClientRemoveSDK = oldAddSDK, oldRemoveSDK
	})

	for _, test := range []struct {
		args    []string
		wantSDK string
	}{
		{args: []string{"--auto-apply", "module", "init", "go", "--name", "sdk-smoke", "--path", ".dagger/modules/sdk-smoke"}, wantSDK: "go"},
		{args: []string{"module", "init", "--name", "demo", "go"}, wantSDK: "go"},
		{args: []string{"module", "init", "go", "--starter", "empty"}, wantSDK: "go"},
		{args: []string{"module", "client", "add", "database", "--sdk=go"}},
		{args: []string{"module", "client", "add", "--sdk", "go", "database"}},
		{args: []string{"module", "client", "rm", "go", "--sdk=go"}},
	} {
		sdk, ok := moduleSDKCommandSelection(parseGlobalFlags(root, test.args))
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

func TestSDKModuleSettingFlagsKeepTypes(t *testing.T) {
	cmd := &cobra.Command{Use: "init"}
	sdk := configuredSDK{commandName: "test"}
	args := []*modFunctionArg{
		{Name: "boolean", TypeDef: &modTypeDef{Kind: dagger.TypeDefKindBooleanKind}},
		{Name: "integer", TypeDef: &modTypeDef{Kind: dagger.TypeDefKindIntegerKind}},
		{Name: "float", TypeDef: &modTypeDef{Kind: dagger.TypeDefKindFloatKind}},
		{Name: "booleans", TypeDef: &modTypeDef{
			Kind: dagger.TypeDefKindListKind,
			AsList: &modList{
				ElementTypeDef: &modTypeDef{Kind: dagger.TypeDefKindBooleanKind},
			},
		}},
		{Name: "integers", TypeDef: &modTypeDef{
			Kind: dagger.TypeDefKindListKind,
			AsList: &modList{
				ElementTypeDef: &modTypeDef{Kind: dagger.TypeDefKindIntegerKind},
			},
		}},
		{Name: "floats", TypeDef: &modTypeDef{
			Kind: dagger.TypeDefKindListKind,
			AsList: &modList{
				ElementTypeDef: &modTypeDef{Kind: dagger.TypeDefKindFloatKind},
			},
		}},
	}
	require.NoError(t, addSDKModuleSettingFlags(cmd, sdk, args))
	require.NoError(t, cmd.Flags().Set("boolean", "true"))
	require.NoError(t, cmd.Flags().Set("integer", "42"))
	require.NoError(t, cmd.Flags().Set("float", "1.5"))
	require.NoError(t, cmd.Flags().Set("booleans", "true,false"))
	require.NoError(t, cmd.Flags().Set("integers", "1,2"))
	require.NoError(t, cmd.Flags().Set("floats", "1.5,2.5"))

	raw, err := sdkModuleSettingsJSON(cmd, "test")
	require.NoError(t, err)
	require.JSONEq(t, `{
		"boolean": true,
		"integer": 42,
		"float": 1.5,
		"booleans": [true, false],
		"integers": [1, 2],
		"floats": [1.5, 2.5]
	}`, raw)
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
	addSDKUsage := moduleClientAddCmd.Flags().Lookup("sdk").Usage
	removeSDKUsage := moduleClientRemoveCmd.Flags().Lookup("sdk").Usage
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
	t.Cleanup(func() {
		for _, cmd := range []*cobra.Command{goInit, pythonInit} {
			if cmd != nil {
				moduleInitCmd.RemoveCommand(cmd)
			}
		}
		moduleClientAddCmd.Flags().Lookup("sdk").Usage = addSDKUsage
		moduleClientRemoveCmd.Flags().Lookup("sdk").Usage = removeSDKUsage
	})
	require.NotNil(t, goInit)
	require.NotNil(t, pythonInit)
	require.Empty(t, moduleClientAddCmd.Commands())

	var out bytes.Buffer
	moduleInitCmd.SetOut(&out)
	require.NoError(t, moduleInitCmd.Help())
	moduleInitCmd.SetOut(nil)
	help := out.String()
	require.Contains(t, help, "Initialize a new module for development with an SDK")
	require.Contains(t, help, "dagger module init SDK [flags]")
	require.Contains(t, help, "AVAILABLE SDKs")
	require.NotContains(t, help, "AVAILABLE COMMANDS")
	require.Contains(t, strings.Fields(help), "go")
	require.Contains(t, strings.Fields(help), "python")
	require.NotContains(t, help, "Examples")

	for _, cmd := range []*cobra.Command{moduleClientAddCmd, moduleClientRemoveCmd} {
		out.Reset()
		cmd.SetOut(&out)
		require.NoError(t, cmd.Help())
		cmd.SetOut(nil)
		help = out.String()
		require.NotContains(t, help, "AVAILABLE COMMANDS")
		require.Contains(t, help, "--sdk SDK")
		require.Contains(t, help, "available: go, python")
		require.NotContains(t, help, "SDK settings")
	}
}

func TestModuleClientHelpWithoutInstalledSDKs(t *testing.T) {
	addSDKUsage := moduleClientAddCmd.Flags().Lookup("sdk").Usage
	removeSDKUsage := moduleClientRemoveCmd.Flags().Lookup("sdk").Usage
	t.Cleanup(func() {
		moduleClientAddCmd.Flags().Lookup("sdk").Usage = addSDKUsage
		moduleClientRemoveCmd.Flags().Lookup("sdk").Usage = removeSDKUsage
	})
	for _, cfg := range []*workspace.Config{nil, {}} {
		require.NoError(t, registerModuleSDKCommandsFromConfig(context.Background(), cfg, "dagger.toml", nil, ""))
		for _, cmd := range []*cobra.Command{moduleClientAddCmd, moduleClientRemoveCmd} {
			var out bytes.Buffer
			cmd.SetOut(&out)
			require.NoError(t, cmd.Help())
			cmd.SetOut(nil)
			require.Contains(t, out.String(), "no SDKs installed")
			require.NotContains(t, out.String(), "AVAILABLE COMMANDS")
		}
	}
}

func TestModuleInitHelpWithoutInstalledSDKs(t *testing.T) {
	root := &cobra.Command{Use: "dagger"}
	root.SetUsageTemplate(usageTemplate)
	module := &cobra.Command{Use: "module"}
	cmd := &cobra.Command{
		Use:                   moduleInitCmd.Use,
		Long:                  moduleInitCmd.Long,
		DisableFlagsInUseLine: true,
		Annotations:           moduleInitCmd.Annotations,
		Run:                   func(*cobra.Command, []string) {},
	}
	root.AddCommand(module)
	module.AddCommand(cmd)

	var out bytes.Buffer
	cmd.SetOut(&out)
	require.NoError(t, cmd.Help())
	help := out.String()
	require.Contains(t, help, "dagger module init SDK [flags]")
	require.Contains(t, help, "NO AVAILABLE SDKs. In doubt, try 'dagger mod install github.com/dagger/dang-sdk'")
	require.NotContains(t, help, "AVAILABLE COMMANDS")
}
