package core

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestModuleTypeDefsProjectCollections(t *testing.T) {
	ctx := context.Background()
	mod := &Module{
		NameField:    "go",
		OriginalName: "Go",
		Deps:         NewSchemaBuilder(nil, nil),
	}

	itemType := (&TypeDef{}).WithObject("GoTest", "", nil, nil)
	collectionType := (&TypeDef{}).
		WithObject("GoTests", "Collection of tests.", nil, nil).
		WithCollection().
		WithCollectionKeys("names").
		WithCollectionGet("test")

	var err error
	collectionType, err = collectionType.WithObjectField(
		"names",
		(&TypeDef{}).WithListOf((&TypeDef{}).WithKind(TypeDefKindString)),
		"Raw names field.",
		nil,
		nil,
	)
	require.NoError(t, err)

	getFn := NewFunction("Test", itemType).
		WithArg("name", (&TypeDef{}).WithKind(TypeDefKindString), "Test name.", nil, "", "", nil, nil, nil)
	collectionType, err = collectionType.WithFunction(getFn)
	require.NoError(t, err)

	batchFn := NewFunction("RunTests", (&TypeDef{}).WithKind(TypeDefKindString))
	collectionType, err = collectionType.WithFunction(batchFn)
	require.NoError(t, err)

	rootType := (&TypeDef{}).WithObject("Go", "", nil, nil)
	rootType, err = rootType.WithObjectField("tests", collectionType.Clone(), "", nil, nil)
	require.NoError(t, err)

	mod.ObjectDefs = []*TypeDef{rootType, itemType, collectionType}
	require.NoError(t, mod.validateTypeDef(ctx, collectionType))
	require.NoError(t, mod.validateTypeDef(ctx, rootType))

	typeDefs, err := mod.TypeDefs(ctx, nil)
	require.NoError(t, err)

	typeByName := map[string]*TypeDef{}
	for _, typeDef := range typeDefs {
		if typeDef.AsObject.Valid {
			typeByName[typeDef.AsObject.Value.Name] = typeDef
		}
	}

	projectedCollection, ok := typeByName["GoTests"]
	require.True(t, ok)
	require.True(t, projectedCollection.AsCollection.Valid)

	projectedObj := projectedCollection.AsObject.Value
	require.Len(t, projectedObj.Fields, 3)
	require.Equal(t, "keys", projectedObj.Fields[0].Name)
	require.Equal(t, "list", projectedObj.Fields[1].Name)
	require.Equal(t, "batch", projectedObj.Fields[2].Name)
	require.Len(t, projectedObj.Functions, 2)
	require.Equal(t, "get", projectedObj.Functions[0].Name)
	require.Equal(t, "subset", projectedObj.Functions[1].Name)

	require.NotNil(t, projectedCollection.AsCollection.Value.BatchType)
	require.Equal(t, "GoTests_Batch", projectedCollection.AsCollection.Value.BatchType.AsObject.Value.Name)

	projectedBatch, ok := typeByName["GoTests_Batch"]
	require.True(t, ok)
	require.Len(t, projectedBatch.AsObject.Value.Functions, 1)
	require.Equal(t, "runTests", projectedBatch.AsObject.Value.Functions[0].Name)

	rootProjected, ok := typeByName["Go"]
	require.True(t, ok)
	testsField, ok := rootProjected.AsObject.Value.FieldByName("tests")
	require.True(t, ok)
	require.True(t, testsField.TypeDef.AsCollection.Valid)
	require.Equal(t, "GoTests", testsField.TypeDef.AsObject.Value.Name)
}
