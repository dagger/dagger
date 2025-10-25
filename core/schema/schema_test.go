package schema

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/cache"
)

func TestCoreModTypeDefs(t *testing.T) {
	ctx := context.Background()
	root := &core.Query{}
	baseCache, err := cache.NewCache[string, dagql.AnyResult](ctx, "")
	require.NoError(t, err)
	dag := dagql.NewServer(root, dagql.NewSessionCache(baseCache))
	coreMod := &CoreMod{Dag: dag}
	coreModDeps := core.NewModDeps(root, []core.Mod{coreMod})
	require.NoError(t, coreMod.Install(ctx, dag))
	typeDefs, err := coreModDeps.TypeDefs(ctx, dag)
	require.NoError(t, err)

	typeByName := make(map[string]*core.TypeDef)
	for _, typeDef := range typeDefs {
		switch typeDef.Kind {
		case core.TypeDefKindObject:
			typeByName[typeDef.AsObject.Value.Name] = typeDef
		case core.TypeDefKindInput:
			typeByName[typeDef.AsInput.Value.Name] = typeDef
		case core.TypeDefKindEnum:
			typeByName[typeDef.AsEnum.Value.Name] = typeDef
		}
	}

	// just verify some subset of objects+functions as a sanity check

	// Container
	ctrTypeDef, ok := typeByName["Container"]
	require.True(t, ok)
	ctrObj := ctrTypeDef.AsObject.Value

	_, ok = ctrObj.FunctionByName("id")
	require.False(t, ok)

	fileFn, ok := ctrObj.FunctionByName("file")
	require.True(t, ok)
	require.Equal(t, core.TypeDefKindObject, fileFn.ReturnType.Kind)
	require.Equal(t, "File", fileFn.ReturnType.AsObject.Value.Name)
	require.Len(t, fileFn.Args, 2)

	fileFnPathArg := fileFn.Args[0]
	require.Equal(t, "path", fileFnPathArg.Name)
	require.Equal(t, core.TypeDefKindString, fileFnPathArg.TypeDef.Kind)
	require.False(t, fileFnPathArg.TypeDef.Optional)

	fileFnExpandArg := fileFn.Args[1]
	require.Equal(t, "expand", fileFnExpandArg.Name)
	require.Equal(t, core.TypeDefKindBoolean, fileFnExpandArg.TypeDef.Kind)

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

	// PortForward input type
	portForwardTypeDef, ok := typeByName["PortForward"]
	require.True(t, ok)
	require.Equal(t, core.TypeDefKindInput, portForwardTypeDef.Kind)
	require.Len(t, portForwardTypeDef.AsInput.Value.Fields, 3)
	var frontendPortField *core.FieldTypeDef
	var backendPortField *core.FieldTypeDef
	var protocolField *core.FieldTypeDef
	for _, field := range portForwardTypeDef.AsInput.Value.Fields {
		switch field.Name {
		case "frontend":
			frontendPortField = field
		case "backend":
			backendPortField = field
		case "protocol":
			protocolField = field
		}
	}
	require.NotNil(t, frontendPortField)
	require.Equal(t, core.TypeDefKindInteger, frontendPortField.TypeDef.Kind)
	require.True(t, frontendPortField.TypeDef.Optional)
	require.NotNil(t, backendPortField)
	require.Equal(t, core.TypeDefKindInteger, backendPortField.TypeDef.Kind)
	require.False(t, backendPortField.TypeDef.Optional)
	require.NotNil(t, protocolField)
	require.Equal(t, core.TypeDefKindEnum, protocolField.TypeDef.Kind)

	// File
	fileTypeDef, ok := typeByName["File"]
	require.True(t, ok)
	fileObj := fileTypeDef.AsObject.Value

	_, ok = fileObj.FunctionByName("id")
	require.False(t, ok)

	exportFn, ok := fileObj.FunctionByName("export")
	require.True(t, ok)
	require.Equal(t, core.TypeDefKindString, exportFn.ReturnType.Kind)
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
