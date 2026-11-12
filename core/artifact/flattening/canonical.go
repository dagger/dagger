package flattening

import "strings"

// NameDimension identifies the full ordinary field path from the module root.
const NameDimension = "name"

// CanonicalCoordinates assigns module, collection, and name coordinates.
// fieldPath contains every ordinary schema field from the exposed module root,
// including collection fields. It excludes the module prefix and synthetic get
// operations. Field.FieldPath builds this path from nearest-item input metadata.
// An empty path names the root itself with ".". Field spelling is preserved.
//
// Collection coordinates keep their order and values. Existing module, name,
// and legacy type coordinates are replaced. Collection identifiers must already
// be exact schema collection type names. No artifact type or selected schema is
// needed: neither can change an artifact's canonical coordinates.
func CanonicalCoordinates(module string, fieldPath []string, collections []Coordinate) []Coordinate {
	coordinates := make([]Coordinate, 0, len(collections)+2)
	if module != "" {
		coordinates = append(coordinates, Coordinate{Dimension: ModuleDimension, Key: module})
	}
	for _, collection := range collections {
		if !IsStaticDimension(collection.Dimension) {
			coordinates = append(coordinates, collection)
		}
	}
	name := strings.Join(fieldPath, ".")
	if len(fieldPath) == 0 {
		name = "."
	}
	return append(coordinates, Coordinate{Dimension: NameDimension, Key: name})
}

// CanonicalDimensions replaces legacy static metadata with module and name
// metadata. Collection definitions use their exact CollectionType identities
// and retain first-occurrence order. Repeated collection types add one column.
// CollectionType and ItemType must be supplied by schema metadata.
// The result owns its metadata, so changes do not affect the input.
func CanonicalDimensions(module string, dimensions Dimensions) Dimensions {
	result := make(Dimensions, 0, len(dimensions)+2)
	if module != "" {
		result = append(result, &Dimension{
			Kind: "MODULE", Identifier: ModuleDimension, Name: ModuleDimension,
			QualifiedName: ModuleDimension, KeyName: "name",
		})
	}
	seen := make(map[string]bool)
	for _, dimension := range dimensions {
		if dimension.Kind != "COLLECTION" || seen[dimension.CollectionType] {
			continue
		}
		seen[dimension.CollectionType] = true
		result = append(result, (Collection{
			Type: dimension.CollectionType, ItemType: dimension.ItemType,
			KeyName: dimension.KeyName, KeyDescription: dimension.KeyDescription,
		}).CanonicalDimension())
	}
	return append(result, &Dimension{
		Kind: "NAME", Identifier: NameDimension, Name: NameDimension,
		QualifiedName: NameDimension, KeyName: "name",
	})
}
