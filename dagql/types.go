package dagql

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vito/dagql/idproto"
	"google.golang.org/protobuf/proto"
)

type Typed interface {
	Type() *ast.Type
}

type Scalar interface {
	Typed
	idproto.Literate
	New(any) (Scalar, error)
}

type Int struct {
	Value int
}

func NewInt(val int) Int {
	return Int{Value: val}
}

func (Int) New(val any) (Scalar, error) {
	switch x := val.(type) {
	case int:
		return NewInt(x), nil
	case int32:
		return NewInt(int(x)), nil
	case int64:
		return NewInt(int(x)), nil
	case json.Number:
		i, err := x.Int64()
		if err != nil {
			return nil, err
		}
		return NewInt(int(i)), nil
	default:
		return nil, fmt.Errorf("cannot convert %T to Int", x)
	}
}

func (i Int) Literal() *idproto.Literal {
	return &idproto.Literal{
		Value: &idproto.Literal_Int{
			Int: int64(i.Value),
		},
	}
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

type Float struct {
	Value float64
}

func NewFloat(val float64) Float {
	return Float{Value: val}
}

var _ Scalar = Float{}

func (Float) New(val any) (Scalar, error) {
	switch x := val.(type) {
	case float32:
		return NewFloat(float64(x)), nil
	case float64:
		return NewFloat(float64(x)), nil
	case json.Number:
		i, err := x.Float64()
		if err != nil {
			return nil, err
		}
		return NewFloat(i), nil
	default:
		return nil, fmt.Errorf("cannot convert %T to Float", x)
	}
}

func (i Float) Literal() *idproto.Literal {
	return &idproto.Literal{
		Value: &idproto.Literal_Float{
			Float: i.Value,
		},
	}
}

func (Float) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Float",
		NonNull:   true,
	}
}

func (i Float) MarshalJSON() ([]byte, error) {
	return json.Marshal(i.Value)
}

func (i *Float) UnmarshalJSON(p []byte) error {
	var num float64
	if err := json.Unmarshal(p, &num); err != nil {
		return err
	}
	i.Value = num
	return nil
}

type Boolean struct {
	Value bool
}

func NewBoolean(val bool) Boolean {
	return Boolean{Value: val}
}

var _ Typed = Boolean{}

func (Boolean) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Boolean",
		NonNull:   true,
	}
}

var _ Scalar = Boolean{}

func (Boolean) New(val any) (Scalar, error) {
	switch x := val.(type) {
	case bool:
		return NewBoolean(x), nil
	default:
		return nil, fmt.Errorf("cannot convert %T to Boolean", x)
	}
}

func (i Boolean) Literal() *idproto.Literal {
	return idproto.LiteralValue(i.Value)
}

func (i Boolean) MarshalJSON() ([]byte, error) {
	return json.Marshal(i.Value)
}

func (i *Boolean) UnmarshalJSON(p []byte) error {
	var num bool
	if err := json.Unmarshal(p, &num); err != nil {
		return err
	}
	i.Value = num
	return nil
}

type String struct {
	Value string
}

func NewString(val string) String {
	return String{Value: val}
}

var _ Typed = String{}

func (String) Type() *ast.Type {
	return &ast.Type{
		NamedType: "String",
		NonNull:   true,
	}
}

var _ Scalar = String{}

func (String) New(val any) (Scalar, error) {
	switch x := val.(type) {
	case string:
		return NewString(x), nil
	default:
		return nil, fmt.Errorf("cannot convert %T to String", x)
	}
}

func (i String) Literal() *idproto.Literal {
	return idproto.LiteralValue(i.Value)
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

type ID[T Typed] struct {
	*idproto.ID

	expected T
}

var _ Typed = ID[Typed]{}

func (i ID[T]) Type() *ast.Type {
	return &ast.Type{
		NamedType: i.expected.Type().Name() + "ID",
		NonNull:   true,
	}
}

// For parsing string IDs provided in queries.
var _ Scalar = ID[Typed]{}

func (ID[T]) New(val any) (Scalar, error) {
	switch x := val.(type) {
	case *idproto.ID:
		return ID[T]{ID: x}, nil
	case string:
		i := ID[T]{}
		if err := (&i).Decode(x); err != nil {
			return nil, err
		}
		return i, nil
	default:
		return nil, fmt.Errorf("cannot convert %T to Int", x)
	}
}

func (i ID[T]) Literal() *idproto.Literal {
	return &idproto.Literal{
		Value: &idproto.Literal_Id{
			Id: i.ID,
		},
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

// For returning responses.
var _ json.Marshaler = ID[Typed]{}

func (i ID[T]) MarshalJSON() ([]byte, error) {
	enc, err := i.Encode()
	if err != nil {
		return nil, err
	}
	return json.Marshal(enc)
}

// Not actually used, but implemented for completeness.
//
// FromValue is what's used in practice.
var _ json.Unmarshaler = (*ID[Typed])(nil)

func (i *ID[T]) UnmarshalJSON(p []byte) error {
	var str string
	if err := json.Unmarshal(p, &str); err != nil {
		return err
	}
	return i.Decode(str)
}

type Enumerable interface {
	Len() int
	Nth(int) (Typed, error)
}

type Identified[T Typed] struct {
	id    ID[T]
	value T
}

func NewNode[T Typed](id ID[T], val T) Identified[T] {
	return Identified[T]{
		id:    id,
		value: val,
	}
}

var _ Typed = Identified[Typed]{}

func (i Identified[T]) Type() *ast.Type {
	// NB: Identified will always have a Type, but never have a Kind, since we'll
	// always be identifying an Object by definition, and those don't have a
	// Kind.
	return i.value.Type()
}

var _ Node = Identified[Typed]{}

func (i Identified[T]) Value() Typed {
	return i.value
}

func (i Identified[T]) ID() *idproto.ID {
	return i.id.ID
}

type Array[T Typed] []T

var _ Typed = Array[Typed]{}

func (i Array[T]) Type() *ast.Type {
	var t T
	return &ast.Type{
		Elem:    t.Type(),
		NonNull: true,
	}
}

var _ Enumerable = Array[Typed]{}

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

type Optional[T Typed] struct {
	Value T
	Valid bool
}

type Nullable interface {
	Unwrap() (Typed, bool)
}

var _ Typed = Optional[Typed]{}

func (n Optional[T]) Type() *ast.Type {
	nullable := *n.Value.Type()
	nullable.NonNull = false
	return &nullable
}

var _ Nullable = Optional[Typed]{}

func (n Optional[T]) Unwrap() (Typed, bool) {
	return n.Value, n.Valid
}

func (i Optional[T]) MarshalJSON() ([]byte, error) {
	if !i.Valid {
		return json.Marshal(nil)
	}
	return json.Marshal(i.Value)
}

func (i *Optional[T]) UnmarshalJSON(p []byte) error {
	if err := json.Unmarshal(p, &i.Value); err != nil {
		return err
	}
	return nil
}

type Enum struct {
	Enum  *ast.Type
	Value string
}

var _ Typed = Enum{}

func (n Enum) Type() *ast.Type {
	return n.Enum
}

func (i Enum) MarshalJSON() ([]byte, error) {
	return json.Marshal(i.Value)
}

func (i *Enum) UnmarshalJSON(p []byte) error {
	if err := json.Unmarshal(p, &i.Value); err != nil {
		return err
	}
	return nil
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

func LiteralToAST(lit *idproto.Literal) *ast.Value {
	switch x := lit.GetValue().(type) {
	case *idproto.Literal_Id:
		enc, err := ID[Typed]{ID: x.Id}.Encode()
		if err != nil {
			panic(err) // TODO
		}
		return &ast.Value{
			Raw:  enc,
			Kind: ast.StringValue,
		}
	case *idproto.Literal_Null:
		return &ast.Value{
			Raw:  "null",
			Kind: ast.NullValue,
		}
	case *idproto.Literal_Bool:
		return &ast.Value{
			Raw:  strconv.FormatBool(x.Bool),
			Kind: ast.BooleanValue,
		}
	case *idproto.Literal_Enum:
		return &ast.Value{
			Raw:  x.Enum,
			Kind: ast.EnumValue,
		}
	case *idproto.Literal_Int:
		return &ast.Value{
			Raw:  fmt.Sprintf("%d", x.Int),
			Kind: ast.IntValue,
		}
	case *idproto.Literal_Float:
		return &ast.Value{
			Raw:  strconv.FormatFloat(x.Float, 'f', -1, 64),
			Kind: ast.FloatValue,
		}
	case *idproto.Literal_String_:
		return &ast.Value{
			Raw:  x.String_,
			Kind: ast.StringValue,
		}
	case *idproto.Literal_List:
		list := &ast.Value{
			Kind: ast.ListValue,
		}
		for _, val := range x.List.Values {
			list.Children = append(list.Children, &ast.ChildValue{
				Value: LiteralToAST(val),
			})
		}
		return list
	case *idproto.Literal_Object:
		obj := &ast.Value{
			Kind: ast.ObjectValue,
		}
		for _, field := range x.Object.Values {
			obj.Children = append(obj.Children, &ast.ChildValue{
				Name:  field.Name,
				Value: LiteralToAST(field.Value),
			})
		}
		return obj
	default:
		panic(fmt.Sprintf("unsupported literal type %T", x))
	}
}
