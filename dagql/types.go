package dagql

import (
	"fmt"

	"github.com/dagger/dagql/idproto"
	"github.com/vektah/gqlparser/v2/ast"
)

type Typed interface {
	TypeName() string
}

func TypeOf(val any) (*ast.Type, error) {
	switch x := val.(type) {
	case int: // TODO deprecate this, Typed should just return *ast.Type?
		return &ast.Type{NamedType: "Int", NonNull: true}, nil
	case string:
		return &ast.Type{NamedType: "String", NonNull: true}, nil
	case Nullable:
		t, err := TypeOf(x.NullableValue())
		if err != nil {
			return nil, err
		}
		t.NonNull = false
		return t, nil
	case Typed:
		return &ast.Type{NamedType: x.TypeName(), NonNull: true}, nil
	default:
		return nil, fmt.Errorf("cannot determine type name of %T", x)
	}
}

type Int struct {
	Value int
}

func (Int) TypeName() string {
	return "Int"
}

func (i Int) MarshalLiteral() (*idproto.Literal, error) {
	return idproto.LiteralValue(i.Value), nil
}

func (i *Int) UnmarshalLiteral(lit *idproto.Literal) error {
	switch x := lit.Value.(type) {
	case *idproto.Literal_Int:
		i.Value = int(lit.GetInt())
	default:
		return fmt.Errorf("cannot convert %T to Int", x)
	}
	return nil
}

type ID[T Typed] struct {
	*idproto.ID

	expected T
}

func (i ID[T]) TypeName() string {
	return i.expected.TypeName() + "ID"
}

func (i ID[T]) MarshalLiteral() (*idproto.Literal, error) {
	return idproto.LiteralValue(i.ID), nil
}

func (i *ID[T]) UnmarshalLiteral(lit *idproto.Literal) error {
	switch x := lit.Value.(type) {
	case *idproto.Literal_Id:
		if x.Id.TypeName != i.expected.TypeName() {
			return fmt.Errorf("expected %q, got %q", i.expected.TypeName(), x.Id.TypeName)
		}
		i.ID = x.Id
	default:
		return fmt.Errorf("cannot convert %T to ID", x)
	}
	return nil
}
