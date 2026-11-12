// Package artifact defines artifact metadata shared by the engine and CLI.
package artifact

import (
	"github.com/dagger/dagger/core/artifact/flattening"

	"github.com/vektah/gqlparser/v2/ast"
)

const ModuleDimension = flattening.ModuleDimension
const NameDimension = flattening.NameDimension

// IsStaticDimension identifies keys available without loading collections.
func IsStaticDimension(identifier string) bool {
	return flattening.IsStaticDimension(identifier)
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

// Union combines dimension sets, sorted by identifier.
func Union(sets ...Dimensions) Dimensions {
	projected := make([]flattening.Dimensions, len(sets))
	for i, set := range sets {
		projected[i] = set.flattening()
	}
	union := flattening.Union(projected...)
	result := make(Dimensions, len(union))
	for i, dim := range union {
		result[i] = (*Dimension)(dim)
	}
	return result
}

func (dims Dimensions) flattening() flattening.Dimensions {
	if dims == nil {
		return nil
	}
	result := make(flattening.Dimensions, len(dims))
	for i, dim := range dims {
		result[i] = (*flattening.Dimension)(dim)
	}
	return result
}

// Names lists selectors from shortest to most qualified. Collection boundaries
// provide parent identity; their fields are needed in names only on a conflict.
func (dims Dimensions) Names(dim *Dimension, wholeCollection bool) []string {
	return dims.flattening().Names((*flattening.Dimension)(dim), wholeCollection)
}

// Resolve binds an identifier or alias. Unknown names remain valid selectors
// that match no artifacts.
func (dims Dimensions) Resolve(name string) (string, error) {
	return dims.flattening().Resolve(name)
}

// DisplayName returns the shortest unambiguous name for a dimension.
func (dims Dimensions) DisplayName(dim *Dimension) string {
	return dims.flattening().DisplayName((*flattening.Dimension)(dim))
}
