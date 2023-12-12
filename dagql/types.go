package dagql

import (
	"context"
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

type ObjectClass interface {
	ID(*idproto.ID) Typed
	New(*idproto.ID, Typed) (Selectable, error)
}

type ScalarClass interface {
	New(any) (Scalar, error)
}

type Selectable interface {
	Node
	Select(context.Context, Selector) (Typed, error)
}

// Per the GraphQL spec, a Node always has an ID.
type Node interface {
	Typed
	ID() *idproto.ID
	Value() Typed
}

type Scalar interface {
	// All Scalars are typed.
	Typed
	// All Scalars are able to be represented as a Literal.
	idproto.Literate
	// All Scalars are able to be represented as JSON.
	json.Marshaler
	// All Scalars have a ScalarClass. This reference is used to initialize
	// default values, among other conveniences.
	Class() ScalarClass
}

type Int struct {
	Value int
}

func NewInt(val int) Int {
	return Int{Value: val}
}

var _ ScalarClass = Int{}

func (Int) Class() ScalarClass {
	return Int{}
}

var _ Scalar = Int{}

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
	case string: // default struct tags
		i, err := strconv.Atoi(x)
		if err != nil {
			return nil, err
		}
		return NewInt(i), nil
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

var _ ScalarClass = Float{}

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
	case string: // default struct tags
		i, err := strconv.ParseFloat(x, 64)
		if err != nil {
			return nil, err
		}
		return NewFloat(i), nil
	default:
		return nil, fmt.Errorf("cannot convert %T to Float", x)
	}
}

var _ Scalar = Float{}

func (Float) Class() ScalarClass {
	return Float{}
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

var _ ScalarClass = Boolean{}

func (Boolean) New(val any) (Scalar, error) {
	switch x := val.(type) {
	case bool:
		return NewBoolean(x), nil
	case string: // from default
		b, err := strconv.ParseBool(x)
		if err != nil {
			return nil, err
		}
		return NewBoolean(b), nil
	default:
		return nil, fmt.Errorf("cannot convert %T to Boolean", x)
	}
}

var _ Scalar = Boolean{}

func (Boolean) Class() ScalarClass {
	return Boolean{}
}

func (b Boolean) Literal() *idproto.Literal {
	return idproto.LiteralValue(b.Value)
}

func (b Boolean) MarshalJSON() ([]byte, error) {
	return json.Marshal(b.Value)
}

func (b *Boolean) UnmarshalJSON(p []byte) error {
	var num bool
	if err := json.Unmarshal(p, &num); err != nil {
		return err
	}
	b.Value = num
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

var _ ScalarClass = String{}

func (String) New(val any) (Scalar, error) {
	switch x := val.(type) {
	case string:
		return NewString(x), nil
	default:
		return nil, fmt.Errorf("cannot convert %T to String", x)
	}
}

var _ Scalar = String{}

func (String) Class() ScalarClass {
	return String{}
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

var _ ScalarClass = ID[Typed]{}

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

// For parsing string IDs provided in queries.
var _ Scalar = ID[Typed]{}

func (i ID[T]) Class() ScalarClass {
	return ID[T]{expected: i.expected}
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

func (i ID[T]) Load(ctx context.Context, server *Server) (Identified[T], error) {
	// TODO check cache
	val, err := server.Load(ctx, i.ID)
	if err != nil {
		return Identified[T]{}, err
	}
	if ided, ok := val.(Identified[T]); ok {
		// TODO so far I only ran into this by loading an ID that itself calls
		// loadFooFromID, which is mostly a historical artifact (it used to return
		// an ID of its own making instead of the ID given).
		//
		// but there are most likely real world scenarios where this could happen,
		// so we should handle it and have a test
		return ided, nil
	}
	obj, ok := val.(T)
	if !ok {
		return Identified[T]{}, fmt.Errorf("load: expected %T, got %T", obj, val)
	}
	return NewNode(i, obj), nil
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

type EnumSetter[T Typed] interface {
	Scalar
	As(Scalar) T
}

type EnumValues[T EnumSetter[T]] []string

func NewEnum[T EnumSetter[T]](vals ...string) *EnumValues[T] {
	return (*EnumValues[T])(&vals)
}

func (e *EnumValues[T]) New(val any) (Scalar, error) {
	switch x := val.(type) {
	case string:
		return e.Lookup(x)
	default:
		return nil, fmt.Errorf("cannot convert %T to Enum", x)
	}
}

func (e *EnumValues[T]) PossibleValues() ast.EnumValueList {
	var values ast.EnumValueList
	for _, val := range *e {
		values = append(values, &ast.EnumValueDefinition{
			Name: string(val),
		})
	}
	return values
}

func (e *EnumValues[T]) Lookup(val string) (T, error) {
	var zero T
	for _, possible := range *e {
		if val == string(possible) {
			return zero.As(Enum[T]{
				Enum:  e,
				Value: val,
			}), nil
		}
	}
	return zero, fmt.Errorf("invalid enum value %q", val)
}

func (e *EnumValues[T]) Register(val string) T {
	*e = append(*e, val)
	var zero T
	return zero.As(Enum[T]{
		Enum:  e,
		Value: val,
	})
}

func (e *EnumValues[T]) Install(srv *Server) *ast.Definition {
	var zero T
	def := &ast.Definition{
		Kind:       ast.Enum,
		Name:       zero.Type().Name(),
		EnumValues: e.PossibleValues(),
	}
	srv.schema.AddTypes(def)
	srv.scalars[zero.Type().Name()] = e
	return def
}

type Enum[T EnumSetter[T]] struct {
	Enum  *EnumValues[T]
	Value string
}

func (e Enum[T]) Class() ScalarClass {
	return e.Enum
}

func (e Enum[T]) New(val any) (Scalar, error) {
	return e.Enum.New(val)
}

func (e Enum[T]) Literal() *idproto.Literal {
	return &idproto.Literal{
		Value: &idproto.Literal_Enum{
			Enum: e.Value,
		},
	}
}

func (e Enum[T]) MarshalJSON() ([]byte, error) {
	return json.Marshal(e.Value)
}

func (e Enum[T]) Type() *ast.Type {
	var zero T
	return zero.Type()
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
