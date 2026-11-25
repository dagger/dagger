package daggercmd

import (
	"testing"

	"dagger.io/dagger/core"
	"github.com/dagger/dagger/core/artifact"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestCollectionCommandDimensionUsesItemTypeName(t *testing.T) {
	collectionType := &modTypeDef{
		Kind:     core.TypeDefKindObjectKind,
		AsObject: &modObject{Name: "PrettierProjects"},
	}
	collection := &modCollection{
		KeyType: &modTypeDef{Kind: core.TypeDefKindStringKind},
		ValueType: &modTypeDef{
			Kind:     core.TypeDefKindObjectKind,
			AsObject: &modObject{Name: "PrettierProject"},
		},
	}

	dimension, err := collectionCommandDimension(
		collectionType,
		collection,
	)
	require.NoError(t, err)
	require.Equal(t, "PrettierProjects", dimension.Identifier)
	require.Equal(t, "prettier-project", dimension.Name)
	require.Equal(t, "PrettierProjects", dimension.QualifiedName)
	require.Equal(t, "prettier-project", artifactDimensionFlagNames(
		&cobra.Command{Use: "write"},
		artifact.Dimensions{dimension},
	)[dimension.Identifier].Key)
}
