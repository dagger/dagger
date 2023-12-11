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
	Type() *ast.Type
}

type Int struct {
	Value int
}

func (Int) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Int",
		NonNull:   true,
	}
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

type String struct {
	Value string
}

func (String) Type() *ast.Type {
	return &ast.Type{
		NamedType: "String",
		NonNull:   true,
	}
}

func (i String) MarshalJSON() ([]byte, error) {
	return json.Marshal(i.Value)
}

func (i *String) UnmarshalJSON(p []byte) error {
	var str string
	if err := json.Unmarshal(p, &str); err != nil {
		return err
	}
	i.Value = str
	return nil
}

func (i String) MarshalLiteral() (*idproto.Literal, error) {
	return idproto.LiteralValue(i.Value), nil
}

func (i *String) UnmarshalLiteral(lit *idproto.Literal) error {
	switch x := lit.Value.(type) {
	case *idproto.Literal_String_:
		i.Value = lit.GetString_()
	default:
		return fmt.Errorf("cannot convert %T to String", x)
	}
	return nil
}

type ID[T Typed] struct {
	*idproto.ID

	expected T
}

func (i ID[T]) Type() *ast.Type {
	return &ast.Type{
		NamedType: i.expected.Type().Name() + "ID",
		NonNull:   true,
	}
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
	if idproto.TypeName != i.expected.Type().Name() {
		return fmt.Errorf("expected %q ID, got %q ID", i.expected.Type().Name(), idproto.TypeName)
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
		if x.Id.TypeName != i.expected.Type().Name() {
			return fmt.Errorf("expected %q, got %q", i.expected.Type().Name(), x.Id.TypeName)
		}
		i.ID = x.Id
	case *idproto.Literal_String_:
		return i.Decode(x.String_)
	default:
		return fmt.Errorf("cannot convert %T to ID", x)
	}
	return nil
}

type Enumerable interface {
	Len() int
	Nth(int) (Typed, error)
}

func BasicArray[T Typed](arrayID *idproto.ID, vals ...T) Array[Identified[T]] {
	var arr Array[Identified[T]]
	for _, val := range vals {
		arr = append(arr, Identified[T]{
			ValueID: ID[T]{
				ID:       arrayID,
				expected: val,
			},
			Value: val,
		})
	}
	return arr
}

type Identified[T Typed] struct {
	ValueID ID[T]
	Value   T
}

var _ Node = Identified[Typed]{}

func (i Identified[T]) Type() *ast.Type {
	return i.Value.Type()
}

func (i Identified[T]) ID() *idproto.ID {
	return i.ValueID.ID
}

type Array[T Typed] []T

func (i Array[T]) Type() *ast.Type {
	var t T
	return &ast.Type{
		Elem:    t.Type(),
		NonNull: true,
	}
}

var _ Enumerable = Array[Node]{}

func (arr Array[T]) Len() int {
	return len(arr)
}

func (arr Array[T]) Nth(i int) (Typed, error) {
	if i < 1 || i > len(arr) {
		return nil, fmt.Errorf("index %d out of bounds", i)
	}
	return arr[i-1], nil
}

func (i Array[T]) MarshalJSON() ([]byte, error) {
	return json.Marshal([]T(i))
}

func (i *Array[T]) UnmarshalJSON(p []byte) error {
	var arr []T
	if err := json.Unmarshal(p, &arr); err != nil {
		return err
	}
	*i = arr
	return nil
}

// TODO
func (i *Array[T]) UnmarshalLiteral(lit *idproto.Literal) error {
	// switch x := lit.Value.(type) {
	// case *idproto.Literal_List:
	// 	var ts []T
	// 	for _, val := range x.List.Values {
	// 		ts = append(ts, Literal{val}.ToTyped())
	// 	}
	// 	*i = ts
	// default:
	// 	return fmt.Errorf("cannot convert %T to BasicArray", x)
	// }
	return nil
}

type Optional[T Typed] struct {
	Value T
	Valid bool
}

type Nullable interface {
	Unwrap() (Typed, bool)
}

var _ Typed = Optional[Typed]{}

func (n Optional[T]) Type() *ast.Type {
	t := n.Value.Type()
	t.NonNull = false
	return t
}

var _ Nullable = Optional[Typed]{}

func (n Optional[T]) Unwrap() (Typed, bool) {
	return n.Value, n.Valid
}

type Enum struct {
	Enum  *ast.Type
	Value string
}

var _ Typed = Enum{}

func (n Enum) Type() *ast.Type {
	return n.Enum
}

type EnumSpec struct {
	Name        string
	Description string
	Values      []*ast.EnumValueDefinition
}

func (n EnumSpec) Install(srv *Server) *ast.Definition {
	def := &ast.Definition{
		Kind:        ast.Enum,
		Name:        n.Name,
		Description: n.Description,
		EnumValues:  n.Values,
	}
	srv.schema.AddTypes(def)
	return def
}

func (n EnumSpec) Type() *ast.Type {
	return &ast.Type{
		NamedType: n.Name,
		NonNull:   true,
	}
}

func Opt[T Typed](v T) Optional[T] {
	return Optional[T]{
		Value: v,
		Valid: true,
	}
}

func NoOpt[T Typed]() Optional[T] {
	return Optional[T]{}
}
