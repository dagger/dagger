package main

import (
	"testing"

	"dagger.io/dagger"
	"dagger.io/dagger/querybuilder"
	"github.com/stretchr/testify/require"
)

func TestHandleObjectLeafCollection(t *testing.T) {
	q := querybuilder.Query().Select("tests")
	typeDef := &modTypeDef{
		Kind: dagger.TypeDefKindObjectKind,
		AsObject: &modObject{
			Name: "GoTests",
		},
		AsCollection: &modCollection{},
	}

	require.Nil(t, handleObjectLeaf(q, typeDef))
}

func TestModTypeDefKindDisplayCollection(t *testing.T) {
	typeDef := &modTypeDef{
		Kind: dagger.TypeDefKindObjectKind,
		AsObject: &modObject{
			Name: "GoTests",
		},
		AsCollection: &modCollection{},
	}

	require.Equal(t, "Collection", typeDef.KindDisplay())
}
