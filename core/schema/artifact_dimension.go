package schema

import (
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/vektah/gqlparser/v2/ast"
)

type artifactDimensionKind string

var artifactDimensionKinds = dagql.NewEnum[artifactDimensionKind]()
var (
	collectionDimension = artifactDimensionKinds.Register("COLLECTION")
	moduleDimension     = artifactDimensionKinds.Register("MODULE")
	typeDimension       = artifactDimensionKinds.Register("TYPE")
)

func (artifactDimensionKind) Type() *ast.Type {
	return &ast.Type{NamedType: "ArtifactDimensionKind", NonNull: true}
}
func (artifactDimensionKind) Decoder() dagql.InputDecoder { return artifactDimensionKinds }
func (kind artifactDimensionKind) ToLiteral() call.Literal {
	return artifactDimensionKinds.Literal(kind)
}
