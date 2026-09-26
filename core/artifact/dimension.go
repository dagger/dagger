// Package artifact defines artifact metadata shared by the engine and CLI.
package artifact

import (
	"fmt"
	"slices"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
)

const ModuleDimension = "module"

// IsStaticDimension identifies keys available without loading collections.
func IsStaticDimension(identifier string) bool {
	return identifier == ModuleDimension || strings.HasPrefix(identifier, "type:")
}

// Dimension describes a schema axis, independent of its runtime keys.
type Dimension struct {
	Kind           string
	CollectionType string
	Identifier     string `field:"true" doc:"Stable identifier: collection schema path, type:TypeName for an artifact type, or module."`
	Name           string `field:"true" doc:"Short name used to select this dimension."`
	QualifiedName  string `field:"true" doc:"Qualified name used when the short name is ambiguous."`
	ItemType       string `field:"true" doc:"The collection item or artifact type name, or empty for the module dimension."`
	KeyName        string `field:"true" doc:"The collection key argument name, or name for a static dimension."`
	KeyDescription string `field:"true" doc:"The collection key argument description, or empty for a static dimension."`
}

func (*Dimension) Type() *ast.Type {
	return &ast.Type{NamedType: "ArtifactDimension", NonNull: true}
}

// Dimensions is the set of dimensions in a selected schema scope.
type Dimensions []*Dimension

// Names lists selectors from shortest to most qualified. Collection boundaries
// provide parent identity; their fields are needed in names only on a conflict.
func (dims Dimensions) Names(dim *Dimension, wholeCollection bool) []string {
	if IsStaticDimension(dim.Identifier) {
		return []string{dim.Name, dim.QualifiedName}
	}
	path := strings.Split(strings.TrimPrefix(dim.Identifier, "/"), "/")
	module := path[0]
	item := strings.TrimPrefix(dim.Name, module+"-")
	if wholeCollection {
		if len(path) == 1 {
			return []string{module}
		}
		item = path[len(path)-1]
	}
	var names []string
	add := func(qualifiers []string) {
		parts := append([]string{module}, qualifiers...)
		parts = append(parts, item)
		name := strings.Join(parts, "-")
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	add(nil)
	if len(path) == 1 {
		return names
	}
	var local []string
	for i := 1; i < len(path)-1; i++ {
		parent := strings.Join(path[:i+1], "/")
		if !slices.ContainsFunc(dims, func(d *Dimension) bool { return strings.TrimPrefix(d.Identifier, "/") == parent }) {
			local = append(local, path[i])
		}
	}
	for i := len(local) - 1; i >= 0; i-- {
		add(local[i:])
	}
	// Include parent collection names, then this collection field, only if the
	// local names above cannot distinguish the routes.
	for _, context := range [][]string{path[1 : len(path)-1], path[1:]} {
		for i := len(context) - 1; i >= 0; i-- {
			add(context[i:])
		}
	}
	return names
}

// Resolve binds an identifier or alias. Unknown names remain valid selectors
// that match no artifacts.
func (dims Dimensions) Resolve(name string) (string, error) {
	for _, dim := range dims {
		if dim.Identifier == name {
			return name, nil
		}
	}
	var matches, types []string
	qualified := slices.ContainsFunc(dims, func(d *Dimension) bool { return d.QualifiedName == name })
	for _, dim := range dims {
		if qualified && dim.QualifiedName != name {
			continue
		}
		matchesName := dim.Name == name || dim.QualifiedName == name || slices.Contains(dims.Names(dim, false), name)
		if !matchesName && !IsStaticDimension(dim.Identifier) {
			matchesName = slices.Contains(dims.Names(dim, true), name)
		}
		if matchesName {
			if dim.Kind == "TYPE" {
				types = append(types, dim.Identifier)
			} else {
				matches = append(matches, dim.Identifier)
			}
		}
	}
	// Collection aliases retain their meaning when an item type has the same name.
	if len(matches) == 0 {
		matches = types
	}
	switch len(matches) {
	case 0:
		return name, nil
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("ambiguous dimension %q: use %s", name, strings.Join(matches, " or "))
	}
}

// DisplayName returns the shortest unambiguous name for a dimension.
func (dims Dimensions) DisplayName(dim *Dimension) string {
	for _, alias := range append([]string{dim.Name}, dims.Names(dim, false)...) {
		if id, err := dims.Resolve(alias); err == nil && id == dim.Identifier {
			return alias
		}
	}
	return dim.Identifier
}
