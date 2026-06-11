package core

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
)

func TestCollectionDefinitionDefaultMembers(t *testing.T) {
	dag := newTypeDefTestDag(t)
	typeDef := newCollectionTypeDefForTest(t, dag, "GoTests", "GoTest", "keys", "get", "name")

	info, err := collectionDefinition(typeDef)
	require.NoError(t, err)
	require.NotNil(t, info)

	require.Equal(t, "keys", info.keysField.Self().Name)
	require.Equal(t, "get", info.getFn.Self().Name)
	require.Equal(t, "name", info.getArg.Self().Name)
	require.Equal(t, TypeDefKindString, info.keyType.Self().Kind)
	require.Equal(t, "GoTest", info.valueType.Self().AsObject.Value.Self().Name)
}

func TestCollectionDefinitionExplicitOverrides(t *testing.T) {
	dag := newTypeDefTestDag(t)
	typeDef := newCollectionTypeDefForTest(t, dag, "GoModules", "GoModule", "paths", "module", "path")

	info, err := collectionDefinition(typeDef)
	require.NoError(t, err)
	require.NotNil(t, info)

	require.Equal(t, "paths", info.keysField.Self().Name)
	require.Equal(t, "module", info.getFn.Self().Name)
	require.Equal(t, "path", info.getArg.Self().Name)
	require.Equal(t, "GoModule", info.valueType.Self().AsObject.Value.Self().Name)
}

func TestCollectionDefinitionErrors(t *testing.T) {
	dag := newTypeDefTestDag(t)

	tests := []struct {
		name     string
		mutate   func(dagql.ObjectResult[*TypeDef])
		contains string
	}{
		{
			name: "missing keys field",
			mutate: func(typeDef dagql.ObjectResult[*TypeDef]) {
				typeDef.Self().AsObject.Value.Self().Fields = nil
			},
			contains: `must define exactly one effective keys field`,
		},
		{
			name: "keys field must be non-null list",
			mutate: func(typeDef dagql.ObjectResult[*TypeDef]) {
				keysField := typeDef.Self().AsObject.Value.Self().Fields[0].Self()
				keysField.TypeDef = newListTypeDefForTest(t, dag, "optionalKeys", collectionStringTypeForTest(t, dag), true)
			},
			contains: `keys field "keys" must be a non-null list`,
		},
		{
			name: "keys field elements must be non-null",
			mutate: func(typeDef dagql.ObjectResult[*TypeDef]) {
				keysField := typeDef.Self().AsObject.Value.Self().Fields[0].Self()
				optionalString := newTypeDefDetachedResult(t, dag, "optionalString", (&TypeDef{}).WithKind(TypeDefKindString).WithOptional(true))
				keysField.TypeDef = newListTypeDefForTest(t, dag, "optionalKeyElements", optionalString, false)
			},
			contains: `keys field "keys" must be a list of non-null keys`,
		},
		{
			name: "get key type must match keys",
			mutate: func(typeDef dagql.ObjectResult[*TypeDef]) {
				getArg := typeDef.Self().AsObject.Value.Self().Functions[0].Self().Args[0].Self()
				getArg.TypeDef = newTypeDefDetachedResult(t, dag, "integerKey", (&TypeDef{}).WithKind(TypeDefKindInteger))
			},
			contains: `argument "name" must match keys field type`,
		},
		{
			name: "get argument must be non-null",
			mutate: func(typeDef dagql.ObjectResult[*TypeDef]) {
				getArg := typeDef.Self().AsObject.Value.Self().Functions[0].Self().Args[0].Self()
				getArg.TypeDef = newTypeDefDetachedResult(t, dag, "optionalGetArg", (&TypeDef{}).WithKind(TypeDefKindString).WithOptional(true))
			},
			contains: `argument "name" must be non-null`,
		},
		{
			name: "get must return non-null object",
			mutate: func(typeDef dagql.ObjectResult[*TypeDef]) {
				getFn := typeDef.Self().AsObject.Value.Self().Functions[0].Self()
				getFn.ReturnType = newTypeDefDetachedResult(t, dag, "optionalItem", getFn.ReturnType.Self().WithOptional(true))
			},
			contains: `must return a non-null object`,
		},
		{
			name: "get must return object",
			mutate: func(typeDef dagql.ObjectResult[*TypeDef]) {
				getFn := typeDef.Self().AsObject.Value.Self().Functions[0].Self()
				getFn.ReturnType = collectionStringTypeForTest(t, dag)
			},
			contains: `must return an object`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			typeDef := newCollectionTypeDefForTest(t, dag, "GoTests", "GoTest", "keys", "get", "name")
			tt.mutate(typeDef)

			_, err := collectionDefinition(typeDef)
			require.ErrorContains(t, err, tt.contains)
		})
	}
}

func TestCollectionSubsetKeysIsExactAndParentOrdered(t *testing.T) {
	dag := newTypeDefTestDag(t)
	typeDef := newCollectionTypeDefForTest(t, dag, "GoTests", "GoTest", "keys", "get", "name")
	obj := &ModuleObject{
		TypeDef:    typeDef.Self().AsObject.Value.Self(),
		Collection: typeDef.Self().AsCollection.Value.Self(),
		Fields: map[string]any{
			"keys": []string{"unit", "lint", "integration"},
		},
	}

	keys, err := obj.collectionSubsetKeys([]any{"integration", "unit"})
	require.NoError(t, err)
	require.Equal(t, []any{"unit", "integration"}, keys)

	_, err = obj.collectionSubsetKeys([]any{"unit", "unit"})
	require.ErrorContains(t, err, `subset contains duplicate key "unit"`)

	_, err = obj.collectionSubsetKeys([]any{"missing"})
	require.ErrorContains(t, err, `does not contain key "missing"`)
}

func TestCollectionBatchTypeDefExcludesCoreAlgebra(t *testing.T) {
	dag := newTypeDefTestDag(t)
	typeDef := newCollectionTypeDefForTest(t, dag, "GoTests", "GoTest", "keys", "get", "name")
	backing := typeDef.Self().AsObject.Value.Self()
	batchFn := NewFunction("Run", collectionStringTypeForTest(t, dag))
	backing.Functions = append(backing.Functions, newTypeDefDetachedResult(t, dag, "runBatchFn", batchFn))

	batchType := collectionBatchTypeDef(backing, typeDef.Self().AsCollection.Value.Self())
	require.NotNil(t, batchType)
	require.Equal(t, "GoTestsBatch", batchType.Name)
	require.Len(t, batchType.Functions, 1)
	require.Equal(t, "run", batchType.Functions[0].Self().Name)
}

func newCollectionTypeDefForTest(
	t *testing.T,
	dag *dagql.Server,
	collectionName string,
	itemName string,
	keysName string,
	getName string,
	getArgName string,
) dagql.ObjectResult[*TypeDef] {
	t.Helper()

	keyType := collectionStringTypeForTest(t, dag)
	keysType := newListTypeDefForTest(t, dag, collectionName+"Keys", keyType, false)
	itemType := newObjectTypeDefForTest(t, dag, itemName)
	keysField := newFieldTypeDefForTest(t, dag, keysName, keysType)
	getArg := newFunctionArgForTest(t, dag, getArgName, keyType)
	getFn := NewFunction(getName, itemType).WithArg(getArg)
	getFnRes := newTypeDefDetachedResult(t, dag, collectionName+"GetFn", getFn)

	obj := NewObjectTypeDef(collectionName, "", nil)
	obj.Fields = append(obj.Fields, keysField)
	obj.Functions = append(obj.Functions, getFnRes)
	objRes := newTypeDefDetachedResult(t, dag, collectionName+"Object", obj)

	collection := &CollectionTypeDef{
		KeyType:         dagql.NonNull(keyType),
		ValueType:       dagql.NonNull(itemType),
		KeysFieldName:   gqlFieldName(keysName),
		GetFunctionName: gqlFieldName(getName),
		GetArgName:      gqlArgName(getArgName),
	}
	if keysName != collectionKeysFieldName {
		collection.KeysFieldNameOverride = gqlFieldName(keysName)
	}
	if getName != collectionGetFunctionName {
		collection.GetFunctionNameOverride = gqlFieldName(getName)
	}
	collectionRes := newTypeDefDetachedResult(t, dag, collectionName+"Collection", collection)

	typeDef := (&TypeDef{}).WithObject(objRes).WithCollection(collectionRes)
	return newTypeDefDetachedResult(t, dag, collectionName+"Type", typeDef)
}

func collectionStringTypeForTest(t *testing.T, dag *dagql.Server) dagql.ObjectResult[*TypeDef] {
	t.Helper()
	return newTypeDefDetachedResult(t, dag, "stringType", (&TypeDef{}).WithKind(TypeDefKindString))
}

func newObjectTypeDefForTest(t *testing.T, dag *dagql.Server, name string) dagql.ObjectResult[*TypeDef] {
	t.Helper()
	obj := NewObjectTypeDef(name, "", nil)
	objRes := newTypeDefDetachedResult(t, dag, name+"Object", obj)
	typeDef := (&TypeDef{}).WithObject(objRes)
	return newTypeDefDetachedResult(t, dag, name+"Type", typeDef)
}

func newListTypeDefForTest(
	t *testing.T,
	dag *dagql.Server,
	op string,
	element dagql.ObjectResult[*TypeDef],
	optional bool,
) dagql.ObjectResult[*TypeDef] {
	t.Helper()
	list := newTypeDefDetachedResult(t, dag, op+"List", &ListTypeDef{ElementTypeDef: element})
	typeDef := (&TypeDef{}).WithListOf(list).WithOptional(optional)
	return newTypeDefDetachedResult(t, dag, op+"ListType", typeDef)
}

func newFieldTypeDefForTest(t *testing.T, dag *dagql.Server, name string, typeDef dagql.ObjectResult[*TypeDef]) dagql.ObjectResult[*FieldTypeDef] {
	t.Helper()
	return newTypeDefDetachedResult(t, dag, name+"Field", &FieldTypeDef{
		Name:         gqlFieldName(name),
		OriginalName: name,
		TypeDef:      typeDef,
	})
}

func newFunctionArgForTest(t *testing.T, dag *dagql.Server, name string, typeDef dagql.ObjectResult[*TypeDef]) dagql.ObjectResult[*FunctionArg] {
	t.Helper()
	return newTypeDefDetachedResult(t, dag, name+"Arg", NewFunctionArg(name, typeDef, "", nil, "", "", nil, nil))
}
