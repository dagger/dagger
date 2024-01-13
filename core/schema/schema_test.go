package schema

import (
	"context"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/stretchr/testify/require"
)

func TestNamespaceObjects(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		testCase  string
		namespace string
		obj       string
		result    string
	}{
		{
			testCase:  "namespace",
			namespace: "Foo",
			obj:       "Bar",
			result:    "FooBar",
		},
		{
			testCase:  "namespace into camel case",
			namespace: "foo",
			obj:       "bar-baz",
			result:    "FooBarBaz",
		},
		{
			testCase:  "don't namespace when equal",
			namespace: "foo",
			obj:       "Foo",
			result:    "Foo",
		},
		{
			testCase:  "don't namespace when prefixed",
			namespace: "foo",
			obj:       "FooBar",
			result:    "FooBar",
		},
		{
			testCase:  "still namespace when prefixed if not full",
			namespace: "foo",
			obj:       "Foobar",
			result:    "FooFoobar",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.testCase, func(t *testing.T) {
			result := namespaceObject(tc.obj, tc.namespace)
			require.Equal(t, tc.result, result)
		})
	}
}

func TestCoreModTypeDefs(t *testing.T) {
	ctx := context.Background()
	api, err := New(ctx, InitializeArgs{})
	require.NoError(t, err)
	require.NotNil(t, api.root)

	typeDefs, err := api.root.DefaultDeps.TypeDefs(ctx)
	require.NoError(t, err)

	objByName := make(map[string]*core.TypeDef)
	for _, typeDef := range typeDefs {
		require.Equal(t, core.TypeDefKindObject, typeDef.Kind)
		objByName[typeDef.AsObject.Value.Name] = typeDef
	}

	// just verify some subset of objects+functions as a sanity check

	// Container
	ctrTypeDef, ok := objByName["Container"]
	require.True(t, ok)
	ctrObj := ctrTypeDef.AsObject.Value

	_, ok = ctrObj.FunctionByName("id")
	require.False(t, ok)

	fileFn, ok := ctrObj.FunctionByName("file")
	require.True(t, ok)
	require.Equal(t, core.TypeDefKindObject, fileFn.ReturnType.Kind)
	require.Equal(t, "File", fileFn.ReturnType.AsObject.Value.Name)
	require.Len(t, fileFn.Args, 1)

	fileFnPathArg := fileFn.Args[0]
	require.Equal(t, "path", fileFnPathArg.Name)
	require.Equal(t, core.TypeDefKindString, fileFnPathArg.TypeDef.Kind)
	require.False(t, fileFnPathArg.TypeDef.Optional)

	withMountedDirectoryFn, ok := ctrObj.FunctionByName("withMountedDirectory")
	require.True(t, ok)

	withMountedDirectoryFnPathArg := withMountedDirectoryFn.Args[0]
	require.Equal(t, "path", withMountedDirectoryFnPathArg.Name)
	require.Equal(t, core.TypeDefKindString, withMountedDirectoryFnPathArg.TypeDef.Kind)
	require.False(t, withMountedDirectoryFnPathArg.TypeDef.Optional)

	withMountedDirectoryFnSourceArg := withMountedDirectoryFn.Args[1]
	require.Equal(t, "source", withMountedDirectoryFnSourceArg.Name)
	require.Equal(t, core.TypeDefKindObject, withMountedDirectoryFnSourceArg.TypeDef.Kind)
	require.Equal(t, "Directory", withMountedDirectoryFnSourceArg.TypeDef.AsObject.Value.Name)
	require.False(t, withMountedDirectoryFnSourceArg.TypeDef.Optional)

	withMountedDirectoryFnOwnerArg := withMountedDirectoryFn.Args[2]
	require.Equal(t, "owner", withMountedDirectoryFnOwnerArg.Name)
	require.Equal(t, core.TypeDefKindString, withMountedDirectoryFnOwnerArg.TypeDef.Kind)
	require.True(t, withMountedDirectoryFnOwnerArg.TypeDef.Optional)

	// File
	fileTypeDef, ok := objByName["File"]
	require.True(t, ok)
	fileObj := fileTypeDef.AsObject.Value

	_, ok = fileObj.FunctionByName("id")
	require.False(t, ok)

	exportFn, ok := fileObj.FunctionByName("export")
	require.True(t, ok)
	require.Equal(t, core.TypeDefKindBoolean, exportFn.ReturnType.Kind)
	require.Len(t, exportFn.Args, 2)

	exportFnPathArg := exportFn.Args[0]
	require.Equal(t, "path", exportFnPathArg.Name)
	require.Equal(t, core.TypeDefKindString, exportFnPathArg.TypeDef.Kind)
	require.False(t, exportFnPathArg.TypeDef.Optional)

	exportFnAllowParentDirPathArg := exportFn.Args[1]
	require.Equal(t, "allowParentDirPath", exportFnAllowParentDirPathArg.Name)
	require.Equal(t, core.TypeDefKindBoolean, exportFnAllowParentDirPathArg.TypeDef.Kind)
	require.True(t, exportFnAllowParentDirPathArg.TypeDef.Optional)
}
