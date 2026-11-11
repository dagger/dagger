package core

import (
	"slices"
	"strings"

	"github.com/dagger/dagger/core/artifact"
	"github.com/dagger/dagger/core/artifact/flattening"
)

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

// CanonicalCoordinates returns module, collection, and full local name values.
// Legacy DimensionKeys remain unchanged for address and selector compatibility.
func (a *Artifact) CanonicalCoordinates() []flattening.Coordinate {
	collections := make([]flattening.Coordinate, 0, len(a.DimensionKeys))
	for _, key := range a.DimensionKeys {
		collections = append(collections, flattening.Coordinate{Dimension: key.Dimension, Key: key.Key})
	}
	return flattening.CanonicalCoordinates(a.ModuleName, a.LocalPath(), collections)
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
	return slices.ContainsFunc(a.DimensionDefinitions(), func(d *ArtifactDimension) bool { return d.Identifier == identifier })
}
