package flattening

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// Field describes one artifact field, including fields in empty collections.
// LocalPath contains literal schema field names after the nearest collection
// item, or after the exposed module root. An empty path selects that root.
// The final name uses FieldPath, which includes enclosing collection fields.
// Collections lists collection boundaries in parent-to-child order.
type Field struct {
	Module      string       `json:"module"`
	Type        string       `json:"type"`
	LocalPath   []string     `json:"localPath"`
	Collections []Collection `json:"collections"`
}

// FieldPath returns every ordinary schema field from the exposed module root.
// Collection lookups add keys, not field names. The caller owns the result.
func (f Field) FieldPath() []string {
	var path []string
	for _, collection := range f.Collections {
		path = append(path, collection.Fields...)
	}
	return append(path, f.LocalPath...)
}

// Schema holds the complete field metadata before callers select rows.
// Artifact types identify fields but do not add coordinate dimensions.
type Schema struct {
	dimensions Dimensions
	fields     map[string]schemaField
}

type schemaField struct {
	module      string
	fieldPath   []string
	collections []string
}

// NewSchema compiles canonical dimensions from all artifact fields. Collection
// types define columns; field routes do not change their identities.
func NewSchema(fields []Field) (*Schema, error) {
	schema := &Schema{fields: make(map[string]schemaField)}
	byID := make(map[string]*Dimension)
	for _, field := range fields {
		compiled, dimensions, err := compileField(field)
		if err != nil {
			return nil, err
		}
		for _, dimension := range CanonicalDimensions(field.Module, dimensions) {
			id := dimension.Identifier
			if previous, exists := byID[id]; exists && *previous != *dimension {
				return nil, fmt.Errorf("conflicting metadata for dimension %s", id)
			}
			byID[id] = dimension
		}
		schema.fields[fieldIdentity(field)] = compiled
	}
	for _, dimension := range byID {
		schema.dimensions = append(schema.dimensions, dimension)
	}
	var err error
	schema.dimensions, err = NameDimensions(schema.dimensions)
	if err != nil {
		return nil, err
	}
	return schema, nil
}

// Dimensions returns all schema dimensions in identifier order. The caller
// owns the slice and its metadata.
func (s *Schema) Dimensions() Dimensions {
	result := make(Dimensions, len(s.dimensions))
	for i, dimension := range s.dimensions {
		copy := *dimension
		result[i] = &copy
	}
	return result
}

// Coordinates assigns one key per collection boundary to a known field.
// Empty keys are valid. The result does not depend on the selected fields.
func (s *Schema) Coordinates(field Field, keys ...string) ([]Coordinate, error) {
	known, exists := s.fields[fieldIdentity(field)]
	name := strings.Join(field.FieldPath(), ".")
	if name == "" {
		name = "."
	}
	if !exists {
		return nil, fmt.Errorf("field %s is absent from the flattening schema", name)
	}
	if len(keys) != len(known.collections) {
		return nil, fmt.Errorf("field %s needs %d collection keys, got %d", name, len(known.collections), len(keys))
	}
	collections := make([]Coordinate, len(keys))
	for i, key := range keys {
		collections[i] = Coordinate{Dimension: known.collections[i], Key: key}
	}
	return CanonicalCoordinates(known.module, known.fieldPath, collections), nil
}

func compileField(field Field) (schemaField, Dimensions, error) {
	if field.Type == "" {
		return schemaField{}, nil, fmt.Errorf("field has no artifact type")
	}
	if err := validateFieldSegments(field.LocalPath); err != nil {
		return schemaField{}, nil, err
	}
	compiled := schemaField{module: field.Module, fieldPath: field.FieldPath()}
	var dimensions Dimensions
	for _, collection := range field.Collections {
		if collection.Type == "" {
			return schemaField{}, nil, fmt.Errorf("collection has no schema type")
		}
		if IsStaticDimension(collection.Type) || !schemaTypeIdentifier(collection.Type) {
			return schemaField{}, nil, fmt.Errorf("collection type %q must be a non-reserved schema type identifier", collection.Type)
		}
		if collection.ItemType == "" {
			return schemaField{}, nil, fmt.Errorf("collection has no item type")
		}
		if err := validateFieldSegments(collection.Fields); err != nil {
			return schemaField{}, nil, err
		}
		dimension := collection.CanonicalDimension()
		if slices.Contains(compiled.collections, dimension.Identifier) {
			return schemaField{}, nil, fmt.Errorf("field has repeated collection type %s: one column cannot hold two ancestor keys", dimension.Identifier)
		}
		compiled.collections = append(compiled.collections, dimension.Identifier)
		dimensions = append(dimensions, dimension)
	}
	return compiled, dimensions, nil
}

// schemaTypeIdentifier accepts the literal GraphQL name syntax. It does not
// normalize names or treat paths and legacy dimension IDs as schema types.
func schemaTypeIdentifier(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		char := name[i]
		if char == '_' || char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z' || i > 0 && char >= '0' && char <= '9' {
			continue
		}
		return false
	}
	return true
}

func validateFieldSegments(fields []string) error {
	for _, field := range fields {
		if field == "" || strings.ContainsAny(field, "./") {
			return fmt.Errorf("invalid field segment %q: use a nonempty literal field name without dots or slashes", field)
		}
	}
	return nil
}

// fieldIdentity uses literal boundaries, not normalized dimension names.
// Lookup and display metadata are excluded from field identity.
func fieldIdentity(field Field) string {
	type boundary struct {
		Type   string
		Fields []string
	}
	collections := make([]boundary, len(field.Collections))
	for i, collection := range field.Collections {
		collections[i] = boundary{collection.Type, append([]string{}, collection.Fields...)}
	}
	return schemaKey(struct {
		Module, Type string
		LocalPath    []string
		Collections  []boundary
	}{field.Module, field.Type, append([]string{}, field.LocalPath...), collections})
}

func schemaKey(value any) string {
	encoded, _ := json.Marshal(value) // All callers pass structs of strings and slices.
	return string(encoded)
}
