package flattening_test

import (
	"fmt"
	"testing"

	"github.com/dagger/dagger/core/artifact/flattening"
	"github.com/stretchr/testify/require"
)

func ExampleLocalFieldPath() {
	route := []flattening.PathStep{
		{Field: "app", ModuleRoot: true},
		{Field: "packages"},
		{Field: "get", CollectionItem: true},
		{Field: "buildOptions"},
		{Field: "container"},
	}
	path := flattening.LocalFieldPath(route)
	coordinates := flattening.CanonicalCoordinates("app", path, []flattening.Coordinate{
		{Dimension: "AppPackages", Key: "sdk"},
	})
	for _, coordinate := range coordinates {
		fmt.Printf("%s = %s\n", coordinate.Dimension, coordinate.Key)
	}
	// Output:
	// module = app
	// AppPackages = sdk
	// name = buildOptions.container
}

func TestLocalFieldPath(t *testing.T) {
	module := flattening.PathStep{Field: "app", ModuleRoot: true}
	projects := flattening.PathStep{Field: "projects"}
	item := flattening.PathStep{Field: "get", CollectionItem: true}
	services := flattening.PathStep{Field: "services"}
	for _, tc := range []struct {
		name  string
		route []flattening.PathStep
		want  []string
	}{
		{name: "no route"},
		{name: "module root", route: []flattening.PathStep{module}},
		{
			name: "fields from module root",
			route: []flattening.PathStep{module,
				{Field: "buildOptions"}, {Field: "container"}},
			want: []string{"buildOptions", "container"},
		},
		{
			name: "fields from package item",
			route: []flattening.PathStep{module, {Field: "packages"}, item,
				{Field: "options"}, {Field: "container"}},
			want: []string{"options", "container"},
		},
		{
			name:  "direct nested item",
			route: []flattening.PathStep{module, projects, item, services, item},
		},
		{
			name: "field from nearest nested item",
			route: []flattening.PathStep{module, projects, item, services, item,
				{Field: "healthCheck"}, {Field: "run"}},
			want: []string{"healthCheck", "run"},
		},
		{
			name: "collection method stays relative to parent item",
			route: []flattening.PathStep{module, projects, item, services,
				{Field: "all"}, {Field: "up"}},
			want: []string{"services", "all", "up"},
		},
		{
			name:  "collection method without enclosing item",
			route: []flattening.PathStep{module, projects, {Field: "all"}},
			want:  []string{"projects", "all"},
		},
		{
			name: "exposed bound root",
			route: []flattening.PathStep{{Field: "toolAlias", ModuleRoot: true},
				{Field: "healthCheck"}},
			want: []string{"healthCheck"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, flattening.LocalFieldPath(tc.route))
		})
	}
}
