package core

import (
	"slices"
	"strings"

	"github.com/dagger/dagger/core/artifact"
	"github.com/dagger/dagger/core/artifact/flattening"
)

// ArtifactTypeColumnsVersion is the first API view with collection type columns.
const ArtifactTypeColumnsVersion = "v1.0.0-beta.28"

// ProjectionField translates schema nodes without deriving identity from a route.
func (a *Artifact) ProjectionField() flattening.Field {
	field := flattening.Field{Module: a.ModuleName, Type: a.TypeName,
		LocalPath: append([]string{}, a.LocalPath()...), Collections: []flattening.Collection{}}
	if a.Node == nil {
		field.LocalPath = append([]string{}, a.Path...)
		if len(field.LocalPath) > 0 && a.ModuleName != "" && SameCLIName(field.LocalPath[0], a.ModuleName) {
			field.LocalPath = field.LocalPath[1:]
		}
	}
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

// LocalPath is the literal schema field path from the nearest collection item,
// or from the exposed module root. It excludes synthetic collection lookups.
// An empty path identifies the root itself.
func (a *Artifact) LocalPath() []string {
	if a.Node == nil {
		return a.localPathWithoutNode()
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
	// A generator's synthetic stale artifact retains the generator node.
	// Preserve fields appended to its public path after that node's path.
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

func (a *Artifact) localPathWithoutNode() []string {
	path := a.Path
	if len(path) > 0 && a.ModuleName != "" && SameCLIName(path[0], a.ModuleName) {
		path = path[1:]
	}
	// Older metadata and hand-built artifacts can lack the module tree. They
	// have no schema spelling to recover. Keep their literal path after removing
	// the last collection route.
	for i := len(a.DimensionKeys) - 1; i >= 0; i-- {
		key := a.DimensionKeys[i]
		if !strings.HasPrefix(key.Dimension, "/") {
			continue
		}
		route := strings.Split(strings.TrimPrefix(key.Dimension, "/"), "/")
		if len(route) > 0 && a.ModuleName != "" && SameCLIName(route[0], a.ModuleName) {
			route = route[1:]
		}
		if len(route) <= len(path) && slices.EqualFunc(route, path[:len(route)], SameCLIName) {
			path = path[len(route):]
		}
		break
	}
	return slices.Clone(path)
}

// CanonicalCoordinates returns module, collection, and full field name values.
// Legacy DimensionKeys remain unchanged for address and selector compatibility.
func (a *Artifact) CanonicalCoordinates() []flattening.Coordinate {
	fieldPath := a.ProjectionField().FieldPath()
	collections := make([]flattening.Coordinate, 0, len(a.DimensionKeys))
	for node := a.Node; node != nil; node = node.Parent {
		if node.CollectionDimension != nil && node.CollectionKey != nil {
			identifier := node.CollectionDimension.CollectionType
			if identifier == "" {
				identifier = node.CollectionDimension.Identifier
			}
			collections = append(collections, flattening.Coordinate{Dimension: identifier, Key: *node.CollectionKey})
		}
	}
	if len(collections) != 0 {
		slices.Reverse(collections)
		return flattening.CanonicalCoordinates(a.ModuleName, fieldPath, collections)
	}
	// Older snapshots and synthetic artifacts can carry keys without item nodes.
	definitions := a.DimensionDefinitions()
	for _, key := range a.DimensionKeys {
		identifier := key.Dimension
		for _, dimension := range definitions {
			if dimension.Identifier == identifier && dimension.CollectionType != "" {
				identifier = dimension.CollectionType
				break
			}
		}
		collections = append(collections, flattening.Coordinate{Dimension: identifier, Key: key.Key})
	}
	return flattening.CanonicalCoordinates(a.ModuleName, fieldPath, collections)
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
