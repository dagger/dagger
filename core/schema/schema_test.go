package schema

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
)

func TestCoreModTypeDefs(t *testing.T) {
	ctx := context.Background()
	root := &core.Query{}
	baseCache, err := dagql.NewCache(ctx, "")
	require.NoError(t, err)
	ctx = dagql.ContextWithCache(ctx, baseCache)
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		ClientID:  "coremod-typedefs-client",
		SessionID: "coremod-typedefs-session",
	})
	dag := dagql.NewServer(root)
	coreMod := &CoreMod{Dag: dag}
	coreModDeps := core.NewModDeps(root, []core.Mod{coreMod})
	require.NoError(t, coreMod.Install(ctx, dag))
	typeDefs, err := coreModDeps.TypeDefs(ctx, dag)
	require.NoError(t, err)

	typeByName := make(map[string]*core.TypeDef)
	for _, typeDef := range typeDefs {
		typeDefSelf := typeDef.Self()
		switch typeDefSelf.Kind {
		case core.TypeDefKindObject:
			typeByName[typeDefSelf.AsObject.Value.Self().Name] = typeDefSelf
		case core.TypeDefKindInput:
			typeByName[typeDefSelf.AsInput.Value.Self().Name] = typeDefSelf
		case core.TypeDefKindEnum:
			typeByName[typeDefSelf.AsEnum.Value.Self().Name] = typeDefSelf
		}
	}

	// just verify some subset of objects+functions as a sanity check

	// Container
	ctrTypeDef, ok := typeByName["Container"]
	require.True(t, ok)
	ctrObj := ctrTypeDef.AsObject.Value.Self()

	_, ok = ctrObj.FunctionByName("id")
	require.False(t, ok)

	fileFn, ok := ctrObj.FunctionByName("file")
	require.True(t, ok)
	require.Equal(t, core.TypeDefKindObject, fileFn.ReturnType.Self().Kind)
	require.Equal(t, "File", fileFn.ReturnType.Self().AsObject.Value.Self().Name)
	require.Len(t, fileFn.Args, 2)

	fileFnPathArg := fileFn.Args[0].Self()
	require.Equal(t, "path", fileFnPathArg.Name)
	require.Equal(t, core.TypeDefKindString, fileFnPathArg.TypeDef.Self().Kind)
	require.False(t, fileFnPathArg.TypeDef.Self().Optional)

	fileFnExpandArg := fileFn.Args[1].Self()
	require.Equal(t, "expand", fileFnExpandArg.Name)
	require.Equal(t, core.TypeDefKindBoolean, fileFnExpandArg.TypeDef.Self().Kind)

	withMountedDirectoryFn, ok := ctrObj.FunctionByName("withMountedDirectory")
	require.True(t, ok)

	withMountedDirectoryFnPathArg := withMountedDirectoryFn.Args[0].Self()
	require.Equal(t, "path", withMountedDirectoryFnPathArg.Name)
	require.Equal(t, core.TypeDefKindString, withMountedDirectoryFnPathArg.TypeDef.Self().Kind)
	require.False(t, withMountedDirectoryFnPathArg.TypeDef.Self().Optional)

	withMountedDirectoryFnSourceArg := withMountedDirectoryFn.Args[1].Self()
	require.Equal(t, "source", withMountedDirectoryFnSourceArg.Name)
	require.Equal(t, core.TypeDefKindObject, withMountedDirectoryFnSourceArg.TypeDef.Self().Kind)
	require.Equal(t, "Directory", withMountedDirectoryFnSourceArg.TypeDef.Self().AsObject.Value.Self().Name)
	require.False(t, withMountedDirectoryFnSourceArg.TypeDef.Self().Optional)

	withMountedDirectoryFnOwnerArg := withMountedDirectoryFn.Args[2].Self()
	require.Equal(t, "owner", withMountedDirectoryFnOwnerArg.Name)
	require.Equal(t, core.TypeDefKindString, withMountedDirectoryFnOwnerArg.TypeDef.Self().Kind)
	require.True(t, withMountedDirectoryFnOwnerArg.TypeDef.Self().Optional)

	// PortForward input type
	portForwardTypeDef, ok := typeByName["PortForward"]
	require.True(t, ok)
	require.Equal(t, core.TypeDefKindInput, portForwardTypeDef.Kind)
	require.Len(t, portForwardTypeDef.AsInput.Value.Self().Fields, 3)
	var frontendPortField *core.FieldTypeDef
	var backendPortField *core.FieldTypeDef
	var protocolField *core.FieldTypeDef
	for _, field := range portForwardTypeDef.AsInput.Value.Self().Fields {
		switch field.Self().Name {
		case "frontend":
			frontendPortField = field.Self()
		case "backend":
			backendPortField = field.Self()
		case "protocol":
			protocolField = field.Self()
		}
	}
	require.NotNil(t, frontendPortField)
	require.Equal(t, core.TypeDefKindInteger, frontendPortField.TypeDef.Self().Kind)
	require.True(t, frontendPortField.TypeDef.Self().Optional)
	require.NotNil(t, backendPortField)
	require.Equal(t, core.TypeDefKindInteger, backendPortField.TypeDef.Self().Kind)
	require.False(t, backendPortField.TypeDef.Self().Optional)
	require.NotNil(t, protocolField)
	require.Equal(t, core.TypeDefKindEnum, protocolField.TypeDef.Self().Kind)

	// File
	fileTypeDef, ok := typeByName["File"]
	require.True(t, ok)
	fileObj := fileTypeDef.AsObject.Value.Self()

	_, ok = fileObj.FunctionByName("id")
	require.False(t, ok)

	exportFn, ok := fileObj.FunctionByName("export")
	require.True(t, ok)
	require.Equal(t, core.TypeDefKindString, exportFn.ReturnType.Self().Kind)
	require.Len(t, exportFn.Args, 2)

	exportFnPathArg := exportFn.Args[0].Self()
	require.Equal(t, "path", exportFnPathArg.Name)
	require.Equal(t, core.TypeDefKindString, exportFnPathArg.TypeDef.Self().Kind)
	require.False(t, exportFnPathArg.TypeDef.Self().Optional)

	exportFnAllowParentDirPathArg := exportFn.Args[1].Self()
	require.Equal(t, "allowParentDirPath", exportFnAllowParentDirPathArg.Name)
	require.Equal(t, core.TypeDefKindBoolean, exportFnAllowParentDirPathArg.TypeDef.Self().Kind)
	require.True(t, exportFnAllowParentDirPathArg.TypeDef.Self().Optional)
}
