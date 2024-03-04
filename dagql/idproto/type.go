package idproto

import (
	"fmt"
	"hash"

	"github.com/vektah/gqlparser/v2/ast"
)

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

func (t *Type) digestInto(h hash.Hash) error {
	if _, err := h.Write([]byte(t.NamedType)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(h, "%t", t.NonNull); err != nil {
		return err
	}
	if t.Elem != nil {
		if err := t.Elem.digestInto(h); err != nil {
			return err
		}
	}
	return nil
}
