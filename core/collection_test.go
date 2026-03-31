package core

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateCollectionTypeDefDefaultMembers(t *testing.T) {
	ctx := context.Background()
	mod := &Module{Deps: NewSchemaBuilder(nil, nil)}

	itemType := (&TypeDef{}).WithObject("GoTest", "", nil, nil)
	getFn := NewFunction("Get", itemType).
		WithArg("name", (&TypeDef{}).WithKind(TypeDefKindString), "", nil, "", "", nil, nil, nil)

	collectionType := (&TypeDef{}).WithObject("GoTests", "", nil, nil).WithCollection()

	var err error
	collectionType, err = collectionType.WithObjectField(
		"keys",
		(&TypeDef{}).WithListOf((&TypeDef{}).WithKind(TypeDefKindString)),
		"",
		nil,
		nil,
	)
	require.NoError(t, err)

	collectionType, err = collectionType.WithFunction(getFn)
	require.NoError(t, err)

	require.NoError(t, mod.validateTypeDef(ctx, collectionType))
	require.True(t, collectionType.AsCollection.Valid)

	collection := collectionType.AsCollection.Value
	require.Equal(t, "keys", collection.KeysFieldName)
	require.Equal(t, "get", collection.GetFunctionName)
	require.Equal(t, "name", collection.GetArgName)
	require.NotNil(t, collection.KeyType)
	require.Equal(t, TypeDefKindString, collection.KeyType.Kind)
	require.NotNil(t, collection.ValueType)
	require.Equal(t, TypeDefKindObject, collection.ValueType.Kind)
	require.Equal(t, "GoTest", collection.ValueType.AsObject.Value.Name)
}

func TestValidateCollectionTypeDefExplicitOverrides(t *testing.T) {
	ctx := context.Background()
	mod := &Module{Deps: NewSchemaBuilder(nil, nil)}

	itemType := (&TypeDef{}).WithObject("GoModule", "", nil, nil)
	getFn := NewFunction("Module", itemType).
		WithArg("path", (&TypeDef{}).WithKind(TypeDefKindString), "", nil, "", "", nil, nil, nil)

	collectionType := (&TypeDef{}).
		WithObject("GoModules", "", nil, nil).
		WithCollection().
		WithCollectionKeys("paths").
		WithCollectionGet("module")

	var err error
	collectionType, err = collectionType.WithObjectField(
		"paths",
		(&TypeDef{}).WithListOf((&TypeDef{}).WithKind(TypeDefKindString)),
		"",
		nil,
		nil,
	)
	require.NoError(t, err)

	collectionType, err = collectionType.WithFunction(getFn)
	require.NoError(t, err)

	require.NoError(t, mod.validateTypeDef(ctx, collectionType))

	collection := collectionType.AsCollection.Value
	require.Equal(t, "paths", collection.KeysFieldName)
	require.Equal(t, "module", collection.GetFunctionName)
	require.Equal(t, "path", collection.GetArgName)
}

func TestValidateCollectionTypeDefErrors(t *testing.T) {
	ctx := context.Background()
	mod := &Module{Deps: NewSchemaBuilder(nil, nil)}

	tests := []struct {
		name     string
		typeDef  *TypeDef
		contains string
	}{
		{
			name:     "missing keys field",
			typeDef:  collectionTypeWithMembers(t, false, true, nil),
			contains: `must define exactly one effective keys field`,
		},
		{
			name: "keys function unsupported",
			typeDef: collectionTypeWithMembers(t, false, true, func(def *TypeDef) *TypeDef {
				keysFn := NewFunction("Keys", (&TypeDef{}).WithListOf((&TypeDef{}).WithKind(TypeDefKindString))).
					WithArg("prefix", (&TypeDef{}).WithKind(TypeDefKindString), "", nil, "", "", nil, nil, nil)
				def, err := def.WithFunction(keysFn)
				require.NoError(t, err)
				return def
			}),
			contains: `must define exactly one effective keys field`,
		},
		{
			name: "get key type mismatch",
			typeDef: collectionTypeWithMembers(t, true, true, func(def *TypeDef) *TypeDef {
				def.AsObject.Value.Functions = nil
				getFn := NewFunction("Get", (&TypeDef{}).WithObject("GoTest", "", nil, nil)).
					WithArg("name", (&TypeDef{}).WithKind(TypeDefKindInteger), "", nil, "", "", nil, nil, nil)
				def, err := def.WithFunction(getFn)
				require.NoError(t, err)
				return def
			}),
			contains: `must match keys field type`,
		},
		{
			name:     "get returns non object",
			typeDef:  collectionTypeWithMembers(t, true, false, nil),
			contains: `must return an object`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := mod.validateTypeDef(ctx, tt.typeDef)
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.contains)
		})
	}
}

func collectionTypeWithMembers(t *testing.T, includeKeys bool, objectReturn bool, mutate func(*TypeDef) *TypeDef) *TypeDef {
	t.Helper()

	itemReturnType := (&TypeDef{}).WithObject("GoTest", "", nil, nil)
	if !objectReturn {
		itemReturnType = (&TypeDef{}).WithKind(TypeDefKindString)
	}

	def := (&TypeDef{}).WithObject("GoTests", "", nil, nil).WithCollection()

	var err error
	if includeKeys {
		def, err = def.WithObjectField(
			"keys",
			(&TypeDef{}).WithListOf((&TypeDef{}).WithKind(TypeDefKindString)),
			"",
			nil,
			nil,
		)
		require.NoError(t, err)
	}

	getFn := NewFunction("Get", itemReturnType).
		WithArg("name", (&TypeDef{}).WithKind(TypeDefKindString), "", nil, "", "", nil, nil, nil)
	def, err = def.WithFunction(getFn)
	require.NoError(t, err)

	if mutate != nil {
		def = mutate(def)
	}

	return def
}
