package main

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"dagger.io/dagger"
)

func TestConstructorLocalFlags(t *testing.T) {
	fc := &FuncCommand{
		mod: &moduleDef{},
	}

	ciType := &modTypeDef{
		Kind: dagger.TypeDefKindObjectKind,
		AsObject: &modObject{
			Name:             "Ci",
			SourceModuleName: "ci",
		},
	}
	queryType := &modTypeDef{
		Kind: dagger.TypeDefKindObjectKind,
		AsObject: &modObject{
			Name: "Query",
			Functions: []*modFunction{
				{
					Name:             "ci",
					SourceModuleName: "ci",
					ReturnType:       ciType,
					Args: []*modFunctionArg{
						{
							Name:    "prefix",
							TypeDef: &modTypeDef{Kind: dagger.TypeDefKindStringKind},
						},
					},
				},
			},
		},
	}

	t.Run("registers constructor args as local flags", func(t *testing.T) {
		root := &cobra.Command{Use: "call"}
		fc.addConstructorLocalFlags(root, queryType)
		flag := root.Flags().Lookup("prefix")
		require.NotNil(t, flag, "expected --prefix as local flag")
	})

	t.Run("skips duplicate constructor args", func(t *testing.T) {
		root := &cobra.Command{Use: "call"}
		fc.addConstructorLocalFlags(root, queryType)
		fc.addConstructorLocalFlags(root, queryType)
		flag := root.Flags().Lookup("prefix")
		require.NotNil(t, flag)
	})

	t.Run("child selectFunc picks up parent flag value", func(t *testing.T) {
		root := &cobra.Command{Use: "call"}
		fc.addConstructorLocalFlags(root, queryType)

		// Simulate parsing --prefix at root level
		root.Flags().Set("prefix", "root-val")

		child := &cobra.Command{Use: "serve"}
		root.AddCommand(child)

		// Parent's local flag should be findable
		parentFlag := child.Parent().LocalFlags().Lookup("prefix")
		require.NotNil(t, parentFlag)
		require.True(t, parentFlag.Changed)
		require.Equal(t, "root-val", parentFlag.Value.String())
	})
}
