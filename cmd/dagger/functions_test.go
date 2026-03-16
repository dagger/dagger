package main

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"dagger.io/dagger"
)

func TestRewriteQueryRootConstructorArgs(t *testing.T) {
	root := &cobra.Command{Use: "call"}
	root.Flags().StringP("mod", "m", "", "")
	root.Flags().BoolP("json", "j", false, "")

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
					Name:       "ci",
					ReturnType: ciType,
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

	t.Run("moves constructor flags behind namespaced module token", func(t *testing.T) {
		args := []string{"--prefix", "ctor", "ci", "echo", "--prefix", "method"}
		require.Equal(
			t,
			[]string{"ci", "--prefix", "ctor", "echo", "--prefix", "method"},
			rewriteQueryRootConstructorArgs(root, queryType, args),
		)
	})

	t.Run("keeps root flags in place", func(t *testing.T) {
		args := []string{"-m", ".", "--prefix", "ctor", "ci", "echo", "--prefix", "method"}
		require.Equal(
			t,
			[]string{"-m", ".", "ci", "--prefix", "ctor", "echo", "--prefix", "method"},
			rewriteQueryRootConstructorArgs(root, queryType, args),
		)
	})

	t.Run("does not rewrite when the candidate token is a flag value", func(t *testing.T) {
		args := []string{"--prefix", "ci", "echo"}
		require.Equal(t, args, rewriteQueryRootConstructorArgs(root, queryType, args))
	})

	t.Run("does not rewrite non query roots", func(t *testing.T) {
		require.Equal(
			t,
			[]string{"--prefix", "ctor", "ci", "echo"},
			rewriteQueryRootConstructorArgs(root, ciType, []string{"--prefix", "ctor", "ci", "echo"}),
		)
	})
}
