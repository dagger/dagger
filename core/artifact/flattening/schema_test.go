package flattening_test

import (
	"slices"
	"testing"

	"github.com/dagger/dagger/core/artifact/flattening"
	"github.com/stretchr/testify/require"
)

func TestSchemaCoordinates(t *testing.T) {
	project := flattening.Collection{Fields: []string{"compose", "projects"}, Type: "DockerProjects", ItemType: "DockerProject"}
	service := flattening.Collection{Fields: []string{"services"}, Type: "DockerServices", ItemType: "DockerService"}
	moduleRoot := flattening.Field{Module: "docker", Type: "Docker"}
	staticCheck := flattening.Field{Module: "docker", Type: "Check", LocalPath: []string{"unit", "healthCheck"}}
	serviceItem := flattening.Field{Module: "docker", Type: "DockerService", Collections: []flattening.Collection{project, service}}
	serviceCheck := flattening.Field{Module: "docker", Type: "Check", LocalPath: []string{"healthCheck", "run"}, Collections: []flattening.Collection{project, service}}
	collectionFunction := flattening.Field{Module: "docker", Type: "Service", LocalPath: []string{"services", "all", "up"}, Collections: []flattening.Collection{project}}
	// This collection has no selected rows. Its metadata still adds a column.
	emptyItem := flattening.Field{Module: "docker", Type: "Task", Collections: []flattening.Collection{{Fields: []string{"empty"}, Type: "Tasks", ItemType: "Task"}}}
	fields := []flattening.Field{moduleRoot, staticCheck, serviceItem, serviceCheck, collectionFunction, emptyItem}

	schema, err := flattening.NewSchema(fields)
	require.NoError(t, err)
	require.Equal(t, flattening.Dimensions{
		{Kind: "COLLECTION", Identifier: "DockerProjects", CollectionType: "DockerProjects", Name: "docker-project", QualifiedName: "docker-projects", ItemType: "DockerProject"},
		{Kind: "COLLECTION", Identifier: "DockerServices", CollectionType: "DockerServices", Name: "docker-service", QualifiedName: "docker-services", ItemType: "DockerService"},
		{Kind: "COLLECTION", Identifier: "Tasks", CollectionType: "Tasks", Name: "task", QualifiedName: "tasks", ItemType: "Task"},
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
		{"nested item with empty key", serviceItem, []string{"e2e", ""}, []flattening.Coordinate{{Dimension: "module", Key: "docker"}, {Dimension: "DockerProjects", Key: "e2e"}, {Dimension: "DockerServices", Key: ""}, {Dimension: "name", Key: "."}}},
		{"field on nested item", serviceCheck, []string{"e2e", "web"}, []flattening.Coordinate{{Dimension: "module", Key: "docker"}, {Dimension: "DockerProjects", Key: "e2e"}, {Dimension: "DockerServices", Key: "web"}, {Dimension: "name", Key: "healthCheck.run"}}},
		{"function on collection", collectionFunction, []string{"e2e"}, []flattening.Coordinate{{Dimension: "module", Key: "docker"}, {Dimension: "DockerProjects", Key: "e2e"}, {Dimension: "name", Key: "services.all.up"}}},
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
		{Module: "matrix", Type: "Check", LocalPath: []string{"check"}, Collections: []flattening.Collection{{Type: "Rows", ItemType: "Row"}}},
	}
	schema, err := flattening.NewSchema(fields)
	require.NoError(t, err)
	got, err := schema.Coordinates(flattening.Field{Type: "Container"})
	require.NoError(t, err)
	require.Equal(t, []flattening.Coordinate{{Dimension: "name", Key: "."}}, got)
	got, err = schema.Coordinates(fields[1], "one")
	require.NoError(t, err)
	require.Equal(t, []flattening.Coordinate{{Dimension: "module", Key: "matrix"}, {Dimension: "Rows", Key: "one"}, {Dimension: "name", Key: "check"}}, got)
}

func TestSchemaErrors(t *testing.T) {
	collection := flattening.Collection{Fields: []string{"testCases"}, Type: "Cases", ItemType: "Case"}
	field := flattening.Field{Module: "app", Type: "Check", LocalPath: []string{"check"}, Collections: []flattening.Collection{collection}}
	for _, tc := range []struct {
		name   string
		fields []flattening.Field
		want   string
	}{
		{"missing type", []flattening.Field{{}}, "no artifact type"},
		{"missing collection type", []flattening.Field{{Type: "Check", Collections: []flattening.Collection{{ItemType: "Case"}}}}, "no schema type"},
		{"missing item type", []flattening.Field{{Module: "app", Type: "Check", Collections: []flattening.Collection{{Type: "Cases"}}}}, "no item type"},
		{"repeated dimension", []flattening.Field{{Module: "app", Type: "Check", Collections: []flattening.Collection{collection, {Type: "Cases", ItemType: "Case"}}}}, "repeated collection type"},
		{"empty local field", []flattening.Field{{Type: "Check", LocalPath: []string{""}}}, "invalid field segment"},
		{"dotted local field", []flattening.Field{{Type: "Check", LocalPath: []string{"unit.check"}}}, "invalid field segment"},
		{"dotted collection field", []flattening.Field{{Module: "app", Type: "Check", Collections: []flattening.Collection{{Fields: []string{"unit.tests"}, Type: "Cases", ItemType: "Case"}}}}, "invalid field segment"},
		{"conflicting metadata", []flattening.Field{field, {Module: "app", Type: "Check", Collections: []flattening.Collection{{Fields: []string{"testCases"}, Type: "Cases", ItemType: "Other"}}}}, "conflicting metadata"},
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
	field := flattening.Field{Module: "app", Type: "Check", LocalPath: []string{"check"}, Collections: []flattening.Collection{{Fields: []string{"tests"}, Type: "Tests", ItemType: "Test", KeyName: "testName"}}}
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
	original := flattening.Field{Module: "app", Type: "Check", LocalPath: []string{"check"}, Collections: []flattening.Collection{{Fields: []string{"tests"}, Type: "Tests", ItemType: "Test", KeyName: "different", KeyDescription: "different"}}}
	got, err := schema.Coordinates(original, "one")
	require.NoError(t, err) // Lookup metadata does not form part of field identity.
	require.Equal(t, []flattening.Coordinate{{Dimension: "module", Key: "app"}, {Dimension: "Tests", Key: "one"}, {Dimension: "name", Key: "check"}}, got)
	got[1].Key = "changed"
	again, err := schema.Coordinates(original, "one")
	require.NoError(t, err)
	require.Equal(t, "one", again[1].Key)
}

func TestSchemaRejectsUnknownLiteralRoute(t *testing.T) {
	field := flattening.Field{Module: "app", Type: "Check", Collections: []flattening.Collection{{Fields: []string{"testCases"}, Type: "Cases", ItemType: "Case"}}}
	schema, err := flattening.NewSchema([]flattening.Field{field})
	require.NoError(t, err)
	field.Collections[0].Fields = []string{"test-cases"}
	_, err = schema.Coordinates(field, "one")
	require.EqualError(t, err, "field . is absent from the flattening schema")
}

func TestSchemaLookupNeedsNoLookupMetadata(t *testing.T) {
	field := flattening.Field{Module: "matrix", Type: "Check", Collections: []flattening.Collection{{Fields: []string{}, Type: "Rows", ItemType: "Row"}}}
	schema, err := flattening.NewSchema([]flattening.Field{field})
	require.NoError(t, err)
	lookup := flattening.Field{Module: "matrix", Type: "Check", LocalPath: []string{}, Collections: []flattening.Collection{{Type: "Rows"}}}
	got, err := schema.Coordinates(lookup, "one")
	require.NoError(t, err)
	require.Equal(t, []flattening.Coordinate{{Dimension: "module", Key: "matrix"}, {Dimension: "Rows", Key: "one"}, {Dimension: "name", Key: "."}}, got)
}

func TestSchemaCollectionTypesDefineColumns(t *testing.T) {
	row := flattening.Collection{Type: "Rows", ItemType: "Row", Fields: []string{"rows"}}
	cell := flattening.Collection{Type: "Cells", ItemType: "Cell"}
	field := flattening.Field{Type: "Check", LocalPath: []string{"check"}, Collections: []flattening.Collection{row, cell}}
	schema, err := flattening.NewSchema([]flattening.Field{field})
	require.NoError(t, err)
	require.Equal(t, flattening.Dimensions{
		{Kind: "COLLECTION", Identifier: "Cells", CollectionType: "Cells", ItemType: "Cell", Name: "cell", QualifiedName: "cells"},
		{Kind: "COLLECTION", Identifier: "Rows", CollectionType: "Rows", ItemType: "Row", Name: "row", QualifiedName: "rows"},
		{Kind: "NAME", Identifier: "name", Name: "name", QualifiedName: "name", KeyName: "name"},
	}, schema.Dimensions())
	got, err := schema.Coordinates(field, "r", "c")
	require.NoError(t, err)
	require.Equal(t, []flattening.Coordinate{{Dimension: "Rows", Key: "r"}, {Dimension: "Cells", Key: "c"}, {Dimension: "name", Key: "check"}}, got)
}

func TestSchemaSameTypeAtDifferentFields(t *testing.T) {
	left := flattening.Field{Type: "Check", Collections: []flattening.Collection{{Type: "Cases", ItemType: "Case", Fields: []string{"left"}}}}
	right := flattening.Field{Type: "Check", Collections: []flattening.Collection{{Type: "Cases", ItemType: "Case", Fields: []string{"right"}}}}
	schema, err := flattening.NewSchema([]flattening.Field{left, right})
	require.NoError(t, err)
	require.Equal(t, flattening.Dimensions{
		{Kind: "COLLECTION", Identifier: "Cases", CollectionType: "Cases", ItemType: "Case", Name: "case", QualifiedName: "cases"},
		{Kind: "NAME", Identifier: "name", Name: "name", QualifiedName: "name", KeyName: "name"},
	}, schema.Dimensions())
	for _, field := range []flattening.Field{left, right} {
		got, err := schema.Coordinates(field, "one")
		require.NoError(t, err)
		require.Equal(t, []flattening.Coordinate{{Dimension: "Cases", Key: "one"}, {Dimension: "name", Key: "."}}, got)
	}
	left.Collections[0].Type = "UnknownCases"
	_, err = schema.Coordinates(left, "one")
	require.ErrorContains(t, err, "absent from the flattening schema")
}

func TestSchemaSelectionDoesNotChangeColumns(t *testing.T) {
	selected := flattening.Field{Type: "Check", LocalPath: []string{"healthCheck"}}
	empty := flattening.Field{Type: "Case", Collections: []flattening.Collection{{Type: "Cases", ItemType: "Case", Fields: []string{"empty"}}}}
	schema, err := flattening.NewSchema([]flattening.Field{selected, empty})
	require.NoError(t, err)
	before := schema.Dimensions()
	got, err := schema.Coordinates(selected)
	require.NoError(t, err)
	require.Equal(t, []flattening.Coordinate{{Dimension: "name", Key: "healthCheck"}}, got)
	require.Equal(t, before, schema.Dimensions())
	require.Len(t, before, 2)
	emptySchema, err := flattening.NewSchema(nil)
	require.NoError(t, err)
	require.Empty(t, emptySchema.Dimensions())
}

func TestSchemaDistinctTypesKeepSeparateColumns(t *testing.T) {
	first := flattening.Field{Type: "Case", Collections: []flattening.Collection{{Type: "FirstCases", ItemType: "Case", Fields: []string{"first"}}}}
	second := flattening.Field{Type: "Case", Collections: []flattening.Collection{{Type: "SecondCases", ItemType: "Case", Fields: []string{"second"}}}}
	schema, err := flattening.NewSchema([]flattening.Field{first, second})
	require.NoError(t, err)
	require.Equal(t, flattening.Dimensions{
		{Kind: "COLLECTION", Identifier: "FirstCases", CollectionType: "FirstCases", ItemType: "Case", Name: "case", QualifiedName: "first-cases"},
		{Kind: "COLLECTION", Identifier: "SecondCases", CollectionType: "SecondCases", ItemType: "Case", Name: "case", QualifiedName: "second-cases"},
		{Kind: "NAME", Identifier: "name", Name: "name", QualifiedName: "name", KeyName: "name"},
	}, schema.Dimensions())
}

func TestSchemaCollectionTypeIdentifiers(t *testing.T) {
	for _, typ := range []string{"module", "name", "type:Rows", "/grid/rows", "Rows/Cells", "Rows.Cells", "Rows-Cells", "1Rows"} {
		t.Run(typ, func(t *testing.T) {
			_, err := flattening.NewSchema([]flattening.Field{{Type: "Row", Collections: []flattening.Collection{{Type: typ, ItemType: "Row"}}}})
			require.ErrorContains(t, err, "non-reserved schema type identifier")
		})
	}
	field := flattening.Field{Type: "Row", Collections: []flattening.Collection{{Type: "Rows", ItemType: "Row"}}}
	schema, err := flattening.NewSchema([]flattening.Field{field})
	require.NoError(t, err)
	coordinates, err := schema.Coordinates(field, "top")
	require.NoError(t, err)
	require.Equal(t, []flattening.Coordinate{{Dimension: "Rows", Key: "top"}, {Dimension: "name", Key: "."}}, coordinates)
	require.Len(t, coordinates, len(field.Collections)+1)
}
