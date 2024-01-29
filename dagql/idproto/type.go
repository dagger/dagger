package idproto

import "github.com/vektah/gqlparser/v2/ast"

func NewType(gqlType *ast.Type) *Type {
	t := &Type{
		NamedType: gqlType.NamedType,
		NonNull:   gqlType.NonNull,
	}
	if gqlType.Elem != nil {
		t.Elem = NewType(gqlType.Elem)
	}
	return t
}

func (t *Type) ToAST() *ast.Type {
	a := &ast.Type{
		NamedType: t.NamedType,
		NonNull:   t.NonNull,
	}
	if t.Elem != nil {
		a.Elem = t.Elem.ToAST()
	}
	return a
}
