package core

import (
	"fmt"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/idproto"
)

type Void struct{}

var _ dagql.Typed = Void{}

func (p Void) TypeName() string {
	return "Void"
}

func (p Void) TypeDescription() string {
	return dagql.FormatDescription(
		`The absence of a value.`,
		`A Null Void is used as a placeholder for resolvers that do not return anything.`)
}

func (p Void) Type() *ast.Type {
	return &ast.Type{
		NamedType: p.TypeName(),
		NonNull:   true,
	}
}

var _ dagql.Input = Void{}

func (p Void) Decoder() dagql.InputDecoder {
	return p
}

func (p Void) ToLiteral() *idproto.Literal {
	return &idproto.Literal{
		Value: &idproto.Literal_Null{
			Null: true,
		},
	}
}

var _ dagql.ScalarType = Void{}

func (Void) DecodeInput(val any) (dagql.Input, error) {
	switch val.(type) {
	case nil:
		return nil, nil
	default:
		return nil, fmt.Errorf("cannot convert %T to Void", val)
	}
}
