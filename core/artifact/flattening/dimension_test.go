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

func TestCanonicalItemNamesPrecedeCollectionAliases(t *testing.T) {
	rows := (Collection{Type: "Rows", ItemType: "Row"}).CanonicalDimension()
	cells := (Collection{Type: "Row", ItemType: "Cell"}).CanonicalDimension()
	dimensions := Dimensions{rows, cells}
	for _, tc := range []struct{ name, identifier string }{{"row", "Rows"}, {"cell", "Row"}, {"Rows", "Rows"}, {"Row", "Row"}} {
		identifier, err := dimensions.Resolve(tc.name)
		require.NoError(t, err)
		require.Equal(t, tc.identifier, identifier)
	}
	require.Equal(t, "row", dimensions.DisplayName(rows))
	require.Equal(t, "cell", dimensions.DisplayName(cells))
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

func TestNameDimensionsWholeSchema(t *testing.T) {
	input := Dimensions{
		(Collection{Type: "Packages", ItemType: "Package"}).CanonicalDimension(),
		(Collection{Type: "Dependencies", ItemType: "Package"}).CanonicalDimension(),
		(Collection{Type: "Tests", ItemType: "Test"}).CanonicalDimension(),
	}
	got, err := NameDimensions(input)
	require.NoError(t, err)
	require.Equal(t, Dimensions{
		{Kind: "COLLECTION", Identifier: "Dependencies", CollectionType: "Dependencies", ItemType: "Package", Name: "dependencies", QualifiedName: "dependencies"},
		{Kind: "COLLECTION", Identifier: "Packages", CollectionType: "Packages", ItemType: "Package", Name: "packages", QualifiedName: "packages"},
		{Kind: "COLLECTION", Identifier: "Tests", CollectionType: "Tests", ItemType: "Test", Name: "test", QualifiedName: "tests"},
	}, got)
	require.Equal(t, "package", input[0].Name)
	require.Equal(t, "package", input[1].Name)
	again, err := NameDimensions(got)
	require.NoError(t, err)
	require.Equal(t, got, again)
	reordered, err := NameDimensions(Dimensions{input[2], input[1], input[0]})
	require.NoError(t, err)
	require.Equal(t, got, reordered)
	got[0].KeyName = "changed"
	require.Empty(t, input[1].KeyName)
	require.Empty(t, again[0].KeyName)
}

func TestNameDimensionsCascadingCollision(t *testing.T) {
	got, err := NameDimensions(Dimensions{
		(Collection{Type: "Packages", ItemType: "Package"}).CanonicalDimension(),
		(Collection{Type: "Dependencies", ItemType: "Package"}).CanonicalDimension(),
		(Collection{Type: "Groups", ItemType: "Packages"}).CanonicalDimension(),
	})
	require.NoError(t, err)
	require.Equal(t, []string{"dependencies", "groups", "packages"}, []string{got[0].Name, got[1].Name, got[2].Name})
}

func TestNameDimensionsStaticNames(t *testing.T) {
	got, err := NameDimensions(Dimensions{
		(Collection{Type: "Names", ItemType: "Name"}).CanonicalDimension(),
		(Collection{Type: "Modules", ItemType: "Module"}).CanonicalDimension(),
	})
	require.NoError(t, err)
	require.Equal(t, []string{"modules", "names"}, []string{got[0].Name, got[1].Name})
	for _, typ := range []string{"Name", "Module"} {
		t.Run(typ, func(t *testing.T) {
			_, err := NameDimensions(Dimensions{(Collection{Type: typ, ItemType: typ}).CanonicalDimension()})
			require.ErrorContains(t, err, "conflicting dimension names")
		})
	}
}

func TestNameDimensionsRejectsUnresolvableTypeNames(t *testing.T) {
	_, err := NameDimensions(Dimensions{
		(Collection{Type: "HTTPTests", ItemType: "Test"}).CanonicalDimension(),
		(Collection{Type: "HttpTests", ItemType: "Test"}).CanonicalDimension(),
	})
	require.ErrorContains(t, err, "conflicting dimension names")
	require.ErrorContains(t, err, "http-tests")
}

func TestNameDimensionsRejectsAnotherColumnIdentifier(t *testing.T) {
	_, err := NameDimensions(Dimensions{
		(Collection{Type: "FooTests", ItemType: "Row"}).CanonicalDimension(),
		(Collection{Type: "row", ItemType: "Cell"}).CanonicalDimension(),
	})
	require.ErrorContains(t, err, `dimension name "row" conflicts with collection identifier row`)
}

func TestNameDimensionsKeepsLegacyMetadata(t *testing.T) {
	legacy := &Dimension{Kind: "COLLECTION", Identifier: "/app/tests", CollectionType: "Tests", ItemType: "Name", Name: "app-name", QualifiedName: "app-tests-name"}
	got, err := NameDimensions(Dimensions{legacy})
	require.NoError(t, err)
	require.Equal(t, Dimensions{legacy}, got)
	got[0].Name = "changed"
	require.Equal(t, "app-name", legacy.Name)
}
