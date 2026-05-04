package main

import (
	"context"
	"testing"

	"dagger.io/dagger"
	"dagger.io/dagger/querybuilder"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestFunctionArgNamedWorkspaceIgnoresInheritedGlobalWorkspaceFlag(t *testing.T) {
	root := &cobra.Command{Use: "dagger"}
	root.PersistentFlags().String("workspace", "", "Select the workspace to load")
	require.NoError(t, root.PersistentFlags().Set("workspace", "github.com/acme/workspace"))

	cmd := &cobra.Command{Use: "call"}
	root.AddCommand(cmd)

	fc := &FuncCommand{
		mod: &moduleDef{
			typeDefsByName: map[string]*modTypeDef{
				Directory: {
					TypeName: Directory,
					Kind:     dagger.TypeDefKindObjectKind,
					AsObject: &modObject{Name: Directory},
				},
			},
		},
		q: querybuilder.Query(),
	}
	fn := &modFunction{
		Name: "greeter",
		Args: []*modFunctionArg{
			{
				Name:        "workspace",
				DefaultPath: "/",
				TypeDef: &modTypeDef{
					TypeName: Directory,
					Optional: true,
				},
			},
		},
	}

	require.NoError(t, fc.addFlagsForFunction(cmd, fn))

	flag := cmd.Flags().Lookup("workspace")
	require.NotNil(t, flag)
	require.NotSame(t, root.PersistentFlags().Lookup("workspace"), flag)
	require.Same(t, flag, cmd.LocalNonPersistentFlags().Lookup("workspace"))

	require.NoError(t, fc.selectFunc(fn, cmd))

	query, err := fc.q.Build(context.Background())
	require.NoError(t, err)
	require.NotContains(t, query, "workspace:")
}
