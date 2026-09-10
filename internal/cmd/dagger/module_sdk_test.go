package daggercmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
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

func TestModuleInitFlagCommandPosition(t *testing.T) {
	for _, test := range []struct {
		name       string
		args       []string
		settings   bool
		modulePath string
		sdkPath    string
	}{
		{name: "parent flag", args: []string{"--path=src", "custom"}, settings: true, modulePath: "src"},
		{name: "SDK flag", args: []string{"custom", "--path=assets"}, settings: true, sdkPath: "assets"},
		{name: "both flags", args: []string{"--path", "src", "custom", "--path", "assets"}, settings: true, modulePath: "src", sdkPath: "assets"},
		{name: "inherited flag", args: []string{"custom", "--path=src"}, modulePath: "src"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := &cobra.Command{Use: "dagger", TraverseChildren: rootCmd.TraverseChildren}
			module := &cobra.Command{Use: "module"}
			init := &cobra.Command{Use: "init"}
			var modulePath string
			init.PersistentFlags().StringVar(&modulePath, "path", "", "Module path")
			root.AddCommand(module)
			module.AddCommand(init)
			argv := append([]string{"module", "init"}, test.args...)
			// Main performs this pass before it can inspect the SDK constructor.
			sdkName, ok := moduleSDKCommandSelection(parseGlobalFlags(root, argv))
			require.True(t, ok)
			require.Equal(t, "custom", sdkName)
			require.Empty(t, modulePath)

			var args []*modFunctionArg
			if test.settings {
				args = []*modFunctionArg{{Name: "path", TypeDef: &modTypeDef{Kind: dagger.TypeDefKindStringKind, Optional: true}}}
			}
			cmd, err := newSDKModuleInitCommand(configuredSDK{commandName: sdkName}, args)
			require.NoError(t, err)
			cmd.RunE = func(cmd *cobra.Command, _ []string) error {
				require.Equal(t, test.modulePath, modulePath)
				if test.settings {
					value, err := cmd.Flags().GetString("path")
					require.NoError(t, err)
					require.Equal(t, test.sdkPath, value)
				}
				return nil
			}
			init.AddCommand(cmd)
			root.SetArgs(argv)
			require.NoError(t, root.Execute())
		})
	}
}

func TestModuleInitGlobalFlagDiscovery(t *testing.T) {
	oldWorkdir, oldWorkspace, oldDebug, oldRelease := workdir, workspaceRef, debugFlag, xRelease
	t.Cleanup(func() { workdir, workspaceRef, debugFlag, xRelease = oldWorkdir, oldWorkspace, oldDebug, oldRelease })
	for _, test := range []struct {
		name          string
		prefix        []string
		suffix        []string
		settings      []string
		nextSettings  []string
		missing       bool
		wantDir       string
		wantWorkspace string
		wantDebug     bool
		wantRelease   string
		wantCalls     int
		wantError     string
	}{
		{name: "SDK workdir", suffix: []string{"--workdir=assets"}, settings: []string{"workdir"}, wantCalls: 1},
		{name: "global and SDK workdir", prefix: []string{"--workdir=src"}, suffix: []string{"--workdir=assets"}, settings: []string{"workdir"}, wantDir: "src", wantCalls: 1},
		{name: "SDK workspace", suffix: []string{"--workspace=assets"}, settings: []string{"workspace"}, wantCalls: 1},
		{name: "inherited workspace", suffix: []string{"--workspace=target"}, wantWorkspace: "target", wantCalls: 2},
		{name: "SDK debug string", suffix: []string{"--debug=assets"}, settings: []string{"debug"}, wantCalls: 1},
		{name: "global debug and SDK debug string", prefix: []string{"--debug"}, suffix: []string{"--debug=assets"}, settings: []string{"debug"}, wantDebug: true, wantCalls: 1},
		{name: "inherited debug", suffix: []string{"--debug"}, wantDebug: true, wantCalls: 1},
		{name: "inherited x-release", suffix: []string{"--x-release=next"}, wantRelease: "next", wantCalls: 1},
		{name: "inherited workdir", suffix: []string{"--workdir=target"}, wantDir: "target", wantCalls: 2},
		{name: "relative workdir keeps invocation base", prefix: []string{"--workdir=src"}, suffix: []string{"--workdir=target"}, wantDir: "target", wantCalls: 2},
		{name: "counters and repeatable flags", prefix: []string{"-v", "--label=first"}, suffix: []string{"-v", "--label=second", "--workdir=target"}, wantDir: "target", wantCalls: 2},
		{name: "SDK absent initially", suffix: []string{"--workdir=target"}, missing: true, wantCalls: 1, wantError: "the SDK is absent from the initial workspace"},
		{name: "different settings after context change", suffix: []string{"--workdir=target"}, nextSettings: []string{"workdir"}, wantCalls: 2, wantError: "different settings"},
	} {
		t.Run(test.name, func(t *testing.T) {
			invocationDir := t.TempDir()
			t.Chdir(invocationDir)
			require.NoError(t, os.Mkdir("src", 0o755))
			require.NoError(t, os.Mkdir("target", 0o755))
			workdir, workspaceRef, debugFlag, xRelease = ".", "", false, ""
			t.Setenv(daggerXReleaseEnv, "")
			root := &cobra.Command{Use: "dagger", TraverseChildren: true}
			root.PersistentFlags().StringVar(&workdir, "workdir", ".", "Workdir")
			root.PersistentFlags().StringVar(&workspaceRef, "workspace", "", "Workspace")
			root.PersistentFlags().BoolVar(&debugFlag, "debug", false, "Debug")
			root.PersistentFlags().StringVar(&xRelease, "x-release", "", "Release")
			var verbose int
			var labels []string
			root.PersistentFlags().CountVarP(&verbose, "verbose", "v", "Verbosity")
			root.PersistentFlags().StringSliceVar(&labels, "label", nil, "Labels")
			module := &cobra.Command{Use: "module"}
			init := &cobra.Command{Use: "init"}
			root.AddCommand(module)
			module.AddCommand(init)
			argv := append(append(append([]string{}, test.prefix...), "module", "init", "custom"), test.suffix...)
			remaining := parseGlobalFlags(root, argv)
			// Main changes directory before inspecting the SDK. Its setting
			// value must not affect that first directory selection.
			initialDir, err := NormalizeWorkdir(workdir)
			require.NoError(t, err)
			require.NoError(t, os.Chdir(initialDir))
			workdir = initialDir
			calls := 0
			err = prepareModuleSDKCommands(context.Background(), root, remaining, invocationDir, func(_ context.Context, sdk string) error {
				calls++
				require.Equal(t, "custom", sdk)
				for _, cmd := range init.Commands() {
					init.RemoveCommand(cmd)
				}
				if test.missing {
					return nil
				}
				settings := test.settings
				if calls == 2 && test.nextSettings != nil {
					settings = test.nextSettings
				}
				var args []*modFunctionArg
				for _, name := range settings {
					args = append(args, &modFunctionArg{Name: name, TypeDef: &modTypeDef{Kind: dagger.TypeDefKindStringKind, Optional: true}})
				}
				cmd, err := newSDKModuleInitCommand(configuredSDK{commandName: sdk}, args)
				require.NoError(t, err)
				cmd.RunE = func(cmd *cobra.Command, _ []string) error {
					for _, name := range settings {
						value, err := cmd.Flags().GetString(name)
						require.NoError(t, err)
						require.Equal(t, "assets", value)
					}
					return nil
				}
				init.AddCommand(cmd)
				return nil
			})
			require.Equal(t, test.wantCalls, calls)
			if test.wantError != "" {
				require.ErrorContains(t, err, test.wantError)
				require.ErrorContains(t, err, "dagger --workdir=<value> module init custom")
				return
			}
			require.NoError(t, err)
			cwd, err := os.Getwd()
			require.NoError(t, err)
			require.Equal(t, filepath.Join(invocationDir, test.wantDir), cwd)
			require.Equal(t, test.wantDebug, debugFlag)
			require.Equal(t, test.wantWorkspace, workspaceRef)
			require.Equal(t, test.wantRelease, xRelease)
			if test.name == "counters and repeatable flags" {
				require.Equal(t, 2, verbose)
				require.Equal(t, []string{"first", "second"}, labels)
			}
			replayGlobalFlags(root)
			root.SetArgs(argv)
			require.NoError(t, root.Execute())
			if test.name == "counters and repeatable flags" {
				require.Equal(t, 2, verbose)
				require.Equal(t, []string{"first", "second"}, labels)
			}
		})
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
