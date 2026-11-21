package flattening_test

import (
	"fmt"
	"testing"

	"github.com/dagger/dagger/core/artifact/flattening"
	"github.com/stretchr/testify/require"
)

func ExampleCanonicalCoordinates() {
	collections := []flattening.Coordinate{
		{Dimension: "DockerComposeProjects", Key: "e2e"},
		{Dimension: "DockerComposeServices", Key: "web"},
	}
	for _, coordinate := range flattening.CanonicalCoordinates("docker", []string{"compose", "projects", "services", "healthCheck", "run"}, collections) {
		fmt.Printf("%s = %s\n", coordinate.Dimension, coordinate.Key)
	}
	// Output:
	// module = docker
	// DockerComposeProjects = e2e
	// DockerComposeServices = web
	// name = compose.projects.services.healthCheck.run
}

func TestCanonicalFullFieldPaths(t *testing.T) {
	project := flattening.Coordinate{Dimension: "DockerComposeProjects", Key: "e2e"}
	service := flattening.Coordinate{Dimension: "DockerComposeServices", Key: "web"}
	for _, tc := range []struct {
		name        string
		module      string
		fieldPath   []string
		collections []flattening.Coordinate
		want        []flattening.Coordinate
	}{
		{
			name: "module root", module: "docker",
			want: []flattening.Coordinate{{Dimension: "module", Key: "docker"}, {Dimension: "name", Key: "."}},
		},
		{
			name: "full path from exposed module root", module: "docker", fieldPath: []string{"compose", "allServices", "up"},
			want: []flattening.Coordinate{{Dimension: "module", Key: "docker"}, {Dimension: "name", Key: "compose.allServices.up"}},
		},
		{
			name: "nested collection item", module: "docker", fieldPath: []string{"compose", "projects", "services"}, collections: []flattening.Coordinate{project, service},
			want: []flattening.Coordinate{{Dimension: "module", Key: "docker"}, project, service, {Dimension: "name", Key: "compose.projects.services"}},
		},
		{
			name: "full path through nearest item", module: "docker", collections: []flattening.Coordinate{project, service}, fieldPath: []string{"compose", "projects", "services", "healthCheck", "run"},
			want: []flattening.Coordinate{{Dimension: "module", Key: "docker"}, project, service, {Dimension: "name", Key: "compose.projects.services.healthCheck.run"}},
		},
		{
			name: "function on collection retains all ordinary fields", module: "docker", collections: []flattening.Coordinate{project}, fieldPath: []string{"compose", "projects", "services", "all", "up"},
			want: []flattening.Coordinate{{Dimension: "module", Key: "docker"}, project, {Dimension: "name", Key: "compose.projects.services.all.up"}},
		},
		{
			name: "module root is collection", module: "matrix", collections: []flattening.Coordinate{{Dimension: "Rows", Key: "one"}}, fieldPath: []string{"check"},
			want: []flattening.Coordinate{{Dimension: "module", Key: "matrix"}, {Dimension: "Rows", Key: "one"}, {Dimension: "name", Key: "check"}},
		},
		{
			name: "no workspace module", fieldPath: []string{"bound", "container"},
			want: []flattening.Coordinate{{Dimension: "name", Key: "bound.container"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, flattening.CanonicalCoordinates(tc.module, tc.fieldPath, tc.collections))
		})
	}
}

func TestCanonicalReplacesStaticCoordinates(t *testing.T) {
	input := []flattening.Coordinate{
		{Dimension: "module", Key: "old"},
		{Dimension: "TestsCases", Key: "TestConnect"},
		{Dimension: "type:Case", Key: "tests/cases"},
		{Dimension: "type:Check", Key: "tests/cases/check"},
		{Dimension: "name", Key: "old"},
	}
	got := flattening.CanonicalCoordinates("tests", []string{"cases", "check"}, input)
	require.Equal(t, []flattening.Coordinate{
		{Dimension: "module", Key: "tests"},
		{Dimension: "TestsCases", Key: "TestConnect"},
		{Dimension: "name", Key: "cases.check"},
	}, got)
	got[1].Key = "changed"
	require.Equal(t, "TestConnect", input[1].Key)
}

func TestCanonicalDimensions(t *testing.T) {
	collection := &flattening.Dimension{
		Kind: "COLLECTION", Identifier: "/tests/cases", Name: "tests-case", QualifiedName: "tests-cases-case",
		CollectionType: "TestsCases", ItemType: "TestsCase", KeyName: "testName", KeyDescription: "Test to run",
	}
	legacy := flattening.ArtifactDimensions("tests", "Check", flattening.Dimensions{collection, {Kind: "COLLECTION", Identifier: "/other/route", CollectionType: "TestsCases", ItemType: "TestsCase"}})
	got := flattening.CanonicalDimensions("tests", legacy)
	require.Equal(t, flattening.Dimensions{
		{Kind: "MODULE", Identifier: "module", Name: "module", QualifiedName: "module", KeyName: "name"},
		{Kind: "COLLECTION", Identifier: "TestsCases", CollectionType: "TestsCases", ItemType: "TestsCase", Name: "tests-case", QualifiedName: "TestsCases", KeyName: "testName", KeyDescription: "Test to run"},
		{Kind: "NAME", Identifier: "name", Name: "name", QualifiedName: "name", KeyName: "name"},
	}, got)
	require.Len(t, flattening.CanonicalDimensions("", nil), 1)
	got[1].Name = "changed"
	require.Equal(t, "tests-case", collection.Name)
	resolved, err := got.Resolve("name")
	require.NoError(t, err)
	require.Equal(t, flattening.NameDimension, resolved)
}
