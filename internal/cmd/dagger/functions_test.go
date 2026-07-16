package daggercmd

import (
	"bytes"
	"context"
	"io"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/querybuilder"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestFindSiblingEntrypoint(t *testing.T) {
	defaultType := testObjectTypeDef("DaggerDev", "dagger-dev", "default module")
	defaultType.AsObject.Functions = []*modFunction{
		{Name: "hello", ReturnType: testStringTypeDef()},
	}

	siblingType := testObjectTypeDef("PythonSdk", "python-sdk", "python sdk")
	queryType := testObjectTypeDef("Query", "", "")
	queryType.AsObject.Functions = []*modFunction{
		{Name: "daggerDev", SourceModuleName: "dagger-dev", ReturnType: defaultType},
		{Name: "pythonSdk", SourceModuleName: "python-sdk", ReturnType: siblingType},
	}

	mod := &moduleDef{
		Name:       "dagger-dev",
		MainObject: defaultType,
		Objects:    []*modTypeDef{queryType, defaultType, siblingType},
	}

	sibling := findSiblingEntrypoint(mod, "python-sdk")
	require.NotNil(t, sibling)
	require.Equal(t, "pythonSdk", sibling.Name)
}

func TestFunctionListRunIncludesSiblingEntrypoints(t *testing.T) {
	provider := &modObject{
		Name: "DaggerDev",
		Functions: []*modFunction{
			{Name: "hello", Description: "default module", ReturnType: testStringTypeDef()},
		},
	}
	siblingType := testObjectTypeDef("PythonSdk", "python-sdk", "python sdk")
	sibling := &modFunction{
		Name:             "pythonSdk",
		Description:      "python sdk",
		SourceModuleName: "python-sdk",
		ReturnType:       siblingType,
	}

	var out bytes.Buffer
	err := functionListRun(provider, &out, io.Discard, false, false, []*modFunction{sibling})
	require.NoError(t, err)
	require.Contains(t, out.String(), "hello")
	require.Contains(t, out.String(), "python-sdk")
}

func TestFunctionListRunCanHideLoadFromIDFunctions(t *testing.T) {
	provider := &modObject{
		Name: "Query",
		Functions: []*modFunction{
			{Name: "container", Description: "Create a container", ReturnType: testObjectTypeDef("Container", "", "")},
			{Name: "loadContainerFromID", Description: "Load a Container from its ID", ReturnType: testObjectTypeDef("Container", "", "")},
		},
	}

	var out bytes.Buffer
	err := functionListRun(provider, &out, io.Discard, false, true, nil)
	require.NoError(t, err)
	require.Contains(t, out.String(), "container")
	require.NotContains(t, out.String(), "load-container-from-id")
}

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

func TestCorePseudoModuleSelection(t *testing.T) {
	oldModuleURL := moduleURL
	oldModuleNoURL := moduleNoURL
	t.Cleanup(func() {
		moduleURL = oldModuleURL
		moduleNoURL = oldModuleNoURL
	})

	moduleURL = coreModuleRef
	moduleNoURL = false

	require.True(t, isCoreModuleSelected())
	require.False(t, shouldLoadWorkspaceModules(false))
	require.False(t, initModuleParams([]string{"container"}).LoadWorkspaceModules)
	// The scope is set per command site (api call/functions), never by the
	// shared helper: shell also builds its params here and must keep the
	// full workspace view.
	require.Empty(t, initModuleParams([]string{"container"}).WorkspaceModuleScope)

	ref, ok := getExplicitModuleSourceRef()
	require.True(t, ok)
	require.Equal(t, coreModuleRef, ref)

	moduleURL = "./core"
	require.False(t, isCoreModuleSelected())
	require.True(t, shouldLoadWorkspaceModules(false))
}

func testStringTypeDef() *modTypeDef {
	return &modTypeDef{Kind: dagger.TypeDefKindStringKind}
}

func testObjectTypeDef(name, sourceModuleName, description string) *modTypeDef {
	return &modTypeDef{
		Kind: dagger.TypeDefKindObjectKind,
		AsObject: &modObject{
			Name:             name,
			Description:      description,
			SourceModuleName: sourceModuleName,
		},
	}
}
