package daggercmd

import (
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

	cmd, args, err := rootCmd.Find([]string{"sdk"})
	require.NoError(t, err)
	require.Same(t, rootCmd, cmd)
	require.Equal(t, []string{"sdk"}, args)

	require.Nil(t, moduleInitCmd.Flags().Lookup("sdk"), "module init names its SDK positionally")

	nameFlag := moduleInitCmd.Flags().Lookup("name")
	require.NotNil(t, nameFlag)
	require.Equal(t, "n", nameFlag.Shorthand)
	require.NotNil(t, moduleInitCmd.Flags().Lookup("path"))

	workspaceUpdate := findCommand(workspaceCmd, "update")
	require.NotNil(t, workspaceUpdate.Flags().Lookup("no-generate"))
}

func TestModuleInitCustomPathMessage(t *testing.T) {
	require.Empty(t, moduleInitCustomPathMessage(""))
	require.Equal(t, `Initialized module foo/bar/baz
Custom path; module was not installed.
`, moduleInitCustomPathMessage("foo/bar/baz"))
}

func TestModuleSDKSettingsRegistrationArgs(t *testing.T) {
	for _, test := range []struct {
		args    []string
		rawArgs []string
		want    bool
	}{
		{args: []string{"module", "init"}, rawArgs: []string{"module", "init", "--help"}, want: true},
		{args: []string{"mod", "init"}, want: false},
		{args: []string{"module", "init", "go"}, rawArgs: []string{"module", "init", "go", "--starter=empty"}, want: true},
		{args: []string{"module", "client", "add", "database"}, want: false},
		{args: []string{"module", "client", "add", "database"}, rawArgs: []string{"module", "client", "add", "database", "--typescript-runtime=bun"}, want: true},
		{args: []string{"help", "module", "client", "add"}, want: true},
		{args: []string{"module", "client", "list"}, want: false},
		{args: []string{"workspace", "update"}, want: false},
	} {
		require.Equal(t, test.want, moduleSDKSettingsRegistrationArgs(test.args, test.rawArgs), test.args)
	}
}

func TestSDKModuleSettingFlagsAreNamespaced(t *testing.T) {
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
	require.NoError(t, addSDKModuleSettingFlags(cmd, sdk, args, true))
	flag := cmd.Flags().Lookup("typescript-runtime")
	require.NotNil(t, flag)
	require.Equal(t, "node", flag.DefValue)
	require.Equal(t, []string{"typescript", "runtime"}, flag.Annotations[sdkModuleSettingAnnotation])

	require.NoError(t, cmd.Flags().Set("typescript-runtime", "bun"))
	raw, err := sdkModuleSettingsJSON(cmd, "typescript")
	require.NoError(t, err)
	require.JSONEq(t, `{"runtime":"bun"}`, raw)
}

func TestSDKModuleSettingFlagDoesNotSelectSDK(t *testing.T) {
	cmd := &cobra.Command{Use: "init"}
	sdk := configuredSDK{commandName: "go"}
	args := []*modFunctionArg{{
		Name: "compat",
		TypeDef: &modTypeDef{
			Kind: dagger.TypeDefKindBooleanKind,
		},
	}}
	require.NoError(t, addSDKModuleSettingFlags(cmd, sdk, args, true))
	require.NoError(t, cmd.Flags().Set("go-compat", "true"))
	_, err := sdkModuleSettingsJSON(cmd, "")
	require.ErrorContains(t, err, "also pass --sdk=go")
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
	require.NoError(t, addSDKModuleSettingFlags(cmd, sdk, args, false))

	// The SDK is positional, so the flag carries no SDK prefix.
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

func TestModuleInitSDKArg(t *testing.T) {
	// These are parseGlobalFlags results, so flags and their values are already
	// stripped. TestModuleInitSDKArgReadsStrippedArgs covers that contract.
	for _, test := range []struct {
		args []string
		want string
	}{
		{args: []string{"module", "init", "go"}, want: "go"},
		{args: []string{"mod", "init", "go"}, want: "go"},
		{args: []string{"help", "module", "init", "go"}, want: "go"},
		{args: []string{"module", "init", "-x", "go"}, want: "go"},
		{args: []string{"module", "init"}, want: ""},
		{args: []string{"module", "client", "add", "db"}, want: ""},
		{args: []string{"workspace", "update"}, want: ""},
	} {
		require.Equal(t, test.want, moduleInitSDKArg(test.args), test.args)
	}
}

// moduleInitSDKArg relies on parseGlobalFlags having removed every flag and
// flag value, including space-separated values for flags it does not know.
func TestModuleInitSDKArgReadsStrippedArgs(t *testing.T) {
	for _, rawArgs := range [][]string{
		{"module", "init", "go"},
		{"module", "init", "--name", "x", "go"},
		{"module", "init", "--name=x", "go"},
		{"module", "init", "-n", "x", "go"},
		{"module", "init", "go", "--starter=empty"},
		{"module", "init", "go", "--starter", "empty"},
	} {
		require.Equal(t, "go", moduleInitSDKArg(parseGlobalFlags(rawArgs)), rawArgs)
	}
}
