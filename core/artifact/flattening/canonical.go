package flattening

import "strings"

// NameDimension identifies the full field path within an artifact's local root.
const NameDimension = "name"

// CanonicalCoordinates assigns module, collection, and name coordinates.
// localPath contains actual schema field names relative to the nearest
// enclosing collection item, or to the module's exposed root if there is no
// enclosing item. It excludes the module prefix and synthetic get operations.
// An empty path names the root itself with ".". Field spelling is preserved.
//
// Collection coordinates keep their order and values. Existing module, name,
// and legacy type coordinates are replaced. Collection identifiers retain
// their current /module/field form. No artifact type or selected schema is
// needed: neither can change an artifact's canonical coordinates.
func CanonicalCoordinates(module string, localPath []string, collections []Coordinate) []Coordinate {
	coordinates := make([]Coordinate, 0, len(collections)+2)
	if module != "" {
		coordinates = append(coordinates, Coordinate{Dimension: ModuleDimension, Key: module})
	}
	for _, collection := range collections {
		if collection.Dimension != NameDimension && !IsStaticDimension(collection.Dimension) {
			coordinates = append(coordinates, collection)
		}
	}
	name := strings.Join(localPath, ".")
	if len(localPath) == 0 {
		name = "."
	}
	return append(coordinates, Coordinate{Dimension: NameDimension, Key: name})
}

// CanonicalDimensions replaces legacy static metadata with module and name
// metadata. Collection definitions keep their order and current identities.
// The result owns its metadata, so changes do not affect the input.
func CanonicalDimensions(module string, dimensions Dimensions) Dimensions {
	result := make(Dimensions, 0, len(dimensions)+2)
	if module != "" {
		result = append(result, &Dimension{
			Kind: "MODULE", Identifier: ModuleDimension, Name: ModuleDimension,
			QualifiedName: ModuleDimension, KeyName: "name",
		})
	}
	for _, dimension := range dimensions {
		if dimension.Kind == "COLLECTION" {
			copy := *dimension
			result = append(result, &copy)
		}
	}
	return append(result, &Dimension{
		Kind: "NAME", Identifier: NameDimension, Name: NameDimension,
		QualifiedName: NameDimension, KeyName: "name",
	})
}
