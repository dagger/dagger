package core

import (
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

func TestArtifactCollectionArgumentAncestor(t *testing.T) {
	object := NewObjectTypeDef("Packages", "", nil)
	object.Collection = &CollectionConfig{Enabled: true}
	obj, err := dagql.NewResultForCall(object, moduleObjectTestSyntheticCall("selectedPackagesType", object))
	require.NoError(t, err)
	typ := (&TypeDef{}).WithObjectTypeDef(dagql.ObjectResult[*ObjectTypeDef]{Result: obj})
	typeResult, err := dagql.NewResultForCall(typ, moduleObjectTestSyntheticCall("selectedPackagesDef", typ))
	require.NoError(t, err)
	def := dagql.ObjectResult[*TypeDef]{Result: typeResult}
	outer := &ModTreeNode{Name: "outer", Type: def}
	inner := &ModTreeNode{Name: "inner", Type: def, Parent: outer}
	item := &ModTreeNode{Name: "get", Parent: inner}
	check := &ModTreeNode{Name: "test", Parent: item}
	arg := NewFunctionArg("selection", def, "", nil, "", "", nil, nil)
	require.Same(t, inner, check.collectionArgumentAncestor(arg))
	require.Nil(t, (&ModTreeNode{Name: "unrelated"}).collectionArgumentAncestor(arg))
	argument, err := dagql.NewResultForCall(arg, moduleObjectTestSyntheticCall("selectionArgument", arg))
	require.NoError(t, err)
	fn := &Function{IsCheck: true, Args: dagql.ObjectResultArray[*FunctionArg]{{Result: argument}}}
	require.False(t, check.functionRequiresCallerArgs(fn))
	require.True(t, (&ModTreeNode{Name: "unrelated"}).functionRequiresCallerArgs(fn))
	fn.IsCheck = false
	require.True(t, check.functionRequiresCallerArgs(fn), "ordinary functions retain caller-supplied arguments")
}
