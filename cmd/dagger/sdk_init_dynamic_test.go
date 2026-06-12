package main

import (
	"bytes"
	"testing"

	"dagger.io/dagger"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestDynamicSDKInitCommandLookup(t *testing.T) {
	moduleParent := &cobra.Command{
		Use:  "init <sdk> <name>",
		Args: cobra.NoArgs,
	}
	moduleParent.AddCommand(newModuleInitSDKCommand("go"))

	moduleChild, _, err := moduleParent.Find([]string{"go", "myapp"})
	require.NoError(t, err)
	require.Equal(t, "go", moduleChild.Name())
	require.Equal(t, "go <name>", moduleChild.Use)

	cmd, args, err := moduleParent.Find([]string{"plain", "myapp"})
	require.NoError(t, err)
	require.Same(t, moduleParent, cmd)
	require.Equal(t, []string{"plain", "myapp"}, args)
	require.ErrorContains(t, moduleParent.Args(moduleParent, args), `unknown command "plain"`)

	clearDynamicSDKInitCommands(moduleParent)
	cmd, args, err = moduleParent.Find([]string{"go", "myapp"})
	require.NoError(t, err)
	require.Same(t, moduleParent, cmd)
	require.Equal(t, []string{"go", "myapp"}, args)
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

func TestPrintSDKInitOptions(t *testing.T) {
	args := []*modFunctionArg{
		{
			Name:        "goVersion",
			Description: "Go version to use.",
			TypeDef: &modTypeDef{
				Kind: dagger.TypeDefKindStringKind,
			},
		},
		{
			Name:        "cgoEnabled",
			Description: "Enable cgo.",
			TypeDef: &modTypeDef{
				Kind:     dagger.TypeDefKindBooleanKind,
				Optional: true,
			},
		},
	}

	var buf bytes.Buffer
	require.NoError(t, printSDKInitOptions(&buf, "go", sdkInitKindModule, args))
	out := buf.String()
	require.Contains(t, out, "Flags for `dagger module init go <name>`:")
	require.Contains(t, out, "--go-version")
	require.Contains(t, out, "string")
	require.Contains(t, out, "yes")
	require.Contains(t, out, "--cgo-enabled")
	require.Contains(t, out, "bool")
	require.Contains(t, out, "no")
}

func TestShouldRegisterSDKInitCommands(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want bool
	}{
		{
			name: "module init",
			args: []string{"module", "init", "go", "myapp"},
			want: true,
		},
		{
			name: "api client init",
			args: []string{"api", "client", "init", "typescript", "./client", "."},
			want: true,
		},
		{
			name: "global workspace flag",
			args: []string{"--workspace", "./ws", "module", "init", "go", "myapp"},
			want: true,
		},
		{
			name: "global workspace short flag",
			args: []string{"-W", "./ws", "api", "client", "init", "go", "./client", "."},
			want: true,
		},
		{
			name: "unrelated command",
			args: []string{"sdk", "list"},
			want: false,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, shouldRegisterSDKInitCommands(tt.args))
		})
	}
}

func sdkInitArgNames(args []*modFunctionArg) []string {
	names := make([]string, len(args))
	for i, arg := range args {
		names[i] = arg.Name
	}
	return names
}
