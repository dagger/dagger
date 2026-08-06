package daggercmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/querybuilder"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
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

func TestDetachedFinalQueryAndResponsePath(t *testing.T) {
	oldOutputPath, oldJSONOutput := outputPath, jsonOutput
	t.Cleanup(func() { outputPath, jsonOutput = oldOutputPath, oldJSONOutput })
	outputPath, jsonOutput = "", false

	fc := &FuncCommand{q: querybuilder.Query()}
	cmd := &cobra.Command{Use: "call"}
	receiver := testObjectTypeDef("Container", "", "")
	stdout := &modFunction{Name: "stdout", ReturnType: testStringTypeDef()}
	require.NoError(t, fc.selectFunc(&modFunction{Name: "container", ReturnType: receiver}, cmd))
	require.NoError(t, fc.selectFunc(stdout, cmd))
	q, leaf := handleObjectLeafWithPath(fc.q, stdout.ReturnType)
	require.Empty(t, leaf)

	presentation, err := detachedPresentation(stdout.ReturnType, fc.responsePath, q)
	require.NoError(t, err)
	require.Equal(t, []string{"container", "stdout"}, presentation.ResponsePath)
	require.Equal(t, string(dagger.TypeDefKindStringKind), presentation.ReturnKind)

	request, err := buildDetachedQueryRequest(context.Background(), q)
	require.NoError(t, err)
	var gqlRequest struct {
		Query         string `json:"query"`
		OperationName string `json:"operationName"`
	}
	require.NoError(t, json.Unmarshal(request, &gqlRequest))
	require.Equal(t, "query Query {container{stdout}}", gqlRequest.Query)
	require.Equal(t, "Query", gqlRequest.OperationName)
}

func TestDetachedPresentationSupportedAndRejectedResults(t *testing.T) {
	oldOutputPath, oldJSONOutput := outputPath, jsonOutput
	t.Cleanup(func() { outputPath, jsonOutput = oldOutputPath, oldJSONOutput })
	outputPath, jsonOutput = "", false
	q := querybuilder.Query().Select("value")

	scalarKinds := []dagger.TypeDefKind{
		dagger.TypeDefKindStringKind, dagger.TypeDefKindIntegerKind,
		dagger.TypeDefKindFloatKind, dagger.TypeDefKindBooleanKind,
		dagger.TypeDefKindScalarKind, dagger.TypeDefKindEnumKind,
		dagger.TypeDefKindVoidKind,
	}
	for _, kind := range scalarKinds {
		t.Run(string(kind), func(t *testing.T) {
			typeDef := &modTypeDef{Kind: kind}
			presentation, err := detachedPresentation(typeDef, []string{"value"}, q)
			require.NoError(t, err)
			require.Equal(t, kind == dagger.TypeDefKindVoidKind, presentation.Void)
		})
	}

	list := &modTypeDef{Kind: dagger.TypeDefKindListKind, AsList: &modList{ElementTypeDef: testStringTypeDef()}}
	presentation, err := detachedPresentation(list, []string{"values"}, q)
	require.NoError(t, err)
	require.Equal(t, string(dagger.TypeDefKindStringKind), presentation.ElementKind)

	object := testObjectTypeDef(Container, "", "")
	_, err = detachedPresentation(object, []string{"container", "id"}, q)
	require.NoError(t, err)

	rejected := []struct {
		name     string
		typeDef  *modTypeDef
		contains string
	}{
		{name: "object list", typeDef: &modTypeDef{Kind: dagger.TypeDefKindListKind, AsList: &modList{ElementTypeDef: object}}, contains: "object-list"},
		{name: "changeset", typeDef: testObjectTypeDef(Changeset, "", ""), contains: "Changeset"},
		{name: "changeset list", typeDef: &modTypeDef{Kind: dagger.TypeDefKindListKind, AsList: &modList{ElementTypeDef: testObjectTypeDef(Changeset, "", "")}}, contains: "Changeset"},
		{name: "llm", typeDef: testObjectTypeDef(LLM, "", ""), contains: "LLM"},
		{name: "terminal", typeDef: testObjectTypeDef("Terminal", "", ""), contains: "terminal"},
		{name: "pipe", typeDef: testObjectTypeDef("Pipe", "", ""), contains: "pipe"},
		{name: "interface", typeDef: &modTypeDef{Kind: dagger.TypeDefKindInterfaceKind, AsInterface: &modInterface{Name: "Thing"}}, contains: "interface"},
	}
	for _, test := range rejected {
		t.Run(test.name, func(t *testing.T) {
			_, err := detachedPresentation(test.typeDef, []string{"value"}, q)
			var usageErr *detachUsageError
			require.ErrorAs(t, err, &usageErr)
			require.ErrorContains(t, err, test.contains)
		})
	}

	outputPath = "out"
	_, err = detachedPresentation(testStringTypeDef(), []string{"value"}, q)
	require.ErrorContains(t, err, "--output")
}

func TestDetachedResultExtractionAndFormatting(t *testing.T) {
	oldStdoutIsTTY := stdoutIsTTY
	t.Cleanup(func() { stdoutIsTTY = oldStdoutIsTTY })
	stdoutIsTTY = false

	presentation := engine.QueryPresentation{
		ResponsePath: []string{"container", "stdout"},
		ReturnKind:   string(dagger.TypeDefKindStringKind),
	}
	value, err := extractDetachedResult([]byte(`{"data":{"container":{"stdout":"hello"}}}`), presentation)
	require.NoError(t, err)
	var output bytes.Buffer
	require.NoError(t, formatDetachedResult(&output, value, presentation))
	require.Equal(t, "hello", output.String())

	presentation.ResponsePath = []string{"values"}
	presentation.ReturnKind = string(dagger.TypeDefKindListKind)
	presentation.ElementKind = string(dagger.TypeDefKindStringKind)
	value, err = extractDetachedResult([]byte(`{"data":{"values":["a","b"]}}`), presentation)
	require.NoError(t, err)
	output.Reset()
	require.NoError(t, formatDetachedResult(&output, value, presentation))
	require.Equal(t, "a\nb\n", output.String())

	id := call.NewEngineResultID(42, call.NewType(&ast.Type{NamedType: "Container", NonNull: true}))
	encodedID, err := id.Encode()
	require.NoError(t, err)
	presentation.ResponsePath = []string{"container", "id"}
	presentation.ReturnKind = string(dagger.TypeDefKindObjectKind)
	value, err = extractDetachedResult([]byte(fmt.Sprintf(`{"data":{"container":{"id":%q}}}`, encodedID)), presentation)
	require.NoError(t, err)
	output.Reset()
	require.NoError(t, formatDetachedResult(&output, value, presentation))
	require.Contains(t, output.String(), "Container@")

	_, err = extractDetachedResult([]byte(`{"data":null,"errors":[{"message":"saved failure"}]}`), presentation)
	require.ErrorContains(t, err, "saved failure")
}

func TestDetachFlagIsCallLocal(t *testing.T) {
	detachable := (&FuncCommand{Name: "call", EnableDetach: true}).Command()
	require.NotNil(t, detachable.PersistentFlags().Lookup("detach"))
	ordinary := (&FuncCommand{Name: "core"}).Command()
	require.Nil(t, ordinary.PersistentFlags().Lookup("detach"))
	require.Contains(t, callModCmd.Example, "call --detach")
	require.Contains(t, apiCallCmd.Example, "api call --detach")
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
