package daggercmd

import (
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/workspace"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestInstalledSDKCommandShape(t *testing.T) {
	sdk := configuredSDK{
		moduleName:  "dagger-go-sdk",
		commandName: "go",
		entry: workspace.ModuleEntry{
			Source: "github.com/dagger/go-sdk",
			AsSDK:  &workspace.ModuleAsSDK{Name: "go"},
		},
	}
	cmd := newInstalledSDKCommand(sdk)
	require.Equal(t, "go", cmd.Use)
	require.Equal(t, "Use the go SDK to develop and consume modules", cmd.Short)
	require.Equal(t, "true", cmd.Annotations[dynamicSDKCommandAnnotation])
	require.Equal(t, []string{"client", "info", "module"}, commandNames(cmd.Commands()))

	moduleCmd := newSDKModuleCommand("go")
	for _, name := range []string{"claim", "list", "unclaim"} {
		require.NotNil(t, findCommand(moduleCmd, name))
	}
	require.NoError(t, addSDKModuleInitCommand(moduleCmd, "go", &modFunction{}))
	require.Equal(t, "init <name>", findCommand(moduleCmd, "init").Use)

	clientCmd := newSDKClientCommand("go")
	for _, name := range []string{"claim", "list", "unclaim"} {
		require.NotNil(t, findCommand(clientCmd, name))
	}
	require.NoError(t, addSDKClientInitCommand(clientCmd, "go", &modFunction{}))
	require.Equal(t, "claim <path> <module>", findCommand(clientCmd, "claim").Use)
	require.Equal(t, "init <path> <module>", findCommand(clientCmd, "init").Use)
}

func TestClearDynamicSDKCommands(t *testing.T) {
	parent := &cobra.Command{Use: "sdk"}
	dynamic := newInstalledSDKCommand(configuredSDK{moduleName: "go-sdk", commandName: "go"})
	static := &cobra.Command{Use: "static"}
	parent.AddCommand(dynamic, static)

	clearDynamicSDKCommands(parent)
	require.Equal(t, []string{"static"}, commandNames(parent.Commands()))
}

func TestSDKInitFunctionExtraArgs(t *testing.T) {
	workspaceArg := &modFunctionArg{
		Name: "workspace",
		TypeDef: &modTypeDef{
			Kind: dagger.TypeDefKindObjectKind,
			AsObject: &modObject{
				Name: "Workspace",
			},
		},
	}
	stringArg := func(name string) *modFunctionArg {
		return &modFunctionArg{
			Name: name,
			TypeDef: &modTypeDef{
				Kind: dagger.TypeDefKindStringKind,
			},
		}
	}
	boolArg := &modFunctionArg{
		Name: "cgoEnabled",
		TypeDef: &modTypeDef{
			Kind: dagger.TypeDefKindBooleanKind,
		},
	}
	fn := &modFunction{Args: []*modFunctionArg{
		workspaceArg,
		stringArg("name"),
		stringArg("path"),
		stringArg("module"),
		stringArg("goVersion"),
		boolArg,
	}}

	moduleArgs := sdkInitFunctionExtraArgs(fn, sdkInitKindModule)
	require.Equal(t, []string{"module", "goVersion", "cgoEnabled"}, sdkInitArgNames(moduleArgs))

	clientArgs := sdkInitFunctionExtraArgs(fn, sdkInitKindClient)
	require.Equal(t, []string{"name", "goVersion", "cgoEnabled"}, sdkInitArgNames(clientArgs))
}

func TestSDKInitArgsJSON(t *testing.T) {
	cmd := newModuleInitSDKCommand("go")
	flag := &modFunctionArg{
		Name: "goVersion",
		TypeDef: &modTypeDef{
			Kind: dagger.TypeDefKindStringKind,
		},
	}
	require.NoError(t, flag.AddFlag(cmd.Flags()))
	require.NoError(t, cmd.Flags().SetAnnotation(flag.FlagName(), sdkInitArgAnnotation, []string{flag.Name}))
	require.NoError(t, cmd.Flags().Set("go-version", "1.22"))

	args, err := sdkInitArgsJSON(cmd)
	require.NoError(t, err)
	require.JSONEq(t, `{"goVersion":"1.22"}`, args)
}

func TestConfiguredSDKsUsesAsSDKName(t *testing.T) {
	cfg := &workspace.Config{
		Modules: map[string]workspace.ModuleEntry{
			"go-sdk": {
				Source: "github.com/dagger/go-sdk",
				AsSDK:  &workspace.ModuleAsSDK{},
			},
			"custom-sdk": {
				Source: "github.com/acme/custom-sdk",
				AsSDK:  &workspace.ModuleAsSDK{},
			},
		},
	}

	sdks, err := configuredSDKs(cfg)
	require.NoError(t, err)
	require.Equal(t, []string{"custom", "go"}, []string{sdks[0].commandName, sdks[1].commandName})

	resolved, err := resolveConfiguredSDK(cfg, "go")
	require.NoError(t, err)
	require.Equal(t, "go-sdk", resolved.moduleName)
	require.Equal(t, "go", resolved.commandName)

	resolved, err = resolveConfiguredSDK(cfg, "go-sdk")
	require.NoError(t, err)
	require.Equal(t, "go-sdk", resolved.moduleName)
	require.Equal(t, "go", resolved.commandName)
}

func TestConfiguredSDKsRejectsDuplicateCommandName(t *testing.T) {
	cfg := &workspace.Config{
		Modules: map[string]workspace.ModuleEntry{
			"dagger-go-sdk": {
				Source: "github.com/dagger/go-sdk",
				AsSDK:  &workspace.ModuleAsSDK{},
			},
			"go-sdk": {
				Source: "github.com/acme/go-sdk",
				AsSDK:  &workspace.ModuleAsSDK{},
			},
		},
	}

	_, err := configuredSDKs(cfg)
	require.ErrorContains(t, err, `SDK name "go" is ambiguous`)
}

func TestSDKInitFunctionFlagArgsSkipsUnsupportedOptionalArgs(t *testing.T) {
	stringArg := &modFunctionArg{
		Name:        "goVersion",
		Description: "Go version to use.",
		TypeDef: &modTypeDef{
			Kind: dagger.TypeDefKindStringKind,
		},
	}
	unsupportedOptionalArg := &modFunctionArg{
		Name: "settings",
		TypeDef: &modTypeDef{
			Kind:     dagger.TypeDefKindInputKind,
			Optional: true,
			AsInput:  &modInput{Name: "Settings"},
		},
	}
	fn := &modFunction{Args: []*modFunctionArg{
		stringArg,
		unsupportedOptionalArg,
	}}

	args, err := sdkInitFunctionFlagArgs(fn, sdkInitKindModule)
	require.NoError(t, err)
	require.Equal(t, []string{"goVersion"}, sdkInitArgNames(args))

	unsupportedRequiredArg := &modFunctionArg{
		Name: "settings",
		TypeDef: &modTypeDef{
			Kind:    dagger.TypeDefKindInputKind,
			AsInput: &modInput{Name: "Settings"},
		},
	}
	_, err = sdkInitFunctionFlagArgs(&modFunction{Args: []*modFunctionArg{unsupportedRequiredArg}}, sdkInitKindModule)
	require.ErrorContains(t, err, "unsupported type for flag --settings")
}

func TestShouldRegisterSDKCommands(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want bool
	}{
		{name: "sdk help", args: []string{"sdk", "--help"}, want: true},
		{name: "sdk module init", args: []string{"sdk", "go", "module", "init", "myapp"}, want: true},
		{name: "sdk client init", args: []string{"sdk", "typescript", "client", "init", "./client", "."}, want: true},
		{name: "sdk search moved", args: []string{"search", "--sdk"}, want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, shouldRegisterSDKCommands(tt.args))
		})
	}
}

func TestSDKInvocationParsing(t *testing.T) {
	name, ok := sdkInvocationSDKName([]string{"sdk", "go", "info"})
	require.True(t, ok)
	require.Equal(t, "go", name)
	require.False(t, sdkInvocationNeedsInit([]string{"sdk", "go", "info"}))
	require.False(t, sdkInvocationNeedsInit([]string{"sdk", "go", "module", "list"}))
	require.True(t, sdkInvocationNeedsInit([]string{"sdk", "go", "module", "init"}))
	require.True(t, sdkInvocationNeedsInit([]string{"sdk", "go", "module"}))
	require.True(t, sdkInvocationNeedsInit([]string{"sdk", "go"}))
}

func commandNames(commands []*cobra.Command) []string {
	names := make([]string, len(commands))
	for i, command := range commands {
		names[i] = command.Name()
	}
	return names
}

func findCommand(parent *cobra.Command, name string) *cobra.Command {
	for _, command := range parent.Commands() {
		if command.Name() == name {
			return command
		}
	}
	return nil
}

func sdkInitArgNames(args []*modFunctionArg) []string {
	names := make([]string, len(args))
	for i, arg := range args {
		names[i] = arg.Name
	}
	return names
}
