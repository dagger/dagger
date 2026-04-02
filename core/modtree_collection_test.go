package core

import (
	"context"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

func TestModTreeCollectionCheckShadowing(t *testing.T) {
	ctx := context.Background()

	mod, rootType, collectionType := testCollectionModule(t)
	root := &ModTreeNode{
		Module:         mod,
		OriginalModule: mod,
		Type:           rootType,
		filterSet:      NewCollectionFilterSet(nil),
	}

	collectionNode := &ModTreeNode{
		Parent:         root,
		Name:           "tests",
		Module:         mod,
		OriginalModule: mod,
		Type:           collectionType,
		filterSet:      NewCollectionFilterSet(nil),
	}

	children, err := collectionNode.Children(ctx)
	require.NoError(t, err)
	require.Len(t, children, 2)
	require.Equal(t, []string{"lint", "runTest"}, []string{children[0].Name, children[1].Name})
	require.True(t, children[0].IsCheck)
	require.True(t, children[1].IsCheck)

	checks, err := root.RollupChecks(ctx, nil, nil)
	require.NoError(t, err)
	require.Len(t, checks, 2)
	require.Equal(t, "tests:lint", checks[0].PathString())
	require.Equal(t, "tests:run-test", checks[1].PathString())
}

func TestCollectionFilterValuesIgnoreSelfFilter(t *testing.T) {
	ctx := context.Background()

	mod, _, collectionType := testCollectionModule(t)
	collectionObj := &ModuleObject{
		Module:     mod,
		TypeDef:    collectionType.AsObject.Value,
		Collection: collectionType.AsCollection.Value,
		Fields: map[string]any{
			"names": []string{"TestFoo", "TestBar"},
		},
	}

	root := &ModTreeNode{
		Module:         mod,
		OriginalModule: mod,
		Type:           collectionType,
		filterSet: NewCollectionFilterSet([]CollectionFilterInput{{
			CollectionType: "GoTests",
			Values:         []string{"TestFoo"},
		}}),
		resolveValues: func(context.Context) ([]dagql.AnyResult, error) {
			result, err := dagql.NewResultForCurrentID(context.Background(), collectionObj)
			if err != nil {
				return nil, err
			}
			return []dagql.AnyResult{result}, nil
		},
	}

	values, err := root.CollectionFilterValues(ctx, []string{"GoTests"}, nil, nil)
	require.NoError(t, err)
	require.Len(t, values, 1)
	require.Equal(t, "GoTests", values[0].TypeName)
	require.Equal(t, []string{"TestFoo", "TestBar"}, values[0].Values)
}

func TestFilteredCollectionKeysIntersectRequestedValues(t *testing.T) {
	mod, _, collectionType := testCollectionModule(t)
	collectionObj := &ModuleObject{
		Module:     mod,
		TypeDef:    collectionType.AsObject.Value,
		Collection: collectionType.AsCollection.Value,
		Fields: map[string]any{
			"names": []string{"TestFoo", "TestBar"},
		},
	}

	node := &ModTreeNode{
		Type: collectionType,
		filterSet: NewCollectionFilterSet([]CollectionFilterInput{{
			CollectionType: "GoTests",
			Values:         []string{"TestBar", "Missing"},
		}}),
	}

	keys, err := node.filteredCollectionKeys(collectionObj, false)
	require.NoError(t, err)
	require.Equal(t, []any{"TestBar"}, keys)
}

func testCollectionModule(t *testing.T) (*Module, *TypeDef, *TypeDef) {
	t.Helper()

	mod := &Module{
		NameField:    "go",
		OriginalName: "Go",
		Deps:         NewSchemaBuilder(nil, nil),
	}

	itemType := (&TypeDef{}).WithObject("GoTest", "", nil, nil)
	var err error
	itemType, err = itemType.WithFunction(NewFunction("RunTest", (&TypeDef{}).WithKind(TypeDefKindString)).WithCheck())
	require.NoError(t, err)
	itemType, err = itemType.WithFunction(NewFunction("Lint", (&TypeDef{}).WithKind(TypeDefKindString)).WithCheck())
	require.NoError(t, err)

	collectionType := (&TypeDef{}).
		WithObject("GoTests", "Collection of tests.", nil, nil).
		WithCollection().
		WithCollectionKeys("names").
		WithCollectionGet("test")
	collectionType, err = collectionType.WithObjectField(
		"names",
		(&TypeDef{}).WithListOf((&TypeDef{}).WithKind(TypeDefKindString)),
		"Raw names field.",
		nil,
		nil,
	)
	require.NoError(t, err)
	collectionType, err = collectionType.WithFunction(
		NewFunction("Test", itemType.Clone()).
			WithArg("name", (&TypeDef{}).WithKind(TypeDefKindString), "Test name.", nil, "", "", nil, nil, nil),
	)
	require.NoError(t, err)
	collectionType, err = collectionType.WithFunction(NewFunction("RunTest", (&TypeDef{}).WithKind(TypeDefKindString)).WithCheck())
	require.NoError(t, err)

	rootType := (&TypeDef{}).WithObject("Go", "", nil, nil)
	rootType, err = rootType.WithObjectField("tests", collectionType.Clone(), "", nil, nil)
	require.NoError(t, err)

	mod.ObjectDefs = []*TypeDef{rootType, itemType, collectionType}
	require.NoError(t, mod.validateTypeDef(context.Background(), itemType))
	require.NoError(t, mod.validateTypeDef(context.Background(), collectionType))
	require.NoError(t, mod.validateTypeDef(context.Background(), rootType))

	return mod, rootType, collectionType
}
