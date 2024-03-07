package idproto

import (
	"github.com/vektah/gqlparser/v2/ast"
)

type Type struct {
	raw *RawType
}

func NewType(gqlType *ast.Type) *Type {
	return &Type{raw: newRawType(gqlType)}
}

func (t *Type) NamedType() string {
	return t.raw.NamedType
}

func (t *Type) ToAST() *ast.Type {
	return t.raw.toAST()
}

func newRawType(gqlType *ast.Type) *RawType {
	t := &RawType{
		NamedType: gqlType.NamedType,
		NonNull:   gqlType.NonNull,
	}
	if gqlType.Elem != nil {
		t.Elem = newRawType(gqlType.Elem)
	}
	return t
}

func (t *RawType) toAST() *ast.Type {
	a := &ast.Type{
		NamedType: t.NamedType,
		NonNull:   t.NonNull,
	}
	if t.Elem != nil {
		a.Elem = t.Elem.toAST()
	}
	return a
}
