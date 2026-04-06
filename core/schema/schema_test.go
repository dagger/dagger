package schema

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
)

type currentTypeDefsTestServer struct {
	deps *core.SchemaBuilder
}

func TestCoreModTypeDefs(t *testing.T) {
	ctx := context.Background()
	root := &core.Query{}
	baseCache, err := dagql.NewCache(ctx, "", nil)
	require.NoError(t, err)
	ctx = dagql.ContextWithCache(ctx, baseCache)
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		ClientID:  "coremod-typedefs-client",
		SessionID: "coremod-typedefs-session",
	})
	coreSchemaBase, err := NewCoreSchemaBase(ctx)
	require.NoError(t, err)
	dag, err := coreSchemaBase.Fork(ctx, root, "")
	require.NoError(t, err)
	coreMod := coreSchemaBase.CoreMod("")
	coreModDeps := core.NewSchemaBuilder(root, []core.Mod{coreMod})
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

func TestCurrentTypeDefsReturnAllTypes(t *testing.T) {
	ctx := context.Background()
	baseCache, err := dagql.NewCache(ctx, "", nil)
	require.NoError(t, err)
	ctx = dagql.ContextWithCache(ctx, baseCache)
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		ClientID:  "current-typedefs-client",
		SessionID: "current-typedefs-session",
	})
	coreSchemaBase, err := NewCoreSchemaBase(ctx)
	require.NoError(t, err)
	coreMod := coreSchemaBase.CoreMod("")
	coreModDeps := core.NewSchemaBuilder(nil, []core.Mod{coreMod})
	root := core.NewRoot(&currentTypeDefsTestServer{deps: coreModDeps}, nil)
	dag, err := coreSchemaBase.Fork(ctx, root, "")
	require.NoError(t, err)

	var topLevel dagql.ObjectResultArray[*core.TypeDef]
	err = dag.Select(ctx, dag.Root(), &topLevel, dagql.Selector{Field: "currentTypeDefs"})
	require.NoError(t, err)

	var allTypeDefs dagql.ObjectResultArray[*core.TypeDef]
	err = dag.Select(ctx, dag.Root(), &allTypeDefs, dagql.Selector{
		Field: "currentTypeDefs",
		Args: []dagql.NamedInput{
			{Name: "returnAllTypes", Value: dagql.Boolean(true)},
		},
	})
	require.NoError(t, err)

	require.Greater(t, len(allTypeDefs), len(topLevel))

	topLevelIDs := make(map[uint64]struct{}, len(topLevel))
	allIDs := make(map[uint64]struct{}, len(allTypeDefs))
	allNames := make(map[string]struct{}, len(allTypeDefs))
	allKinds := make(map[string]core.TypeDefKind, len(allTypeDefs))
	extraTypeCount := 0

	for _, typeDef := range topLevel {
		id, err := typeDef.ID()
		require.NoError(t, err)
		topLevelIDs[id.EngineResultID()] = struct{}{}
	}
	for _, typeDef := range allTypeDefs {
		id, err := typeDef.ID()
		require.NoError(t, err)
		_, dup := allIDs[id.EngineResultID()]
		require.False(t, dup, "duplicate typedef result id %d", id.EngineResultID())
		allIDs[id.EngineResultID()] = struct{}{}
		require.False(t, typeDef.Self().Optional)
		require.NotEmpty(t, typeDef.Self().Name)
		_, dup = allNames[typeDef.Self().Name]
		require.False(t, dup, "duplicate typedef name %s", typeDef.Self().Name)
		allNames[typeDef.Self().Name] = struct{}{}
		allKinds[typeDef.Self().Name] = typeDef.Self().Kind
		if _, topLevel := topLevelIDs[id.EngineResultID()]; !topLevel {
			extraTypeCount++
		}
	}
	require.Greater(t, extraTypeCount, 0)
	require.Equal(t, core.TypeDefKindString, allKinds["String"])
	require.Equal(t, core.TypeDefKindBoolean, allKinds["Boolean"])
}

func TestCurrentTypeDefsReturnAllTypesAfterSessionRelease(t *testing.T) {
	baseCtx := context.Background()
	baseCache, err := dagql.NewCache(baseCtx, "", nil)
	require.NoError(t, err)
	baseCtx = dagql.ContextWithCache(baseCtx, baseCache)

	ctxSessionA := engine.ContextWithClientMetadata(baseCtx, &engine.ClientMetadata{
		ClientID:  "current-typedefs-release-client-a",
		SessionID: "current-typedefs-release-session-a",
	})
	coreSchemaBase, err := NewCoreSchemaBase(ctxSessionA)
	require.NoError(t, err)
	coreMod := coreSchemaBase.CoreMod("")
	coreModDeps := core.NewSchemaBuilder(nil, []core.Mod{coreMod})
	root := core.NewRoot(&currentTypeDefsTestServer{deps: coreModDeps}, nil)
	dagA, err := coreSchemaBase.Fork(ctxSessionA, root, "")
	require.NoError(t, err)

	var initial dagql.ObjectResultArray[*core.TypeDef]
	err = dagA.Select(ctxSessionA, dagA.Root(), &initial, dagql.Selector{
		Field: "currentTypeDefs",
		Args: []dagql.NamedInput{
			{Name: "returnAllTypes", Value: dagql.Boolean(true)},
		},
	})
	require.NoError(t, err)
	require.Greater(t, len(initial), 0)

	require.NoError(t, baseCache.ReleaseSession(ctxSessionA, "current-typedefs-release-session-a"))

	ctxSessionB := engine.ContextWithClientMetadata(baseCtx, &engine.ClientMetadata{
		ClientID:  "current-typedefs-release-client-b",
		SessionID: "current-typedefs-release-session-b",
	})
	dagB, err := coreSchemaBase.Fork(ctxSessionB, root, "")
	require.NoError(t, err)

	var afterRelease dagql.ObjectResultArray[*core.TypeDef]
	err = dagB.Select(ctxSessionB, dagB.Root(), &afterRelease, dagql.Selector{
		Field: "currentTypeDefs",
		Args: []dagql.NamedInput{
			{Name: "returnAllTypes", Value: dagql.Boolean(true)},
		},
	})
	require.NoError(t, err)
	require.Greater(t, len(afterRelease), 0)
}
