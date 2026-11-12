package flattening

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// Field describes one artifact field, including fields in empty collections.
// LocalPath contains literal schema field names after the nearest collection
// item, or after the exposed module root. An empty path names that root.
// Collections lists collection boundaries in parent-to-child order.
type Field struct {
	Module      string       `json:"module"`
	Type        string       `json:"type"`
	LocalPath   []string     `json:"localPath"`
	Collections []Collection `json:"collections"`
}

// Schema holds the complete field metadata before callers select rows.
// Artifact types identify fields but do not add coordinate dimensions.
type Schema struct {
	dimensions Dimensions
	fields     map[string]schemaField
}

type schemaField struct {
	module      string
	localPath   []string
	collections []string
}

// NewSchema compiles canonical dimensions from all artifact fields. It rejects
// collection routes whose current naming rules lose their distinct identities.
func NewSchema(fields []Field) (*Schema, error) {
	schema := &Schema{fields: make(map[string]schemaField)}
	byID := make(map[string]*Dimension)
	routes := make(map[string]string)
	for _, field := range fields {
		compiled, dimensions, literalRoutes, err := compileField(field)
		if err != nil {
			return nil, err
		}
		for i, dimension := range dimensions {
			id := dimension.Identifier
			if route, exists := routes[id]; exists && route != literalRoutes[i] {
				return nil, fmt.Errorf("distinct collection routes share dimension %s", id)
			}
			routes[id] = literalRoutes[i]
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
	slices.SortFunc(schema.dimensions, func(a, b *Dimension) int {
		return strings.Compare(a.Identifier, b.Identifier)
	})
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
	name := strings.Join(field.LocalPath, ".")
	if len(field.LocalPath) == 0 {
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
	return CanonicalCoordinates(known.module, known.localPath, collections), nil
}

func compileField(field Field) (schemaField, Dimensions, []string, error) {
	if field.Type == "" {
		return schemaField{}, nil, nil, fmt.Errorf("field has no artifact type")
	}
	if err := validateFieldSegments(field.LocalPath); err != nil {
		return schemaField{}, nil, nil, err
	}
	if len(field.Collections) != 0 && field.Module == "" {
		return schemaField{}, nil, nil, fmt.Errorf("collection fields need a module namespace")
	}
	compiled := schemaField{module: field.Module, localPath: slices.Clone(field.LocalPath)}
	var dimensions Dimensions
	var routes []string
	var literalFields [][]string
	var parent *Dimension
	for _, collection := range field.Collections {
		if collection.ItemType == "" {
			return schemaField{}, nil, nil, fmt.Errorf("collection has no item type")
		}
		if err := validateFieldSegments(collection.Fields); err != nil {
			return schemaField{}, nil, nil, err
		}
		dimension := collection.Dimension(field.Module, parent, nil)
		if slices.Contains(compiled.collections, dimension.Identifier) {
			return schemaField{}, nil, nil, fmt.Errorf("field has repeated dimension %s: collection items that are collections are unsupported", dimension.Identifier)
		}
		compiled.collections = append(compiled.collections, dimension.Identifier)
		dimensions = append(dimensions, dimension)
		literalFields = append(literalFields, append([]string{}, collection.Fields...))
		routes = append(routes, schemaKey(struct {
			Module string
			Fields [][]string
		}{field.Module, literalFields}))
		parent = dimension
	}
	return compiled, dimensions, routes, nil
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
	collections := make([][]string, len(field.Collections))
	for i, collection := range field.Collections {
		collections[i] = append([]string{}, collection.Fields...)
	}
	return schemaKey(struct {
		Module, Type string
		LocalPath    []string
		Collections  [][]string
	}{field.Module, field.Type, append([]string{}, field.LocalPath...), collections})
}

func schemaKey(value any) string {
	encoded, _ := json.Marshal(value) // All callers pass structs of strings and slices.
	return string(encoded)
}
