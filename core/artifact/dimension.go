// Package artifact defines artifact metadata shared by the engine and CLI.
package artifact

import (
	"fmt"
	"slices"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
)

const ModuleDimension = "module"

// IsStaticDimension identifies module and type dimensions.
func IsStaticDimension(identifier string) bool {
	return identifier == ModuleDimension || strings.HasPrefix(identifier, "type:")
}

// Dimension describes a schema axis, independent of its runtime keys.
type Dimension struct {
	Kind          string
	Identifier    string `field:"true" doc:"Stable identifier: type:TypeName for an artifact type, or module."`
	Name          string `field:"true" doc:"Short name used to select this dimension."`
	QualifiedName string `field:"true" doc:"Qualified name used when the short name is ambiguous."`
	ItemType      string `field:"true" doc:"The artifact type name, or empty for the module dimension."`
}

func (*Dimension) Type() *ast.Type {
	return &ast.Type{NamedType: "ArtifactDimension", NonNull: true}
}

// Dimensions is the set of dimensions in a selected schema scope.
type Dimensions []*Dimension

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
		if dim.Name == name || dim.QualifiedName == name {
			if dim.Kind == "TYPE" {
				types = append(types, dim.Identifier)
			} else {
				matches = append(matches, dim.Identifier)
			}
		}
	}
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
