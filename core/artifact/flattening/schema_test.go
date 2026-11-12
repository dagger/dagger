package flattening_test

import (
	"slices"
	"testing"

	"github.com/dagger/dagger/core/artifact/flattening"
	"github.com/stretchr/testify/require"
)

func TestSchemaCoordinates(t *testing.T) {
	project := flattening.Collection{Fields: []string{"compose", "projects"}, ItemType: "DockerProject"}
	service := flattening.Collection{Fields: []string{"services"}, ItemType: "DockerService"}
	moduleRoot := flattening.Field{Module: "docker", Type: "Docker"}
	staticCheck := flattening.Field{Module: "docker", Type: "Check", LocalPath: []string{"unit", "healthCheck"}}
	serviceItem := flattening.Field{Module: "docker", Type: "DockerService", Collections: []flattening.Collection{project, service}}
	serviceCheck := flattening.Field{Module: "docker", Type: "Check", LocalPath: []string{"healthCheck", "run"}, Collections: []flattening.Collection{project, service}}
	collectionFunction := flattening.Field{Module: "docker", Type: "Service", LocalPath: []string{"services", "all", "up"}, Collections: []flattening.Collection{project}}
	// This collection has no selected rows. Its metadata still adds a column.
	emptyItem := flattening.Field{Module: "docker", Type: "Task", Collections: []flattening.Collection{{Fields: []string{"empty"}, ItemType: "Task"}}}
	fields := []flattening.Field{moduleRoot, staticCheck, serviceItem, serviceCheck, collectionFunction, emptyItem}

	schema, err := flattening.NewSchema(fields)
	require.NoError(t, err)
	require.Equal(t, flattening.Dimensions{
		{Kind: "COLLECTION", Identifier: "/docker/compose/projects", Name: "docker-project", QualifiedName: "docker-compose-projects-project", ItemType: "DockerProject"},
		{Kind: "COLLECTION", Identifier: "/docker/compose/projects/services", Name: "docker-service", QualifiedName: "docker-compose-projects-services-service", ItemType: "DockerService"},
		{Kind: "COLLECTION", Identifier: "/docker/empty", Name: "task", QualifiedName: "docker-empty-task", ItemType: "Task"},
		{Kind: "MODULE", Identifier: "module", Name: "module", QualifiedName: "module", KeyName: "name"},
		{Kind: "NAME", Identifier: "name", Name: "name", QualifiedName: "name", KeyName: "name"},
	}, schema.Dimensions())
	for _, tc := range []struct {
		name  string
		field flattening.Field
		keys  []string
		want  []flattening.Coordinate
	}{
		{"module root", moduleRoot, nil, []flattening.Coordinate{{Dimension: "module", Key: "docker"}, {Dimension: "name", Key: "."}}},
		{"static field", staticCheck, nil, []flattening.Coordinate{{Dimension: "module", Key: "docker"}, {Dimension: "name", Key: "unit.healthCheck"}}},
		{"nested item with empty key", serviceItem, []string{"e2e", ""}, []flattening.Coordinate{{Dimension: "module", Key: "docker"}, {Dimension: "/docker/compose/projects", Key: "e2e"}, {Dimension: "/docker/compose/projects/services", Key: ""}, {Dimension: "name", Key: "."}}},
		{"field on nested item", serviceCheck, []string{"e2e", "web"}, []flattening.Coordinate{{Dimension: "module", Key: "docker"}, {Dimension: "/docker/compose/projects", Key: "e2e"}, {Dimension: "/docker/compose/projects/services", Key: "web"}, {Dimension: "name", Key: "healthCheck.run"}}},
		{"function on collection", collectionFunction, []string{"e2e"}, []flattening.Coordinate{{Dimension: "module", Key: "docker"}, {Dimension: "/docker/compose/projects", Key: "e2e"}, {Dimension: "name", Key: "services.all.up"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := schema.Coordinates(tc.field, tc.keys...)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
	reversed := slices.Clone(fields)
	slices.Reverse(reversed)
	other, err := flattening.NewSchema(reversed)
	require.NoError(t, err)
	require.Equal(t, schema.Dimensions(), other.Dimensions())
}

func TestSchemaRoots(t *testing.T) {
	fields := []flattening.Field{
		{Type: "Container", LocalPath: []string{}},
		{Module: "matrix", Type: "Check", LocalPath: []string{"check"}, Collections: []flattening.Collection{{ItemType: "Row"}}},
	}
	schema, err := flattening.NewSchema(fields)
	require.NoError(t, err)
	got, err := schema.Coordinates(flattening.Field{Type: "Container"})
	require.NoError(t, err)
	require.Equal(t, []flattening.Coordinate{{Dimension: "name", Key: "."}}, got)
	got, err = schema.Coordinates(fields[1], "one")
	require.NoError(t, err)
	require.Equal(t, []flattening.Coordinate{{Dimension: "module", Key: "matrix"}, {Dimension: "/matrix", Key: "one"}, {Dimension: "name", Key: "check"}}, got)
}

func TestSchemaErrors(t *testing.T) {
	collection := flattening.Collection{Fields: []string{"testCases"}, ItemType: "Case"}
	field := flattening.Field{Module: "app", Type: "Check", LocalPath: []string{"check"}, Collections: []flattening.Collection{collection}}
	for _, tc := range []struct {
		name   string
		fields []flattening.Field
		want   string
	}{
		{"missing type", []flattening.Field{{}}, "no artifact type"},
		{"missing namespace", []flattening.Field{{Type: "Check", Collections: []flattening.Collection{collection}}}, "module namespace"},
		{"missing item type", []flattening.Field{{Module: "app", Type: "Check", Collections: []flattening.Collection{{}}}}, "no item type"},
		{"repeated dimension", []flattening.Field{{Module: "app", Type: "Check", Collections: []flattening.Collection{collection, {ItemType: "Child"}}}}, "repeated dimension"},
		{"empty local field", []flattening.Field{{Type: "Check", LocalPath: []string{""}}}, "invalid field segment"},
		{"dotted local field", []flattening.Field{{Type: "Check", LocalPath: []string{"unit.check"}}}, "invalid field segment"},
		{"dotted collection field", []flattening.Field{{Module: "app", Type: "Check", Collections: []flattening.Collection{{Fields: []string{"unit.tests"}, ItemType: "Case"}}}}, "invalid field segment"},
		{"conflicting metadata", []flattening.Field{field, {Module: "app", Type: "Check", Collections: []flattening.Collection{{Fields: []string{"testCases"}, ItemType: "Other"}}}}, "conflicting metadata"},
		{"collapsed literal route", []flattening.Field{field, {Module: "app", Type: "Check", Collections: []flattening.Collection{{Fields: []string{"test-cases"}, ItemType: "Case"}}}}, "distinct collection routes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := flattening.NewSchema(tc.fields)
			require.ErrorContains(t, err, tc.want)
		})
	}
	schema, err := flattening.NewSchema([]flattening.Field{field})
	require.NoError(t, err)
	_, err = schema.Coordinates(field)
	require.ErrorContains(t, err, "needs 1 collection keys, got 0")
	_, err = schema.Coordinates(field, "one", "two")
	require.ErrorContains(t, err, "needs 1 collection keys, got 2")
	field.Type = "Service"
	_, err = schema.Coordinates(field, "one")
	require.ErrorContains(t, err, "absent from the flattening schema")
}

func TestSchemaOwnership(t *testing.T) {
	field := flattening.Field{Module: "app", Type: "Check", LocalPath: []string{"check"}, Collections: []flattening.Collection{{Fields: []string{"tests"}, ItemType: "Test", KeyName: "testName"}}}
	schema, err := flattening.NewSchema([]flattening.Field{field})
	require.NoError(t, err)
	want := schema.Dimensions()
	field.LocalPath[0] = "changed"
	field.Collections[0].Fields[0] = "changed"
	field.Collections[0].KeyName = "changed"
	copy := schema.Dimensions()
	copy[0].Name = "changed"
	copy[0] = nil
	require.Equal(t, want, schema.Dimensions())
	original := flattening.Field{Module: "app", Type: "Check", LocalPath: []string{"check"}, Collections: []flattening.Collection{{Fields: []string{"tests"}, ItemType: "Test", KeyName: "different", KeyDescription: "different"}}}
	got, err := schema.Coordinates(original, "one")
	require.NoError(t, err) // Lookup metadata does not form part of field identity.
	require.Equal(t, []flattening.Coordinate{{Dimension: "module", Key: "app"}, {Dimension: "/app/tests", Key: "one"}, {Dimension: "name", Key: "check"}}, got)
	got[1].Key = "changed"
	again, err := schema.Coordinates(original, "one")
	require.NoError(t, err)
	require.Equal(t, "one", again[1].Key)
}

func TestSchemaRejectsUnknownLiteralRoute(t *testing.T) {
	field := flattening.Field{Module: "app", Type: "Check", Collections: []flattening.Collection{{Fields: []string{"testCases"}, ItemType: "Case"}}}
	schema, err := flattening.NewSchema([]flattening.Field{field})
	require.NoError(t, err)
	field.Collections[0].Fields = []string{"test-cases"}
	_, err = schema.Coordinates(field, "one")
	require.EqualError(t, err, "field . is absent from the flattening schema")
}

func TestSchemaLookupNeedsNoCollectionMetadata(t *testing.T) {
	field := flattening.Field{Module: "matrix", Type: "Check", Collections: []flattening.Collection{{Fields: []string{}, ItemType: "Row"}}}
	schema, err := flattening.NewSchema([]flattening.Field{field})
	require.NoError(t, err)
	lookup := flattening.Field{Module: "matrix", Type: "Check", LocalPath: []string{}, Collections: []flattening.Collection{{}}}
	got, err := schema.Coordinates(lookup, "one")
	require.NoError(t, err)
	require.Equal(t, []flattening.Coordinate{{Dimension: "module", Key: "matrix"}, {Dimension: "/matrix", Key: "one"}, {Dimension: "name", Key: "."}}, got)
}
