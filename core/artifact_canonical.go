package core

import (
	"slices"

	"github.com/dagger/dagger/core/artifact"
	"github.com/dagger/dagger/core/artifact/flattening"
)

const ArtifactTypeColumnsVersion = "v1.0.0-beta.28"

func (a *Artifact) ProjectionField() flattening.Field {
	field := flattening.Field{Module: a.ModuleName, Type: a.TypeName,
		LocalPath: append([]string{}, a.LocalPath()...), Collections: []flattening.Collection{}}

	for node := a.Node; node != nil; node = node.Parent {
		if node.CollectionDimension == nil {
			continue
		}
		dimension := node.CollectionDimension
		parent := &Artifact{ModuleName: a.ModuleName, Node: node.Parent}
		field.Collections = append(field.Collections, flattening.Collection{
			Fields: append([]string{}, parent.LocalPath()...), Type: dimension.CollectionType, ItemType: dimension.ItemType,
			KeyName: dimension.KeyName, KeyDescription: dimension.KeyDescription,
		})
	}
	slices.Reverse(field.Collections)
	return field
}

func (a *Artifact) LocalPath() []string {
	if a.Node == nil {
		path := a.Path
		if len(path) > 0 && a.ModuleName != "" && SameCLIName(path[0], a.ModuleName) {
			path = path[1:]
		}
		return slices.Clone(path)
	}
	var route []flattening.PathStep
	for node := a.Node; node != nil; node = node.Parent {
		route = append(route, flattening.PathStep{
			Field:          node.Name,
			CollectionItem: node.CollectionDimension != nil,
			ModuleRoot: node.Parent == nil ||
				a.ModuleName != "" && SameCLIName(node.Name, a.ModuleName) && len(node.Path()) == 1,
		})
	}
	slices.Reverse(route)
	path := flattening.LocalFieldPath(route)

	for _, nodePath := range [][]string{a.Node.CLIPath(), a.Node.CommandCLIPath()} {
		if len(nodePath) > 0 && len(nodePath) < len(a.Path) && slices.Equal(nodePath, a.Path[:len(nodePath)]) {
			path = append(path, a.Path[len(nodePath):]...)
			break
		}
	}
	if len(a.Node.CommandCLIPath()) == 0 && slices.Equal(a.Path, []string{"stale"}) {
		path = append(path, "stale")
	}
	return path
}

func (a *Artifact) CanonicalCoordinates() []flattening.Coordinate {
	var collections []flattening.Coordinate
	for node := a.Node; node != nil; node = node.Parent {
		if node.CollectionDimension != nil && node.CollectionKey != nil {
			collections = append(collections, flattening.Coordinate{
				Dimension: node.CollectionDimension.CollectionType,
				Key:       *node.CollectionKey,
			})
		}
	}
	slices.Reverse(collections)
	return flattening.CanonicalCoordinates(a.ModuleName, a.ProjectionField().FieldPath(), collections)
}

func (a *Artifact) CanonicalDimensionKeys() []*ArtifactDimensionKey {
	coordinates := a.CanonicalCoordinates()
	keys := make([]*ArtifactDimensionKey, len(coordinates))
	for i, coordinate := range coordinates {
		keys[i] = &ArtifactDimensionKey{Dimension: coordinate.Dimension, Key: coordinate.Key}
	}
	return keys
}

func (a *Artifact) hasDimension(identifier string) bool {
	if identifier == artifact.NameDimension {
		return a.TypeName != ""
	}
	return slices.ContainsFunc(a.DimensionDefinitions(), func(d *ArtifactDimension) bool { return d.Identifier == identifier || d.CollectionType == identifier })
}
