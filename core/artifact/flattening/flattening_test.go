package flattening_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/dagger/dagger/core/artifact/flattening"
	"github.com/stretchr/testify/require"
)

// Both paths return a Service. Only the first passes through a service item.
// The second is a function on the services collection itself.
func composeSchema() []flattening.Artifact {
	projects := flattening.Collection{Fields: []string{"compose", "projects"}, Type: "DockerComposeProjects", ItemType: "DockerComposeProject", KeyName: "name"}
	services := flattening.Collection{Fields: []string{"services"}, Type: "DockerComposeServices", ItemType: "DockerComposeService", KeyName: "name"}
	return []flattening.Artifact{
		{Module: "docker", Path: []string{"docker", "compose", "projects", "services", "up"}, Type: "Service", Collections: []flattening.Collection{projects, services}},
		{Module: "docker", Path: []string{"docker", "compose", "projects", "services", "all", "up"}, Type: "Service", Collections: []flattening.Collection{projects}},
	}
}

func ExamplePlan() {
	schema := composeSchema()
	p := flattening.New(schema)
	names := p.Names()
	fmt.Println("DIMENSION => NAME")
	for _, d := range p.Dimensions() {
		fmt.Printf("%s => %s\n", d.Identifier, names[d.Identifier])
	}
	for i, keys := range [][]string{{"e2e", "web"}, {"e2e"}} {
		row, err := p.Flatten(schema[i], keys...)
		if err != nil {
			panic(err)
		}
		fmt.Printf("\n%s\n", schema[i].Path)
		for _, c := range row.Coordinates {
			fmt.Printf("%s = %s\n", c.Dimension, c.Key)
		}
		fmt.Printf("short type key = %s\n", row.TypeKey)
	}
	// Output:
	// DIMENSION => NAME
	// /docker/compose/projects => docker-compose-project
	// /docker/compose/projects/services => docker-compose-service
	// module => module
	// type:Service => service
	//
	// [docker compose projects services up]
	// module = docker
	// /docker/compose/projects = e2e
	// /docker/compose/projects/services = web
	// type:Service = docker/compose/projects/services/up
	// short type key = up
	//
	// [docker compose projects services all up]
	// module = docker
	// /docker/compose/projects = e2e
	// type:Service = docker/compose/projects/services/all/up
	// short type key = all/up
}

func TestObjectFieldsAndCollectionItems(t *testing.T) {
	schema := composeSchema()
	p := flattening.New(schema)
	item, err := p.Flatten(schema[0], "e2e", "web")
	require.NoError(t, err)
	require.Equal(t, []flattening.Coordinate{
		{Dimension: "module", Key: "docker"},
		{Dimension: "/docker/compose/projects", Key: "e2e"},
		{Dimension: "/docker/compose/projects/services", Key: "web"},
		{Dimension: "type:Service", Key: "docker/compose/projects/services/up"},
	}, item.Coordinates)
	helper, err := p.Flatten(schema[1], "e2e")
	require.NoError(t, err)
	require.Equal(t, []flattening.Coordinate{
		{Dimension: "module", Key: "docker"},
		{Dimension: "/docker/compose/projects", Key: "e2e"},
		{Dimension: "type:Service", Key: "docker/compose/projects/services/all/up"},
	}, helper.Coordinates)
	require.Equal(t, "up", item.TypeKey)
	require.Equal(t, "all/up", helper.TypeKey)
}

func TestSchemaScopeSurvivesRowSelection(t *testing.T) {
	schema := []flattening.Artifact{
		{Module: "app", Path: []string{"app", "linux", "tests", "check"}, Type: "Check", Collections: []flattening.Collection{{Fields: []string{"linux", "tests"}, ItemType: "AppTest"}}},
		{Module: "app", Path: []string{"app", "windows", "tests", "check"}, Type: "Check", Collections: []flattening.Collection{{Fields: []string{"windows", "tests"}, ItemType: "AppTest"}}},
	}
	p := flattening.New(schema)
	require.Equal(t, map[string]string{
		"/app/linux/tests":   "app-linux-test",
		"/app/windows/tests": "app-windows-test",
		"module":             "module", "type:Check": "check",
	}, p.Names())
	selected, err := p.Flatten(schema[0], "TestConnect")
	require.NoError(t, err)
	require.Equal(t, []flattening.Coordinate{
		{Dimension: "module", Key: "app"},
		{Dimension: "/app/linux/tests", Key: "TestConnect"},
		{Dimension: "type:Check", Key: "app/linux/tests/check"},
	}, selected.Coordinates)
	require.Equal(t, "app-linux-test", p.Names()["/app/linux/tests"])
	// A smaller schema would change the name. Selection must retain the full
	// schema, including paths for which no runtime item was supplied.
	require.Equal(t, "app-test", flattening.New(schema[:1]).Names()["/app/linux/tests"])
	reverse := slices.Clone(schema)
	slices.Reverse(reverse)
	reordered := flattening.New(reverse)
	require.Equal(t, p.Dimensions(), reordered.Dimensions())
	require.Equal(t, p.Names(), reordered.Names())
	reorderedRow, err := reordered.Flatten(schema[0], "TestConnect")
	require.NoError(t, err)
	require.Equal(t, selected, reorderedRow)
}

func TestStaticAndBoundArtifacts(t *testing.T) {
	schema := []flattening.Artifact{
		{Module: "app", Path: []string{"test"}, Type: "Check"}, // Main module shorthand.
		{Module: "tools", Path: []string{"tools", "test"}, Type: "Check"},
		{Path: []string{"bound", "container"}, Type: "Container"}, // No workspace module.
	}
	p := flattening.New(schema)
	for i, want := range [][]flattening.Coordinate{
		{{Dimension: "module", Key: "app"}, {Dimension: "type:Check", Key: "test"}},
		{{Dimension: "module", Key: "tools"}, {Dimension: "type:Check", Key: "tools/test"}},
		{{Dimension: "type:Container", Key: "bound/container"}},
	} {
		row, err := p.Flatten(schema[i])
		require.NoError(t, err)
		require.Equal(t, want, row.Coordinates)
		require.Equal(t, want[len(want)-1].Key, row.TypeKey, "static paths are not shortened")
	}
}

func TestModuleRootCollection(t *testing.T) {
	schema := []flattening.Artifact{{Module: "module", Path: []string{"check"}, Type: "Check", Collections: []flattening.Collection{{Type: "Module", ItemType: "ModuleItem"}}}}
	p := flattening.New(schema)
	row, err := p.Flatten(schema[0], "one")
	require.NoError(t, err)
	require.Equal(t, []flattening.Coordinate{
		{Dimension: "module", Key: "module"},
		{Dimension: "/module", Key: "one"},
		{Dimension: "type:Check", Key: "check"},
	}, row.Coordinates)
	require.Equal(t, "module", p.Names()["module"])
	require.Equal(t, "module-item", p.Names()["/module"])
}

func TestCoordinatesCoverIdentity(t *testing.T) {
	schema := composeSchema()
	// Same object type and same collection keys, reached through another field.
	other := schema[0]
	other.Path = []string{"docker", "compose", "projects", "services", "backup", "up"}
	schema = append(schema, other)
	p := flattening.New(schema)
	var rows []flattening.Row
	for i, keys := range [][]string{{"e2e", "web"}, {"e2e"}, {"e2e", "web"}} {
		row, err := p.Flatten(schema[i], keys...)
		require.NoError(t, err)
		for _, previous := range rows {
			require.NotEqual(t, previous.Coordinates, row.Coordinates)
		}
		rows = append(rows, row)
	}
	require.Equal(t, "services/up", rows[0].TypeKey)
	require.Equal(t, "backup/up", rows[2].TypeKey)
	secondItem, err := p.Flatten(schema[0], "e2e", "worker")
	require.NoError(t, err)
	require.NotEqual(t, rows[0].Coordinates, secondItem.Coordinates)
}

func TestPlanInputErrors(t *testing.T) {
	schema := composeSchema()
	p := flattening.New(schema)
	_, err := p.Flatten(schema[0], "e2e")
	require.EqualError(t, err, "artifact docker/compose/projects/services/up needs 2 collection keys, got 1")
	unknown := schema[0]
	unknown.Path = []string{"unknown"}
	_, err = p.Flatten(unknown, "e2e", "web")
	require.EqualError(t, err, "artifact unknown is absent from the flattening schema")
	_, err = flattening.New(nil).Flatten(flattening.Artifact{Module: "foreign", Path: []string{"unknown"}})
	require.EqualError(t, err, "artifact unknown has no type")
}

func TestPlanOwnsMetadata(t *testing.T) {
	schema := composeSchema()
	p := flattening.New(schema)
	dims := p.Dimensions()
	dims[0].Name = "changed"
	schema[0].Path[0] = "changed"
	schema[0].Collections[0].Fields[0] = "changed"
	require.Equal(t, "docker-compose-project", p.Names()["/docker/compose/projects"])
}

// Capture a current limitation: a collection item can itself be a collection.
// There is no intervening object field, so both boundaries get the same ID.
// A dimension-key selector cannot distinguish these two coordinate positions.
func TestCollectionItemIsAnotherCollection(t *testing.T) {
	schema := []flattening.Artifact{{
		Module: "matrix", Path: []string{"matrix", "rows"}, Type: "Cell",
		Collections: []flattening.Collection{
			{Fields: []string{"rows"}, Type: "Rows", ItemType: "Columns"},
			{Type: "Columns", ItemType: "Cell"},
		},
	}}
	p := flattening.New(schema)
	row, err := p.Flatten(schema[0], "row-1", "column-1")
	require.NoError(t, err)
	require.Equal(t, []flattening.Coordinate{
		{Dimension: "module", Key: "matrix"},
		{Dimension: "/matrix/rows", Key: "row-1"},
		{Dimension: "/matrix/rows", Key: "column-1"},
		{Dimension: "type:Cell", Key: "matrix/rows"},
	}, row.Coordinates)
	// Union collapses the two collection columns to one definition.
	require.Len(t, p.Dimensions(), 3)
}

func TestCurrentCollectionSpelling(t *testing.T) {
	collection := flattening.Collection{
		Fields: []string{"k8SClusters"}, Type: "E2ETestsK8SClusters", ItemType: "E2ETestsK8SCluster",
	}
	dim := collection.Dimension("e2e-tests", nil, nil)
	require.Equal(t, "/e2e-tests/k8s-clusters", dim.Identifier)
	require.Equal(t, "e2e-tests-k8s-cluster", dim.Name)
	require.Equal(t, "e2e-tests-k8s-clusters-k8s-cluster", dim.QualifiedName)
	dims := flattening.ArtifactDimensions("e2e-tests", "E2ETest", nil)
	require.Equal(t, "e2e-test", dims[1].Name)
	require.Equal(t, "artifact-e2e-test", dims[1].QualifiedName)
}

func TestCrossModuleCollectionPrefix(t *testing.T) {
	parent := &flattening.Dimension{Identifier: "/outer/projects"}
	collection := flattening.Collection{Fields: []string{"tasks"}, ItemType: "InnerTask"}
	dim := collection.Dimension("inner", parent, nil)
	require.Equal(t, "/outer/projects/tasks", dim.Identifier)
	require.Equal(t, "inner-task", dim.Name)
	require.Equal(t, "outer-projects-tasks-task", dim.QualifiedName)
}

func TestCollectionNamingPolicy(t *testing.T) {
	collection := flattening.Collection{Fields: []string{"items"}, ItemType: "app-item"}
	dim := collection.Dimension("app", nil, strings.ToUpper)
	require.Equal(t, "/APP/ITEMS", dim.Identifier)
	require.Equal(t, "APP-ITEM", dim.Name)
	require.Equal(t, "APP-ITEMS-ITEM", dim.QualifiedName)
}
