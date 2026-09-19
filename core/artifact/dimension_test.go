package artifact

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDimensionNames(t *testing.T) {
	dims := Dimensions{
		{Identifier: "Golang.modules", Name: "go-module", QualifiedName: "golang-modules"},
		{Identifier: "App.dependencies", Name: "go-module", QualifiedName: "app-dependencies"},
	}
	for _, tc := range []struct{ name, id string }{
		{"Golang.modules", "Golang.modules"},
		{"golang-modules", "Golang.modules"},
		{"app-dependencies", "App.dependencies"},
		{"missing", "missing"},
	} {
		id, err := dims.Resolve(tc.name)
		require.NoError(t, err)
		require.Equal(t, tc.id, id)
	}
	_, err := dims.Resolve("go-module")
	require.EqualError(t, err, `ambiguous dimension "go-module": use Golang.modules or App.dependencies`)
	require.Equal(t, "golang-modules", dims.DisplayName(dims[0]))
	require.Equal(t, "app-dependencies", dims.DisplayName(dims[1]))
	require.Equal(t, "go-module", dims[:1].DisplayName(dims[0]))

	// An alias can also conflict with another dimension's qualified name.
	dims = append(dims, &Dimension{Identifier: "Other.modules", Name: "golang-modules", QualifiedName: "golang-modules"})
	require.Equal(t, "Golang.modules", dims.DisplayName(dims[0]))
}
