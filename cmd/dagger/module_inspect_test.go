package main

import (
	"testing"

	"dagger.io/dagger"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceLoadLocation(t *testing.T) {
	t.Run("nil workspace", func(t *testing.T) {
		require.Equal(t, ".", workspaceLoadLocation(nil))
	})

	t.Run("empty workspace", func(t *testing.T) {
		ref := ""
		require.Equal(t, ".", workspaceLoadLocation(&ref))
	})

	t.Run("explicit workspace", func(t *testing.T) {
		ref := "https://github.com/dagger/dagger"
		require.Equal(t, ref, workspaceLoadLocation(&ref))
	})
}

func TestFocusRootModuleFunctions(t *testing.T) {
	appType := &modTypeDef{
		Kind: dagger.TypeDefKindObjectKind,
		AsObject: &modObject{
			Name:             "App",
			SourceModuleName: "app",
			Constructor: &modFunction{
				Name: "app",
				Args: []*modFunctionArg{
					{
						Name:    "greeting",
						TypeDef: &modTypeDef{Kind: dagger.TypeDefKindStringKind},
					},
				},
			},
		},
	}
	defaultsType := &modTypeDef{
		Kind: dagger.TypeDefKindObjectKind,
		AsObject: &modObject{
			Name:             "Defaults",
			SourceModuleName: "defaults",
		},
	}
	queryType := &modTypeDef{
		Kind: dagger.TypeDefKindObjectKind,
		AsObject: &modObject{
			Name: "Query",
			Fields: []*modField{
				{Name: "version"},
			},
			Functions: []*modFunction{
				{Name: "app", SourceModuleName: "app", ReturnType: appType},
				{Name: "build", SourceModuleName: "app"},
				{Name: "defaults", SourceModuleName: "defaults", ReturnType: defaultsType},
				{Name: "message", SourceModuleName: "defaults"},
				{Name: "lint", SourceModuleName: "lint"},
				{Name: "container"},
			},
		},
	}
	def := &moduleDef{
		Objects:      []*modTypeDef{queryType, appType, defaultsType},
		MainObject:   queryType,
		Dependencies: nil,
	}

	focused := focusRootModuleFunctions(def, "app", []string{"app", "defaults"})
	require.NotNil(t, focused)
	require.Equal(t, "app", focused.Name)
	require.NotSame(t, def.MainObject, focused.MainObject)
	require.Equal(t, "Query", focused.MainObject.AsObject.Name)
	require.Len(t, focused.MainObject.AsObject.Fields, 0)
	require.Equal(
		t,
		[]string{"build", "defaults", "message"},
		[]string{
			focused.MainObject.AsObject.Functions[0].Name,
			focused.MainObject.AsObject.Functions[1].Name,
			focused.MainObject.AsObject.Functions[2].Name,
		},
	)
	require.NotNil(t, focused.MainObject.AsObject.Constructor)
	require.Same(t, focused.MainObject, focused.MainObject.AsObject.Constructor.ReturnType)
	require.Len(t, focused.MainObject.AsObject.Constructor.Args, 1)
	require.Equal(t, "greeting", focused.MainObject.AsObject.Constructor.Args[0].Name)

	require.Len(t, def.MainObject.AsObject.Fields, 1)
	require.Len(t, def.MainObject.AsObject.Functions, 6)
}

func TestFocusRootModuleFunctionsMissingModule(t *testing.T) {
	queryType := &modTypeDef{
		Kind: dagger.TypeDefKindObjectKind,
		AsObject: &modObject{
			Name: "Query",
			Functions: []*modFunction{
				{Name: "lint", SourceModuleName: "lint"},
			},
		},
	}
	def := &moduleDef{
		Objects:    []*modTypeDef{queryType},
		MainObject: queryType,
	}

	require.Nil(t, focusRootModuleFunctions(def, "app", nil))
}
