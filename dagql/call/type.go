package call

import (
	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/vektah/gqlparser/v2/ast"
)

type Type struct {
	pb *callpbv1.Type
}

func NewType(gqlType *ast.Type) *Type {
	return &Type{pb: newPBType(gqlType)}
}

func (t *Type) NamedType() string {
	return t.pb.NamedType
}

func (t *Type) ToAST() *ast.Type {
	return t.pb.ToAST()
}

func newPBType(gqlType *ast.Type) *callpbv1.Type {
	t := &callpbv1.Type{
		NamedType: gqlType.NamedType,
		NonNull:   gqlType.NonNull,
	}
	if gqlType.Elem != nil {
		t.Elem = newPBType(gqlType.Elem)
	}
	return t
}
