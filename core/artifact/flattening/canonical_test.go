package flattening_test

import (
	"fmt"
	"testing"

	"github.com/dagger/dagger/core/artifact/flattening"
	"github.com/stretchr/testify/require"
)

func ExampleCanonicalCoordinates() {
	collections := []flattening.Coordinate{
		{Dimension: "/docker/compose/projects", Key: "e2e"},
		{Dimension: "/docker/compose/projects/services", Key: "web"},
	}
	for _, coordinate := range flattening.CanonicalCoordinates("docker", []string{"healthCheck", "run"}, collections) {
		fmt.Printf("%s = %s\n", coordinate.Dimension, coordinate.Key)
	}
	// Output:
	// module = docker
	// /docker/compose/projects = e2e
	// /docker/compose/projects/services = web
	// name = healthCheck.run
}

func TestCanonicalLocalRoots(t *testing.T) {
	project := flattening.Coordinate{Dimension: "/docker/compose/projects", Key: "e2e"}
	service := flattening.Coordinate{Dimension: "/docker/compose/projects/services", Key: "web"}
	for _, tc := range []struct {
		name        string
		module      string
		localPath   []string
		collections []flattening.Coordinate
		want        []flattening.Coordinate
	}{
		{
			name: "module root", module: "docker",
			want: []flattening.Coordinate{{Dimension: "module", Key: "docker"}, {Dimension: "name", Key: "."}},
		},
		{
			name: "full path from exposed module root", module: "docker", localPath: []string{"compose", "allServices", "up"},
			want: []flattening.Coordinate{{Dimension: "module", Key: "docker"}, {Dimension: "name", Key: "compose.allServices.up"}},
		},
		{
			name: "nested collection item", module: "docker", collections: []flattening.Coordinate{project, service},
			want: []flattening.Coordinate{{Dimension: "module", Key: "docker"}, project, service, {Dimension: "name", Key: "."}},
		},
		{
			name: "full path from nearest item", module: "docker", collections: []flattening.Coordinate{project, service}, localPath: []string{"healthCheck", "run"},
			want: []flattening.Coordinate{{Dimension: "module", Key: "docker"}, project, service, {Dimension: "name", Key: "healthCheck.run"}},
		},
		{
			name: "function on collection uses enclosing item", module: "docker", collections: []flattening.Coordinate{project}, localPath: []string{"services", "all", "up"},
			want: []flattening.Coordinate{{Dimension: "module", Key: "docker"}, project, {Dimension: "name", Key: "services.all.up"}},
		},
		{
			name: "module root is collection", module: "matrix", collections: []flattening.Coordinate{{Dimension: "/matrix", Key: "one"}}, localPath: []string{"check"},
			want: []flattening.Coordinate{{Dimension: "module", Key: "matrix"}, {Dimension: "/matrix", Key: "one"}, {Dimension: "name", Key: "check"}},
		},
		{
			name: "no workspace module", localPath: []string{"bound", "container"},
			want: []flattening.Coordinate{{Dimension: "name", Key: "bound.container"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, flattening.CanonicalCoordinates(tc.module, tc.localPath, tc.collections))
		})
	}
}

func TestCanonicalSameTypeFieldsAndSelection(t *testing.T) {
	// Each path returns a Check. The field paths distinguish them without a
	// type dimension or a schema-dependent shortest suffix.
	paths := [][]string{{"unit", "check"}, {"slow", "check"}, {"check"}}
	var all [][]flattening.Coordinate
	for _, path := range paths {
		all = append(all, flattening.CanonicalCoordinates("tests", path, nil))
	}
	require.Equal(t, []flattening.Coordinate{{Dimension: "module", Key: "tests"}, {Dimension: "name", Key: "unit.check"}}, all[0])
	require.Equal(t, "slow.check", all[1][1].Key)
	require.Equal(t, "check", all[2][1].Key)
	// Selecting one artifact or changing traversal order cannot shorten its name.
	require.Equal(t, all[1], flattening.CanonicalCoordinates("tests", paths[1], nil))
	for i := len(paths) - 1; i >= 0; i-- {
		require.Equal(t, all[i], flattening.CanonicalCoordinates("tests", paths[i], nil))
	}
}

func TestCanonicalReplacesLegacyCoordinates(t *testing.T) {
	legacy := []flattening.Coordinate{
		{Dimension: "module", Key: "old"},
		{Dimension: "/tests/cases", Key: "TestConnect"},
		{Dimension: "type:Case", Key: "tests/cases"},
		{Dimension: "type:Check", Key: "tests/cases/check"},
		{Dimension: "name", Key: "old"},
	}
	got := flattening.CanonicalCoordinates("tests", []string{"check"}, legacy)
	require.Equal(t, []flattening.Coordinate{
		{Dimension: "module", Key: "tests"},
		{Dimension: "/tests/cases", Key: "TestConnect"},
		{Dimension: "name", Key: "check"},
	}, got)
	got[1].Key = "changed"
	require.Equal(t, "TestConnect", legacy[1].Key)
}

func TestCanonicalDimensions(t *testing.T) {
	collection := &flattening.Dimension{
		Kind: "COLLECTION", Identifier: "/tests/cases", Name: "tests-case", QualifiedName: "tests-cases-case",
		CollectionType: "TestsCases", ItemType: "TestsCase", KeyName: "testName", KeyDescription: "Test to run",
	}
	legacy := flattening.ArtifactDimensions("tests", "Check", flattening.Dimensions{collection})
	got := flattening.CanonicalDimensions("tests", legacy)
	require.Equal(t, flattening.Dimensions{
		{Kind: "MODULE", Identifier: "module", Name: "module", QualifiedName: "module", KeyName: "name"},
		collection,
		{Kind: "NAME", Identifier: "name", Name: "name", QualifiedName: "name", KeyName: "name"},
	}, got)
	require.Len(t, flattening.CanonicalDimensions("", nil), 1)
	got[1].Name = "changed"
	require.Equal(t, "tests-case", collection.Name)
	resolved, err := got.Resolve("name")
	require.NoError(t, err)
	require.Equal(t, flattening.NameDimension, resolved)
}

func TestCanonicalCollectionIdentifiersAreOpaque(t *testing.T) {
	require.Equal(t, []flattening.Coordinate{
		{Dimension: "module", Key: "tests"}, {Dimension: "case", Key: "TestConnect"}, {Dimension: "name", Key: "check"},
	}, flattening.CanonicalCoordinates("tests", []string{"check"}, []flattening.Coordinate{{Dimension: "case", Key: "TestConnect"}}))
}
