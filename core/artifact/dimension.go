// Package artifact defines artifact metadata shared by the engine and CLI.
package artifact

import (
	"fmt"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
)

// Dimension describes a schema axis, independent of its runtime keys.
type Dimension struct {
	Kind           string
	CollectionType string
	Identifier     string `field:"true" doc:"Stable identifier: ParentType.field for a collection or type:TypeName for an artifact type."`
	Name           string `field:"true" doc:"Short name derived from the collection item type or artifact type."`
	QualifiedName  string `field:"true" doc:"Qualified name used when the short name is ambiguous."`
	ItemType       string `field:"true" doc:"The collection item type or artifact type name."`
	KeyName        string `field:"true" doc:"The collection key argument name, or name for a type dimension."`
	KeyDescription string `field:"true" doc:"The collection key argument description, or empty for a type dimension."`
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
	for _, dim := range dims {
		if dim.Name == name || dim.QualifiedName == name {
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
	for _, alias := range []string{dim.Name, dim.QualifiedName} {
		if id, err := dims.Resolve(alias); err == nil && id == dim.Identifier {
			return alias
		}
	}
	return dim.Identifier
}
