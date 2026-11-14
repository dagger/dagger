package flattening

import "strings"

const NameDimension = "name"

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
