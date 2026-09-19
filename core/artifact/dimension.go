// Package artifact defines artifact metadata shared by the engine and CLI.
package artifact

import (
	"fmt"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
)

// Dimension describes a schema axis, independent of its runtime keys.
type Dimension struct {
	Identifier    string `field:"true" doc:"Exact GraphQL ParentType.field identifier."`
	Name          string `field:"true" doc:"Short name derived from the author item type."`
	QualifiedName string `field:"true" doc:"Author parent type and field name, in CLI case."`
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
	var matches []string
	for _, dim := range dims {
		if dim.Name == name || dim.QualifiedName == name {
			matches = append(matches, dim.Identifier)
		}
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
