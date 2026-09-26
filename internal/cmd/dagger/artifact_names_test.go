package daggercmd

import (
	"slices"
	"testing"

	"github.com/dagger/dagger/core/dagaddress"
	"github.com/stretchr/testify/require"
)

func TestArtifactTypeNames(t *testing.T) {
	paths := []artifactListPath{
		{URI: "dag+check://go/modules/test", Dimensions: []string{"go/modules", "type:Check"}},
		{URI: "dag+check://go/modules/generate/stale", Dimensions: []string{"go/modules", "type:Check"}},
		{URI: "dag+check://sdk/generate/stale", Dimensions: []string{"type:Check"}},
		{URI: "dag+container://backend/container", Dimensions: []string{"type:Container"}},
		{URI: "dag+container://frontend/container", Dimensions: []string{"type:Container"}},
	}
	index, err := newArtifactNameIndex(paths)
	require.NoError(t, err)
	require.Equal(t, []string{"go/modules/generate/stale", "sdk/generate/stale"}, index.matches("type:Check", "stale", nil))
	require.Empty(t, index.matches("type:Check", "missing", nil))
	for _, filter := range []dagaddress.Pair{{Dimension: "go/modules"}, {Dimension: "go/modules", Key: ".", HasKey: true}} {
		filters := []dagaddress.Pair{filter, {Dimension: "type:Check", Key: "stale", HasKey: true}, {Dimension: "type:Check", Key: "test", HasKey: true}}
		require.Equal(t, []string{"go/modules/generate/stale"}, index.matches("type:Check", "stale", filters))
		require.Equal(t, []string{"go/modules/test"}, index.matches("type:Check", "test", filters))
		require.Equal(t, "stale", index.short("type:Check", "go/modules/generate/stale", filters))
		slices.Reverse(filters)
		require.Equal(t, []string{"go/modules/generate/stale"}, index.matches("type:Check", "stale", filters))
	}
	require.Equal(t, "backend/container", index.short("type:Container", "backend/container", nil))
	paths = append(paths, artifactListPath{URI: "dag+check://other/go/modules/test", Dimensions: []string{"go/modules", "type:Check"}})
	index, err = newArtifactNameIndex(paths)
	require.NoError(t, err)
	// Even an empty sibling with the same dimensions prevents a short name.
	require.Equal(t, "/go/modules/test", index.short("type:Check", "go/modules/test", []dagaddress.Pair{{Dimension: "go/modules"}}))
	require.Equal(t, []string{"go/modules/test"}, index.matches("type:Check", "/go/modules/test", nil))
}

func TestArtifactModuleNames(t *testing.T) {
	paths := []artifactListPath{
		{URI: "dag+check://test", ModuleName: "go", Dimensions: []string{"module", "type:Check"}},
		{URI: "dag+check://playwright/test", ModuleName: "playwright", Dimensions: []string{"module", "type:Check"}},
	}
	index, err := newArtifactNameIndex(paths)
	require.NoError(t, err)
	for _, path := range paths {
		filters := []dagaddress.Pair{{Dimension: "module", Key: path.ModuleName, HasKey: true}}
		keys := index.matches("type:Check", "test", filters)
		require.Len(t, keys, 1)
		require.Equal(t, "test", index.short("type:Check", keys[0], filters))
	}
	require.Equal(t, []string{"playwright/test", "test"}, index.matches("type:Check", "test", nil))
}

func TestArtifactGeneratorCheckNames(t *testing.T) {
	for _, collection := range []bool{false, true} {
		name, prefix := "module", "tools"
		dimensions := []string{"module", "type:Check"}
		filters := []dagaddress.Pair{{Dimension: "module", Key: "tools", HasKey: true}}
		if collection {
			name, prefix = "collection", "tools/project/modules"
			dimensions = append(dimensions, "tools/project/modules")
			filters = append(filters, dagaddress.Pair{Dimension: "tools/project/modules", Key: ".", HasKey: true})
		}
		t.Run(name, func(t *testing.T) {
			paths := []artifactListPath{
				{URI: "dag+check://" + prefix + "/docs/stale", ModuleName: "tools", Dimensions: dimensions},
				{URI: "dag+check://" + prefix + "/clients/stale", ModuleName: "tools", Dimensions: dimensions},
				{URI: "dag+check://other/docs/stale", ModuleName: "other", Dimensions: dimensions},
			}
			if collection {
				// Another collection can use the same generator names.
				paths = append(paths, artifactListPath{URI: "dag+check://tools/other/docs/stale", ModuleName: "tools", Dimensions: []string{"module", "type:Check", "tools/other"}})
			}
			for range 2 {
				index, err := newArtifactNameIndex(paths)
				require.NoError(t, err)
				for _, generator := range []string{"docs", "clients"} {
					key := prefix + "/" + generator + "/stale"
					short := generator + "/stale"
					require.Equal(t, short, index.short("type:Check", key, filters))
					require.Equal(t, []string{key}, index.matches("type:Check", short, filters))
				}
				require.Equal(t, []string{prefix + "/clients/stale", prefix + "/docs/stale"}, index.matches("type:Check", "stale", filters))
				slices.Reverse(paths)
				slices.Reverse(filters)
			}
		})
	}
}
