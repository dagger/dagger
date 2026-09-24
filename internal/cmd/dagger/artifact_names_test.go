package daggercmd

import (
	"slices"
	"testing"

	"github.com/dagger/dagger/core/dagaddress"
	"github.com/stretchr/testify/require"
)

func TestArtifactTypeNames(t *testing.T) {
	paths := []artifactListPath{
		{URI: "dag+check://go/modules/test", Dimensions: []string{"Go.modules", "type:Check"}},
		{URI: "dag+check://go/modules/generate/stale", Dimensions: []string{"Go.modules", "type:Check"}},
		{URI: "dag+check://sdk/generate/stale", Dimensions: []string{"type:Check"}},
		{URI: "dag+container://backend/container", Dimensions: []string{"type:Container"}},
		{URI: "dag+container://frontend/container", Dimensions: []string{"type:Container"}},
	}
	index, err := newArtifactNameIndex(paths)
	require.NoError(t, err)
	_, err = index.resolve("type:Check", "stale", nil)
	require.ErrorContains(t, err, "ambiguous Check")
	for _, filter := range []dagaddress.Pair{{Dimension: "Go.modules"}, {Dimension: "Go.modules", Key: ".", HasKey: true}} {
		filters := []dagaddress.Pair{filter, {Dimension: "type:Check", Key: "stale", HasKey: true}, {Dimension: "type:Check", Key: "test", HasKey: true}}
		original := slices.Clone(filters)
		require.NoError(t, resolveArtifactTypeKeys(paths, filters))
		require.Equal(t, "go/modules/generate/stale", filters[1].Key)
		require.Equal(t, "go/modules/test", filters[2].Key)
		require.Equal(t, "stale", index.short("type:Check", filters[1].Key, original))
		slices.Reverse(original)
		require.NoError(t, resolveArtifactTypeKeys(paths, original))
		slices.Reverse(original)
		require.Equal(t, filters, original)
	}
	require.Equal(t, "backend/container", index.short("type:Container", "backend/container", nil))
	paths = append(paths, artifactListPath{URI: "dag+check://other/go/modules/test", Dimensions: []string{"Go.modules", "type:Check"}})
	index, err = newArtifactNameIndex(paths)
	require.NoError(t, err)
	// Even an empty sibling with the same dimensions prevents a short name.
	require.Equal(t, "/go/modules/test", index.short("type:Check", "go/modules/test", []dagaddress.Pair{{Dimension: "Go.modules"}}))
	key, err := index.resolve("type:Check", "/go/modules/test", nil)
	require.NoError(t, err)
	require.Equal(t, "go/modules/test", key)
}
