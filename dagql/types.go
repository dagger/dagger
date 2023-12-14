package dagql

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vito/dagql/idproto"
)

// Typed is any value that knows its GraphQL type.
type Typed interface {
	// Type returns the GraphQL type of the value.
	Type() *ast.Type
}

// ObjectType represents a GraphQL Object type.
type ObjectType interface {
	// New creates a new instance of the type.
	New(*idproto.ID, Typed) (Object, error)
	// NewID creates an ID annotated with the type.
	NewID(*idproto.ID) Typed
	// Definition returns the GraphQL definition of the type.
	Definition() *ast.Definition
	// FieldDefinition returns the GraphQL definition of the field with the given
	// name, or false if it is not defined.
	FieldDefinition(string) (*ast.FieldDefinition, bool)
}

// ScalarType represents a GraphQL Scalar type.
type ScalarType interface {
	// New converts a value to the Scalar type, if possible.
	New(any) (Scalar, error)
	// Definition returns the GraphQL definition of the type.
	Definition() *ast.Definition
}

// EnumType represents a GraphQL Enum type.
//
// This interface is a little awkward to explain, but it lets you implement
// Enums pretty succinctly by re-using a common underlying Enum implementation,
// passed to this function as a Scalar.
//
// For example:
//
//	type Direction struct {
//		dagql.Scalar
//	}
//
//	var Directions = dagql.NewEnum[Direction]()
//
//	var (
//		DirectionUp    = Directions.Register("UP")
//		DirectionDown  = Directions.Register("DOWN")
//		DirectionLeft  = Directions.Register("LEFT")
//		DirectionRight = Directions.Register("RIGHT")
//		DirectionInert = Directions.Register("INERT")
//	)
type EnumType[T Typed] interface {
	Scalar
	// New populates the Enum with the given Scalar value.
	New(Scalar) T
}

// Object represents an Object in the graph which has an ID and can have
// sub-selections.
type Object interface {
	Typed
	ID() *idproto.ID
	Select(context.Context, Selector) (Typed, error)
}

// Scalar represents a leaf node of the graph, i.e. a simple scalar value that
// cannot have any sub-selections.
type Scalar interface {
	// All Scalars are typed.
	Typed
	// All Scalars are able to be represented as a Literal.
	idproto.Literate
	// All Scalars are able to be represented as JSON.
	json.Marshaler
	// All Scalars have a ScalarClass. This reference is used to initialize
	// default values, among other conveniences.
	ScalarType() ScalarType
}

// Int is a GraphQL Int scalar.
type Int struct {
	Value int
}

func NewInt(val int) Int {
	return Int{Value: val}
}

var _ ScalarType = Int{}

func (Int) ScalarType() ScalarType {
	return Int{}
}

func (Int) Definition() *ast.Definition {
	return &ast.Definition{
		Kind:        ast.Scalar,
		Name:        "Int",
		Description: "The `Int` scalar type represents non-fractional signed whole numeric values. Int can represent values between -(2^31) and 2^31 - 1.",
		BuiltIn:     true,
	}
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

func (i Int) ToLiteral() *idproto.Literal {
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

// Float is a GraphQL Float scalar.
type Float struct {
	Value float64
}

func NewFloat(val float64) Float {
	return Float{Value: val}
}

var _ ScalarType = Float{}

func (Float) Definition() *ast.Definition {
	return &ast.Definition{
		Kind:        ast.Scalar,
		Name:        "Float",
		Description: "The `Float` scalar type represents signed double-precision fractional values as specified by [IEEE 754](http://en.wikipedia.org/wiki/IEEE_floating_point).",
		BuiltIn:     true,
	}
}

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

func (Float) ScalarType() ScalarType {
	return Float{}
}

func (i Float) ToLiteral() *idproto.Literal {
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

// Boolean is a GraphQL Boolean scalar.
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

var _ ScalarType = Boolean{}

func (Boolean) Definition() *ast.Definition {
	return &ast.Definition{
		Kind:        ast.Scalar,
		Name:        "Boolean",
		Description: "The `Boolean` scalar type represents `true` or `false`.",
		BuiltIn:     true,
	}
}

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

func (Boolean) ScalarType() ScalarType {
	return Boolean{}
}

func (b Boolean) ToLiteral() *idproto.Literal {
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

// String is a GraphQL String scalar.
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

var _ ScalarType = String{}

func (String) Definition() *ast.Definition {
	return &ast.Definition{
		Kind:        ast.Scalar,
		Name:        "String",
		Description: "The `String` scalar type represents textual data, represented as UTF-8 character sequences. The String type is most often used by GraphQL to represent free-form human-readable text.",
		BuiltIn:     true,
	}
}

func (String) New(val any) (Scalar, error) {
	switch x := val.(type) {
	case string:
		return NewString(x), nil
	default:
		return nil, fmt.Errorf("cannot convert %T to String", x)
	}
}

var _ Scalar = String{}

func (String) ScalarType() ScalarType {
	return String{}
}

func (i String) ToLiteral() *idproto.Literal {
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

// ID is a type-checked ID scalar.
type ID[T Typed] struct {
	*idproto.ID
	expected T
}

// TypeName returns the name of the type with "ID" appended, e.g. `FooID`.
func (i ID[T]) TypeName() string {
	return i.expected.Type().Name() + "ID"
}

var _ Typed = ID[Typed]{}

// Type returns the GraphQL type of the value.
func (i ID[T]) Type() *ast.Type {
	return &ast.Type{
		NamedType: i.TypeName(),
		NonNull:   true,
	}
}

var _ ScalarType = ID[Typed]{}

// Definition returns the GraphQL definition of the type.
func (i ID[T]) Definition() *ast.Definition {
	return &ast.Definition{
		Kind: ast.Scalar,
		Name: i.TypeName(),
		Description: fmt.Sprintf(
			"The `%s` scalar type represents an identifier for an object of type %s.",
			i.TypeName(),
			i.expected.Type().Name(),
		),
		BuiltIn: true,
	}
}

// New creates a new ID with the given value.
//
// It accepts either an *idproto.ID or a string. The string is expected to be
// the base64-encoded representation of an *idproto.ID.
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

// String returns the ID in ClassID@sha256:... format.
func (i ID[T]) String() string {
	var zero T
	dig, err := i.ID.Digest()
	if err != nil {
		panic(err) // TODO
	}
	return fmt.Sprintf("%s@%s", zero.Type().Name(), dig)
}

// For parsing string IDs provided in queries.
var _ Scalar = ID[Typed]{}

func (i ID[T]) ScalarType() ScalarType {
	return ID[T]{expected: i.expected}
}

func (i ID[T]) ToLiteral() *idproto.Literal {
	return &idproto.Literal{
		Value: &idproto.Literal_Id{
			Id: i.ID,
		},
	}
}

func (i *ID[T]) Decode(str string) error {
	expectedName := i.expected.Type().Name()
	var idp idproto.ID
	if err := idp.Decode(str); err != nil {
		return err
	}
	if idp.Type.NamedType != expectedName {
		return fmt.Errorf("expected %q ID, got %q ID", expectedName, idp.Type)
	}
	i.ID = &idp
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

// Load loads the instance with the given ID from the server.
func (i ID[T]) Load(ctx context.Context, server *Server) (Instance[T], error) {
	val, err := server.Load(ctx, i.ID)
	if err != nil {
		return Instance[T]{}, err
	}
	obj, ok := val.(Instance[T])
	if !ok {
		return Instance[T]{}, fmt.Errorf("load: expected %T, got %T", obj, val)
	}
	return obj, nil
}

// Enumerable is a value that has a length and allows indexing.
type Enumerable interface {
	// Len returns the number of elements in the Enumerable.
	Len() int
	// Nth returns the Nth element of the Enumerable, with 1 representing the
	// first entry.
	Nth(int) (Typed, error)
}

// Array is an array of GraphQL values.
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

// Optional wraps a type and allows it to be null.
//
// This is used for optional arguments and return values.
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

// EnumValues is a list of possible values for an Enum.
type EnumValues[T EnumType[T]] []string

// NewEnum creates a new EnumType with the given possible values.
func NewEnum[T EnumType[T]](vals ...string) *EnumValues[T] {
	return (*EnumValues[T])(&vals)
}

func (e *EnumValues[T]) Definition() *ast.Definition {
	var zero T
	return &ast.Definition{
		Kind: ast.Enum,
		Name: zero.Type().Name(),
		// Description: "TODO",
		EnumValues: e.PossibleValues(),
	}
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
	var enum T
	for _, possible := range *e {
		if val == string(possible) {
			return enum.New(Enum[T]{
				Enum:  e,
				Value: val,
			}), nil
		}
	}
	return enum, fmt.Errorf("invalid enum value %q", val)
}

func (e *EnumValues[T]) Register(val string) T {
	var enum T
	*e = append(*e, val)
	return enum.New(Enum[T]{
		Enum:  e,
		Value: val,
	})
}

func (e *EnumValues[T]) Install(srv *Server) {
	var zero T
	srv.scalars[zero.Type().Name()] = e
}

// Enum is the common underlying implementation for all Enum values.
type Enum[T EnumType[T]] struct {
	Enum  *EnumValues[T]
	Value string
}

// ScalarType returns the underlying EnumValues type.
func (e Enum[T]) ScalarType() ScalarType {
	return e.Enum
}

func (e Enum[T]) ToLiteral() *idproto.Literal {
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

// NoOpt returns an empty Optional value.
func NoOpt[T Typed]() Optional[T] {
	return Optional[T]{}
}

// ToLiteral converts a Typed value to a Literal.
//
// A Scalar value is converted to a Literal. An Object value is converted to
// its ID, which is a Literal.
func ToLiteral(typed Typed) *idproto.Literal {
	switch x := typed.(type) {
	case Scalar:
		return x.ToLiteral()
	case Object:
		return &idproto.Literal{
			Value: &idproto.Literal_Id{
				Id: x.ID(),
			},
		}
	default:
		panic(fmt.Sprintf("cannot convert %T to Literal", x))
	}
}
