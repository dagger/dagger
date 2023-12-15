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

// Type is an object that defines a new GraphQL type.
type Type interface {
	// TypeName returns the name of the type.
	TypeName() string

	// Typically a Type will optionally define either of the two interfaces:
	// Descriptive
	// Definitive
}

// ObjectType represents a GraphQL Object type.
type ObjectType interface {
	Type
	// New creates a new instance of the type.
	New(*idproto.ID, Typed) (Object, error)
	// NewID creates an ID annotated with the type.
	NewID(*idproto.ID) Input
	// Definition returns the GraphQL definition of the type.
	Definition() *ast.Definition
	// FieldDefinition returns the GraphQL definition of the field with the given
	// name, or false if it is not defined.
	FieldDefinition(string) (*ast.FieldDefinition, bool)
	// ParseField parses the given field and returns a Selector.
	ParseField(*ast.Field, map[string]any) (Selector, error)
}

// Object represents an Object in the graph which has an ID and can have
// sub-selections.
type Object interface {
	Typed
	ID() *idproto.ID
	Select(context.Context, Selector) (Typed, error)
}

// ScalarType represents a GraphQL Scalar type.
type ScalarType interface {
	Type
	InputDecoder
}

// Input represents any value which may be passed as an input.
type Input interface {
	// All Inputs are typed.
	Typed
	// All Inputs are able to be represented as a Literal.
	idproto.Literate
	// All Inputs now how to decode new instances of themselves.
	Decoder() InputDecoder
	// In principle all Inputs are able to be represented as JSON, but we don't
	// require the interface to be implemented since basic values like strings
	// (Enums) and arrays (...Arrays) already marshal appropriately.
	// json.Marshaler
}

// InputDecoder is a type that knows how to decode values into Inputs.
type InputDecoder interface {
	// Decode converts a value to the Input type, if possible.
	DecodeInput(any) (Input, error)
}

// Int is a GraphQL Int scalar.
type Int struct {
	Value int
}

func NewInt(val int) Int {
	return Int{Value: val}
}

var _ ScalarType = Int{}

func (Int) TypeName() string {
	return "Int"
}

func (i Int) Definition() *ast.Definition {
	return &ast.Definition{
		Kind:        ast.Scalar,
		Name:        i.TypeName(),
		Description: "The `Int` scalar type represents non-fractional signed whole numeric values. Int can represent values between -(2^31) and 2^31 - 1.",
		BuiltIn:     true,
	}
}

func (Int) DecodeInput(val any) (Input, error) {
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
		return nil, fmt.Errorf("cannot create Int from %T", x)
	}
}

var _ Input = Int{}

func (Int) Decoder() InputDecoder {
	return Int{}
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

func (Float) TypeName() string {
	return "Float"
}

func (f Float) Definition() *ast.Definition {
	return &ast.Definition{
		Kind:        ast.Scalar,
		Name:        f.TypeName(),
		Description: "The `Float` scalar type represents signed double-precision fractional values as specified by [IEEE 754](http://en.wikipedia.org/wiki/IEEE_floating_point).",
		BuiltIn:     true,
	}
}

func (Float) DecodeInput(val any) (Input, error) {
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
		return nil, fmt.Errorf("cannot create Float from %T", x)
	}
}

var _ Input = Float{}

func (Float) Decoder() InputDecoder {
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

func (Boolean) TypeName() string {
	return "Boolean"
}

func (b Boolean) Definition() *ast.Definition {
	return &ast.Definition{
		Kind:        ast.Scalar,
		Name:        b.TypeName(),
		Description: "The `Boolean` scalar type represents `true` or `false`.",
		BuiltIn:     true,
	}
}

func (Boolean) DecodeInput(val any) (Input, error) {
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
		return nil, fmt.Errorf("cannot create Boolean from %T", x)
	}
}

var _ Input = Boolean{}

func (Boolean) Decoder() InputDecoder {
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

func (String) TypeName() string {
	return "String"
}

func (s String) Definition() *ast.Definition {
	return &ast.Definition{
		Kind:        ast.Scalar,
		Name:        s.TypeName(),
		Description: "The `String` scalar type represents textual data, represented as UTF-8 character sequences. The String type is most often used by GraphQL to represent free-form human-readable text.",
		BuiltIn:     true,
	}
}

func (String) DecodeInput(val any) (Input, error) {
	switch x := val.(type) {
	case string:
		return NewString(x), nil
	default:
		return nil, fmt.Errorf("cannot create String from %T", x)
	}
}

var _ Input = String{}

func (String) Decoder() InputDecoder {
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
func (ID[T]) DecodeInput(val any) (Input, error) {
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
		return nil, fmt.Errorf("cannot create Int from %T", x)
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
var _ Input = ID[Typed]{}

func (i ID[T]) Decoder() InputDecoder {
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
type ArrayInput[I Input] []I

func MapArrayInput[T Input, R Typed](opt ArrayInput[T], fn func(T) (R, error)) (Array[R], error) {
	r := make(Array[R], len(opt))
	for i, val := range opt {
		var err error
		r[i], err = fn(val)
		if err != nil {
			return nil, fmt.Errorf("map array[%d]: %w", i, err)
		}
	}
	return r, nil
}

func (a ArrayInput[S]) ToArray() Array[S] {
	return Array[S](a)
}

var _ Typed = ArrayInput[Input]{}

func (a ArrayInput[S]) Type() *ast.Type {
	var elem S
	return &ast.Type{
		Elem:    elem.Type(),
		NonNull: true,
	}
}

var _ Input = ArrayInput[Input]{}

func (a ArrayInput[S]) Decoder() InputDecoder {
	return a
}

var _ InputDecoder = ArrayInput[Input]{}

func (ArrayInput[I]) DecodeInput(val any) (Input, error) {
	var zero I
	decoder := zero.Decoder()
	switch x := val.(type) {
	case []any:
		arr := make(ArrayInput[I], len(x))
		for i, val := range x {
			elem, err := decoder.DecodeInput(val)
			if err != nil {
				return nil, fmt.Errorf("ArrayInput.New[%d]: %w", i, err)
			}
			arr[i] = elem.(I) // TODO sus
		}
		return arr, nil
	default:
		return nil, fmt.Errorf("cannot create Int from %T", x)
	}
}

func (i ArrayInput[S]) ToLiteral() *idproto.Literal {
	list := &idproto.List{}
	for _, elem := range i {
		list.Values = append(list.Values, elem.ToLiteral())
	}
	return &idproto.Literal{
		Value: &idproto.Literal_List{
			List: list,
		},
	}
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

// NullableWrapper is a type that wraps another type.
//
// In practice this is only used for Optional and Nullable. It should be used
// sparingly, since wrapping interfaces explodes very quickly.
type NullableWrapper interface {
	Unwrap() (Typed, bool)
}

// Optional wraps a type and allows it to be null.
//
// This is used for optional arguments and return values.
type Optional[I Input] struct {
	Value I
	Valid bool
}

var _ Input = Optional[Input]{}

func MapOpt[I Input, R Typed](opt Optional[I], fn func(I) (R, error)) (Nullable[R], error) {
	if !opt.Valid {
		return Nullable[R]{}, nil
	}
	r, err := fn(opt.Value)
	if err != nil {
		return Nullable[R]{}, err
	}
	return Nullable[R]{
		Value: r,
		Valid: true,
	}, nil
}

func Opt[I Input](v I) Optional[I] {
	return Optional[I]{
		Value: v,
		Valid: true,
	}
}

// NoOpt returns an empty Optional value.
func NoOpt[I Input]() Optional[I] {
	return Optional[I]{}
}

func (n Optional[I]) ToNullable() Nullable[I] {
	return Nullable[I]{
		Value: n.Value,
		Valid: n.Valid,
	}
}

func (n Optional[I]) Decoder() InputDecoder {
	return n
}

func (i Optional[I]) ToLiteral() *idproto.Literal {
	if !i.Valid {
		return &idproto.Literal{
			Value: &idproto.Literal_Null{
				Null: true,
			},
		}
	}
	return i.Value.ToLiteral()
}

func (i Optional[I]) MarshalJSON() ([]byte, error) {
	if !i.Valid {
		return json.Marshal(nil)
	}
	return json.Marshal(i.Value)
}

var _ Typed = Optional[Input]{}

func (n Optional[I]) Type() *ast.Type {
	nullable := *n.Value.Type()
	nullable.NonNull = false
	return &nullable
}

var _ NullableWrapper = Optional[Input]{}

func (n Optional[I]) DecodeInput(val any) (Input, error) { // TODO this should return Scalar?
	if val == nil {
		return Optional[I]{}, nil
	}
	var zero I
	val, err := zero.Decoder().DecodeInput(val)
	if err != nil {
		return nil, err
	}
	return Optional[I]{
		Value: val.(I), // TODO would be nice to not have to cast
		Valid: true,
	}, nil
}

func (n Optional[S]) Unwrap() (Typed, bool) {
	return n.Value, n.Valid
}

func (i *Optional[S]) UnmarshalJSON(p []byte) error {
	if err := json.Unmarshal(p, &i.Value); err != nil {
		return err
	}
	return nil
}

// Nullable wraps a type and allows it to be null.
//
// This is used for optional arguments and return values.
type Nullable[T Typed] struct {
	Value T
	Valid bool
}

func Null[T Typed]() Nullable[T] {
	return Nullable[T]{}
}

func NonNull[T Typed](val T) Nullable[T] {
	return Nullable[T]{
		Value: val,
		Valid: true,
	}
}

var _ Typed = Nullable[Typed]{}

func (n Nullable[T]) Type() *ast.Type {
	nullable := *n.Value.Type()
	nullable.NonNull = false
	return &nullable
}

var _ NullableWrapper = Nullable[Typed]{}

func (n Nullable[T]) Unwrap() (Typed, bool) {
	return n.Value, n.Valid
}

func (i Nullable[T]) MarshalJSON() ([]byte, error) {
	if !i.Valid {
		return json.Marshal(nil)
	}
	return json.Marshal(i.Value)
}

func (i *Nullable[T]) UnmarshalJSON(p []byte) error {
	if err := json.Unmarshal(p, &i.Value); err != nil {
		return err
	}
	return nil
}

type EnumValue interface {
	Input
	~string
}

// EnumValues is a list of possible values for an Enum.
type EnumValues[T EnumValue] []T

// NewEnum creates a new EnumType with the given possible values.
func NewEnum[T EnumValue](vals ...T) *EnumValues[T] {
	return (*EnumValues[T])(&vals)
}

func (e *EnumValues[T]) TypeName() string {
	var zero T
	return zero.Type().Name()
}

func (e *EnumValues[T]) Definition() *ast.Definition {
	return &ast.Definition{
		Kind: ast.Enum,
		Name: e.TypeName(),
		// Description: "TODO",
		EnumValues: e.PossibleValues(),
	}
}

func (e *EnumValues[T]) DecodeInput(val any) (Input, error) {
	switch x := val.(type) {
	case string:
		return e.Lookup(x)
	default:
		return nil, fmt.Errorf("cannot create Enum from %T", x)
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

func (e *EnumValues[T]) Literal(val T) *idproto.Literal {
	return &idproto.Literal{
		Value: &idproto.Literal_Enum{
			Enum: string(val),
		},
	}
}

func (e *EnumValues[T]) Lookup(val string) (T, error) {
	var enum T
	for _, possible := range *e {
		if val == string(possible) {
			return possible, nil
		}
	}
	return enum, fmt.Errorf("invalid enum value %q", val)
}

func (e *EnumValues[T]) Register(val T) T {
	*e = append(*e, val)
	return val
}

func (e *EnumValues[T]) Install(srv *Server) {
	var zero T
	srv.scalars[zero.Type().Name()] = e
}
