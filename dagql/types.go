package dagql

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/dagger/dagql/idproto"
	"github.com/vektah/gqlparser/v2/ast"
	"google.golang.org/protobuf/proto"
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

func (i Int) MarshalJSON() ([]byte, error) {
	return json.Marshal(i.Value)
}

func (i *Int) UnmarshalJSON(p []byte) error {
	var num int
	if err := json.Unmarshal(p, &num); err != nil {
		return err
	}
	i.Value = num
	return nil
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

func (i ID[T]) Encode() (string, error) {
	proto, err := proto.Marshal(i.ID)
	if err != nil {
		return "", err
	}
	enc := base64.URLEncoding.EncodeToString(proto)
	return enc, nil
}

func (i *ID[T]) Decode(str string) error {
	bytes, err := base64.URLEncoding.DecodeString(str)
	if err != nil {
		return err
	}
	var idproto idproto.ID
	if err := proto.Unmarshal(bytes, &idproto); err != nil {
		return err
	}
	if idproto.TypeName != i.expected.TypeName() {
		return fmt.Errorf("expected %q ID, got %q ID", i.expected.TypeName(), idproto.TypeName)
	}
	i.ID = &idproto
	return nil
}

func (i ID[T]) MarshalJSON() ([]byte, error) {
	enc, err := i.Encode()
	if err != nil {
		return nil, err
	}
	return json.Marshal(enc)
}

func (i *ID[T]) UnmarshalJSON(p []byte) error {
	var str string
	if err := json.Unmarshal(p, &str); err != nil {
		return err
	}
	return i.Decode(str)
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
