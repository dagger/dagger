package core

import (
	"context"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceCheckGroupCollectionFilterValues(t *testing.T) {
	ctx := context.Background()

	mod, _, collectionType := testCollectionModule(t)
	group := &CheckGroup{
		Checks: []*Check{
			{Node: workspaceCheckLeaf(t, mod, collectionType, "alpha", []string{"TestFoo"})},
			{Node: workspaceCheckLeaf(t, mod, collectionType, "beta", []string{"TestBar"})},
		},
	}

	values, err := group.CollectionFilterValues(ctx, []string{"GoTests"})
	require.NoError(t, err)
	require.Len(t, values, 1)
	require.Equal(t, "GoTests", values[0].TypeName)
	require.Equal(t, []string{"TestFoo", "TestBar"}, values[0].Values)
}

func TestWorkspaceGeneratorGroupCollectionFilterValues(t *testing.T) {
	ctx := context.Background()

	mod, _, collectionType := testCollectionModule(t)
	group := &GeneratorGroup{
		Generators: []*Generator{
			{Node: workspaceGeneratorLeaf(t, mod, collectionType, "alpha", []string{"TestFoo"})},
			{Node: workspaceGeneratorLeaf(t, mod, collectionType, "beta", []string{"TestBar"})},
		},
	}

	values, err := group.CollectionFilterValues(ctx, []string{"GoTests"})
	require.NoError(t, err)
	require.Len(t, values, 1)
	require.Equal(t, "GoTests", values[0].TypeName)
	require.Equal(t, []string{"TestFoo", "TestBar"}, values[0].Values)
}

func workspaceCheckLeaf(t *testing.T, mod *Module, collectionType *TypeDef, modName string, names []string) *ModTreeNode {
	t.Helper()
	root := workspaceCollectionRoot(t, mod, collectionType, modName, names)
	return &ModTreeNode{
		Parent:         root,
		Name:           "runTest",
		Module:         mod,
		OriginalModule: mod,
		Type:           (&TypeDef{}).WithKind(TypeDefKindString),
		IsCheck:        true,
	}
}

func workspaceGeneratorLeaf(t *testing.T, mod *Module, collectionType *TypeDef, modName string, names []string) *ModTreeNode {
	t.Helper()
	root := workspaceCollectionRoot(t, mod, collectionType, modName, names)
	return &ModTreeNode{
		Parent:         root,
		Name:           "generate",
		Module:         mod,
		OriginalModule: mod,
		Type:           (&TypeDef{}).WithKind(TypeDefKindString),
		IsGenerator:    true,
	}
}

func workspaceCollectionRoot(t *testing.T, mod *Module, collectionType *TypeDef, modName string, names []string) *ModTreeNode {
	t.Helper()

	collectionObj := &ModuleObject{
		Module:     mod,
		TypeDef:    collectionType.AsObject.Value,
		Collection: collectionType.AsCollection.Value,
		Fields: map[string]any{
			"names": names,
		},
	}

	return &ModTreeNode{
		Parent:         &ModTreeNode{},
		Name:           modName,
		Module:         mod,
		OriginalModule: mod,
		Type:           collectionType,
		filterSet:      NewCollectionFilterSet(nil),
		resolveValues: func(context.Context) ([]dagql.AnyResult, error) {
			result, err := dagql.NewResultForCurrentID(context.Background(), collectionObj)
			if err != nil {
				return nil, err
			}
			return []dagql.AnyResult{result}, nil
		},
	}
}
