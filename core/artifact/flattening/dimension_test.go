package flattening

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStaticDimensionNames(t *testing.T) {
	for _, identifier := range []string{ModuleDimension, NameDimension, "type:Check"} {
		require.True(t, IsStaticDimension(identifier), identifier)
	}
	require.False(t, IsStaticDimension("/app/names"))

	dimensions := CanonicalDimensions("app", nil)
	for _, dimension := range dimensions {
		for _, wholeCollection := range []bool{false, true} {
			for _, alias := range dimensions.Names(dimension, wholeCollection) {
				require.Equal(t, dimension.Identifier, alias)
			}
		}
	}
	identifier, err := dimensions.Resolve("name-name")
	require.NoError(t, err)
	require.Equal(t, "name-name", identifier, "an unknown selector must not alias the name dimension")
}

func TestDimensionNames(t *testing.T) {
	dims := Dimensions{
		{Identifier: "golang/modules", Name: "go-module", QualifiedName: "golang-modules"},
		{Identifier: "app/dependencies", Name: "go-module", QualifiedName: "app-dependencies"},
	}
	for _, tc := range []struct{ name, id string }{
		{"golang/modules", "golang/modules"},
		{"golang-modules", "golang/modules"},
		{"app-dependencies", "app/dependencies"},
		{"missing", "missing"},
	} {
		id, err := dims.Resolve(tc.name)
		require.NoError(t, err)
		require.Equal(t, tc.id, id)
	}
	_, err := dims.Resolve("go-module")
	require.EqualError(t, err, `ambiguous dimension "go-module": use golang/modules or app/dependencies`)
	require.Equal(t, "golang-go-module", dims.DisplayName(dims[0]))
	require.Equal(t, "app-go-module", dims.DisplayName(dims[1]))
	require.Equal(t, "go-module", dims[:1].DisplayName(dims[0]))

	// An alias can also conflict with another dimension's qualified name.
	dims = append(dims, &Dimension{Identifier: "other/modules", Name: "golang-go-module", QualifiedName: "golang-go-module"})
	resolved, err := dims.Resolve(dims.DisplayName(dims[0]))
	require.NoError(t, err)
	require.Equal(t, dims[0].Identifier, resolved)
}

func TestCollectionSelectorNames(t *testing.T) {
	dims := Dimensions{
		{Identifier: "/module", Name: "item"},
		{Identifier: "module", Kind: "MODULE", Name: "module"},
		{Identifier: "/tools/linux/modules", Name: "module"},
		{Identifier: "/tools/windows/modules", Name: "module"},
		{Identifier: "/tools/linux/modules/cases", Name: "probe"},
		{Identifier: "/tools/windows/modules/cases", Name: "probe"},
		{Identifier: "/tools/items", Name: "item", QualifiedName: "tools-items-item"},
		{Identifier: "/tools/parents/items", Name: "item", QualifiedName: "tools-parents-items-item"},
	}
	for _, tc := range []struct{ selector, id string }{
		{"module", "module"}, {"/module", "/module"}, {"module-item", "/module"},
		{"tools-linux-module", "/tools/linux/modules"},
		{"tools-windows-module", "/tools/windows/modules"},
		{"tools-linux-probe", "/tools/linux/modules/cases"},
		{"tools-windows-probe", "/tools/windows/modules/cases"},
		{"tools-linux-cases", "/tools/linux/modules/cases"},
		{"tools-items-item", "/tools/items"},
	} {
		id, err := dims.Resolve(tc.selector)
		require.NoError(t, err)
		require.Equal(t, tc.id, id)
	}
}

func TestDimensionNamesInputOrder(t *testing.T) {
	dims := Dimensions{
		{Identifier: "/tools/linux/modules", Name: "tools-module"},
		{Identifier: "/tools/windows/modules", Name: "tools-module"},
		{Identifier: "/tools/linux/modules/cases", Name: "tools-probe"},
		{Identifier: "/tools/windows/modules/cases", Name: "tools-probe"},
	}
	want := map[string]string{
		"/tools/linux/modules":         "tools-linux-module",
		"/tools/windows/modules":       "tools-windows-module",
		"/tools/linux/modules/cases":   "tools-linux-probe",
		"/tools/windows/modules/cases": "tools-windows-probe",
	}
	var permute func(int)
	permute = func(start int) {
		if start == len(dims) {
			for _, dim := range dims {
				name := dims.DisplayName(dim)
				require.Equal(t, want[dim.Identifier], name)
				id, err := dims.Resolve(name)
				require.NoError(t, err)
				require.Equal(t, dim.Identifier, id)
			}
			return
		}
		for i := start; i < len(dims); i++ {
			dims[start], dims[i] = dims[i], dims[start]
			permute(start + 1)
			dims[start], dims[i] = dims[i], dims[start]
		}
	}
	permute(0)
}
