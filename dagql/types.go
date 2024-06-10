package dagql

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
	"golang.org/x/exp/constraints"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/slog"
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
	// Typed returns a Typed value whose Type refers to the object type.
	Typed() Typed
	// IDType returns the scalar type for the object's IDs.
	IDType() (IDType, bool)
	// New creates a new instance of the type.
	New(*call.ID, Typed) (Object, error)
	// ParseField parses the given field and returns a Selector and an expected
	// return type.
	ParseField(context.Context, *ast.Field, map[string]any) (Selector, *ast.Type, error)
	// Extend registers an additional field onto the type.
	//
	// Unlike natively added fields, the extended func is limited to the external
	// Object interface.
	Extend(FieldSpec, FieldFunc)
}

type IDType interface {
	Input
	IDable
	ScalarType
}

// FieldFunc is a function that implements a field on an object while limited
// to the object's external interface.
type FieldFunc func(context.Context, Object, map[string]Input) (Typed, error)

type IDable interface {
	// ID returns the ID of the value.
	ID() *call.ID
}

// Object represents an Object in the graph which has an ID and can have
// sub-selections.
type Object interface {
	Typed
	IDable
	// ObjectType returns the type of the object.
	ObjectType() ObjectType
	// IDFor returns the ID representing the return value of the given field.
	IDFor(context.Context, Selector) (*call.ID, error)
	// Select evaluates the selected field and returns the result.
	//
	// The returned value is the raw Typed value returned from the field; it must
	// be instantiated with a class for further selection.
	//
	// Any Nullable values are automatically unwrapped.
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
	call.Literate
	// All Inputs now how to decode new instances of themselves.
	Decoder() InputDecoder

	// In principle all Inputs are able to be represented as JSON, but we don't
	// require the interface to be implemented since builtins like strings
	// (Enums) and slices (Arrays) already marshal appropriately.

	// json.Marshaler
}

// Setter allows a type to populate fields of a struct.
//
// This is how builtins are supported.
type Setter interface {
	SetField(reflect.Value) error
}

// InputDecoder is a type that knows how to decode values into Inputs.
type InputDecoder interface {
	// Decode converts a value to the Input type, if possible.
	DecodeInput(any) (Input, error)
}

// Int is a GraphQL Int scalar.
type Int int64

func NewInt[T constraints.Integer](val T) Int {
	return Int(val)
}

var _ ScalarType = Int(0)

func (Int) TypeName() string {
	return "Int"
}

func (i Int) TypeDefinition() *ast.Definition {
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
		return NewInt(x), nil
	case int64:
		return NewInt(x), nil
	case json.Number:
		i, err := x.Int64()
		if err != nil {
			return nil, err
		}
		return NewInt(i), nil
	case string: // default struct tags
		i, err := strconv.ParseInt(x, 0, 64)
		if err != nil {
			return nil, err
		}
		return NewInt(i), nil
	default:
		return nil, fmt.Errorf("cannot create Int from %T", x)
	}
}

var _ Input = Int(0)

func (Int) Decoder() InputDecoder {
	return Int(0)
}

func (i Int) Int() int {
	return int(i)
}

func (i Int) Int64() int64 {
	return int64(i)
}

func (i Int) ToLiteral() call.Literal {
	return call.NewLiteralInt(i.Int64())
}

func (Int) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Int",
		NonNull:   true,
	}
}

func (i Int) MarshalJSON() ([]byte, error) {
	return json.Marshal(i.Int())
}

func (i *Int) UnmarshalJSON(p []byte) error {
	var num int
	if err := json.Unmarshal(p, &num); err != nil {
		return err
	}
	*i = Int(num)
	return nil
}

var _ Setter = ID[Typed]{}

func (i Int) SetField(v reflect.Value) error {
	switch v.Interface().(type) {
	case int, int64:
		v.SetInt(i.Int64())
		return nil
	default:
		return fmt.Errorf("cannot set field of type %T with %T", v.Interface(), i)
	}
}

// Float is a GraphQL Float scalar.
type Float float64

func NewFloat(val float64) Float {
	return Float(val)
}

var _ ScalarType = Float(0)

func (Float) TypeName() string {
	return "Float"
}

func (f Float) TypeDefinition() *ast.Definition {
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
		return NewFloat(x), nil
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

var _ Input = Float(0)

func (Float) Decoder() InputDecoder {
	return Float(0)
}

func (f Float) ToLiteral() call.Literal {
	return call.NewLiteralFloat(f.Float64())
}

func (f Float) Float64() float64 {
	return float64(f)
}

func (Float) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Float",
		NonNull:   true,
	}
}

func (f Float) MarshalJSON() ([]byte, error) {
	return json.Marshal(f.Float64())
}

func (f *Float) UnmarshalJSON(p []byte) error {
	var num float64
	if err := json.Unmarshal(p, &num); err != nil {
		return err
	}
	*f = Float(num)
	return nil
}

var _ Setter = Float(0)

func (f Float) SetField(v reflect.Value) error {
	switch x := v.Interface().(type) {
	case float64:
		v.SetFloat(f.Float64())
		_ = x
		return nil
	default:
		return fmt.Errorf("cannot set field of type %T with %T", v.Interface(), f)
	}
}

// Boolean is a GraphQL Boolean scalar.
type Boolean bool

func NewBoolean(val bool) Boolean {
	return Boolean(val)
}

var _ Typed = Boolean(false)

func (Boolean) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Boolean",
		NonNull:   true,
	}
}

var _ ScalarType = Boolean(false)

func (Boolean) TypeName() string {
	return "Boolean"
}

func (b Boolean) TypeDefinition() *ast.Definition {
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

var _ Input = Boolean(false)

func (Boolean) Decoder() InputDecoder {
	return Boolean(false)
}

func (b Boolean) ToLiteral() call.Literal {
	return call.NewLiteralBool(b.Bool())
}

func (b Boolean) Bool() bool {
	return bool(b)
}

func (b Boolean) MarshalJSON() ([]byte, error) {
	return json.Marshal(b.Bool())
}

func (b *Boolean) UnmarshalJSON(p []byte) error {
	var val bool
	if err := json.Unmarshal(p, &val); err != nil {
		return err
	}
	*b = Boolean(val)
	return nil
}

var _ Setter = Boolean(false)

func (b Boolean) SetField(v reflect.Value) error {
	switch v.Interface().(type) {
	case bool:
		v.SetBool(b.Bool())
		return nil
	default:
		return fmt.Errorf("cannot set field of type %T with %T", v.Interface(), b)
	}
}

// String is a GraphQL String scalar.
type String string

func NewString(val string) String {
	return String(val)
}

var _ Typed = String("")

func (String) Type() *ast.Type {
	return &ast.Type{
		NamedType: "String",
		NonNull:   true,
	}
}

var _ ScalarType = String("")

func (String) TypeName() string {
	return "String"
}

func (s String) TypeDefinition() *ast.Definition {
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

var _ Input = String("")

func (String) Decoder() InputDecoder {
	return String("")
}

func (s String) ToLiteral() call.Literal {
	return call.NewLiteralString(string(s))
}

func (s String) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.String())
}

func (s *String) UnmarshalJSON(p []byte) error {
	var str string
	if err := json.Unmarshal(p, &str); err != nil {
		return err
	}
	*s = String(str)
	return nil
}

func (s String) String() string {
	return string(s)
}

var _ Setter = String("")

func (s String) SetField(v reflect.Value) error {
	switch v.Interface().(type) {
	case string:
		v.SetString(s.String())
		return nil
	default:
		return fmt.Errorf("cannot set field of type %T with %T", v.Interface(), s)
	}
}

type ScalarValue interface {
	ScalarType
	Input
}

// Scalar is a GraphQL scalar.
type Scalar[T ScalarValue] struct {
	Name  string
	Value T
}

func NewScalar[T ScalarValue](name string, val T) Scalar[T] {
	return Scalar[T]{name, val}
}

var _ Typed = Scalar[ScalarValue]{}

func (s Scalar[T]) Type() *ast.Type {
	return &ast.Type{
		NamedType: s.Name,
		NonNull:   true,
	}
}

var _ ScalarValue = Scalar[ScalarValue]{}

func (s Scalar[T]) TypeName() string {
	return s.Name
}

func (s Scalar[T]) TypeDefinition() *ast.Definition {
	def := &ast.Definition{
		Kind: ast.Scalar,
		Name: s.TypeName(),
	}
	var val T
	if isType, ok := any(val).(Descriptive); ok {
		def.Description = isType.TypeDescription()
	}
	return def
}

func (s Scalar[T]) DecodeInput(val any) (Input, error) {
	var empty T
	input, err := empty.DecodeInput(val)
	if err != nil {
		return nil, err
	}
	return NewScalar[T](s.Name, input.(T)), nil
}

var _ Input = Scalar[ScalarValue]{}

func (s Scalar[T]) Decoder() InputDecoder {
	return s
}

func (s Scalar[T]) ToLiteral() call.Literal {
	return s.Value.ToLiteral()
}

func (s Scalar[T]) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.Value)
}

func (s *Scalar[T]) UnmarshalJSON(p []byte) error {
	return json.Unmarshal(p, &s.Value)
}

// ID is a type-checked ID scalar.
type ID[T Typed] struct {
	id    *call.ID
	inner T
}

func NewID[T Typed](id *call.ID) ID[T] {
	return ID[T]{
		id: id,
	}
}

func NewDynamicID[T Typed](id *call.ID, typed T) ID[T] {
	return ID[T]{
		id:    id,
		inner: typed,
	}
}

func IDTypeNameFor(t Typed) string {
	return t.Type().Name() + "ID"
}

// TypeName returns the name of the type with "ID" appended, e.g. `FooID`.
func (i ID[T]) TypeName() string {
	return IDTypeNameFor(i.inner)
}

var _ Typed = ID[Typed]{}

// Type returns the GraphQL type of the value.
func (i ID[T]) Type() *ast.Type {
	return &ast.Type{
		NamedType: i.TypeName(),
		NonNull:   true,
	}
}

var _ IDable = ID[Typed]{}

// ID returns the ID of the value.
func (i ID[T]) ID() *call.ID {
	return i.id
}

var _ ScalarType = ID[Typed]{}

// TypeDefinition returns the GraphQL definition of the type.
func (i ID[T]) TypeDefinition() *ast.Definition {
	return &ast.Definition{
		Kind: ast.Scalar,
		Name: i.TypeName(),
		Description: fmt.Sprintf(
			"The `%s` scalar type represents an identifier for an object of type %s.",
			i.TypeName(),
			i.inner.Type().Name(),
		),
		BuiltIn: true,
	}
}

// New creates a new ID with the given value.
//
// It accepts either an *call.ID or a string. The string is expected to be
// the base64-encoded representation of an *call.ID.
func (i ID[T]) DecodeInput(val any) (Input, error) {
	switch x := val.(type) {
	case *call.ID:
		return ID[T]{id: x, inner: i.inner}, nil
	case string:
		if err := (&i).Decode(x); err != nil {
			return nil, err
		}
		return i, nil
	default:
		return nil, fmt.Errorf("cannot create ID[%T] from %T: %#v", i.inner, x, x)
	}
}

// String returns the ID in ClassID@sha256:... format.
func (i ID[T]) String() string {
	return fmt.Sprintf("%s@%s", i.inner.Type().Name(), i.id.Digest())
}

var _ Setter = ID[Typed]{}

func (i ID[T]) SetField(v reflect.Value) error {
	switch v.Interface().(type) {
	case *call.ID:
		v.Set(reflect.ValueOf(i.ID))
		return nil
	default:
		return fmt.Errorf("cannot set field of type %T with %T", v.Interface(), i)
	}
}

// For parsing string IDs provided in queries.
var _ Input = ID[Typed]{}

func (i ID[T]) Decoder() InputDecoder {
	return ID[T]{inner: i.inner}
}

func (i ID[T]) ToLiteral() call.Literal {
	return call.NewLiteralID(i.id)
}

func (i ID[T]) Encode() (string, error) {
	return i.id.Encode()
}

func (i ID[T]) Display() string {
	return i.id.Display()
}

func (i *ID[T]) Decode(str string) error {
	if str == "" {
		return fmt.Errorf("cannot decode empty string as ID")
	}
	expectedName := i.inner.Type().Name()
	var idp call.ID
	if err := idp.Decode(str); err != nil {
		return err
	}
	if idp.Type() == nil {
		return fmt.Errorf("expected %q ID, got untyped ID", expectedName)
	}
	if idp.Type().NamedType() != expectedName {
		return fmt.Errorf("expected %q ID, got %s ID", expectedName, idp.Type().ToAST())
	}
	i.id = &idp
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
	val, err := server.Load(ctx, i.id)
	if err != nil {
		return Instance[T]{}, fmt.Errorf("load %s: %w", i.id.Display(), err)
	}
	obj, ok := val.(Instance[T])
	if !ok {
		return Instance[T]{}, fmt.Errorf("load %s: expected %T, got %T", i.id.Display(), obj, val)
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

func (a ArrayInput[I]) DecodeInput(val any) (Input, error) {
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
	case string: // default
		var vals []any
		dec := json.NewDecoder(strings.NewReader(x))
		dec.UseNumber()
		if err := dec.Decode(&vals); err != nil {
			return nil, fmt.Errorf("decode %q: %w", x, err)
		}
		return a.DecodeInput(vals)
	default:
		return nil, fmt.Errorf("cannot create ArrayInput from %T", x)
	}
}

func (i ArrayInput[S]) ToLiteral() call.Literal {
	lits := make([]call.Literal, 0, len(i))
	for _, elem := range i {
		lits = append(lits, elem.ToLiteral())
	}
	return call.NewLiteralList(lits...)
}

var _ Setter = ArrayInput[Input]{}

func (d ArrayInput[I]) SetField(val reflect.Value) error {
	if val.Kind() != reflect.Slice {
		return fmt.Errorf("expected slice, got %v", val.Kind())
	}
	val.Set(reflect.MakeSlice(val.Type(), len(d), len(d)))
	for i, elem := range d {
		if err := assign(val.Index(i), elem); err != nil {
			return err
		}
	}
	return nil
}

// Array is an array of GraphQL values.
type Array[T Typed] []T

// ToArray creates a new Array by applying the given function to each element
// of the given slice.
func ToArray[A any, T Typed](fn func(A) T, elems ...A) Array[T] {
	arr := make(Array[T], len(elems))
	for i, elem := range elems {
		arr[i] = fn(elem)
	}
	return arr
}

func NewStringArray(elems ...string) Array[String] {
	return ToArray(NewString, elems...)
}

func NewBoolArray(elems ...bool) Array[Boolean] {
	return ToArray(NewBoolean, elems...)
}

func NewIntArray(elems ...int) Array[Int] {
	return ToArray(NewInt, elems...)
}

func NewFloatArray(elems ...float64) Array[Float] {
	return ToArray(NewFloat, elems...)
}

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

type EnumValue interface {
	Input
	~string
}
type DynamicEnumValue struct {
	parent *DynamicEnumValues
	value string
}

func NewDynamicEnumValue(parent *DynamicEnumValues, value string) *DynamicEnumValue {
	return &DynamicEnumValue{parent, value}
}

func (e *DynamicEnumValue) Type() *ast.Type {
	return e.parent.Type()
}

func (e *DynamicEnumValue) TypeDescription() string {
	if isType, ok := any(e.parent).(Descriptive); ok {
		return isType.TypeDescription()
	}

	return ""
}

func (e *DynamicEnumValue) Decoder() InputDecoder {
	return e.parent
}

func (e *DynamicEnumValue) ToLiteral() call.Literal {
	return e.parent.Literal(e.value)
}

func (e *DynamicEnumValue) Value() string {
	return e.value
}

type DynamicEnumValues struct {
	t Typed
	values []string
	descriptions []string
}

func NewDynamicEnum(t Typed) *DynamicEnumValues {
	return &DynamicEnumValues{t: t}
}

func (e *DynamicEnumValues) Type() *ast.Type {
	return e.t.Type()
}

func (e *DynamicEnumValues) TypeName() string {
	return e.Type().Name()
}

func (e *DynamicEnumValues) Register(val string, desc string) *DynamicEnumValues {
	e.values = append(e.values, val)
	e.descriptions = append(e.descriptions, desc)

	return e
}

func (e *DynamicEnumValues) TypeDefinition() *ast.Definition {
	def := &ast.Definition{
		Kind: ast.Enum,
		Name: e.TypeName(),
		EnumValues: e.PossibleValues(),
	}

	if isType, ok := any(e.t).(Descriptive); ok {
		def.Description = isType.TypeDescription()
	}

	return def
}

func (e *DynamicEnumValues) Decoder() InputDecoder {
	return e
}

func (e *DynamicEnumValues) DecodeInput(val any) (Input, error) {
	switch x := val.(type) {
	case string:
		return e.Lookup(x)
	case Scalar[String]:
		return e.Lookup(string(x.Value))
	default:
		return nil, fmt.Errorf("cannot create Enum from %T", x)
	}
}

func (e *DynamicEnumValues) Literal(val string) call.Literal {
	return call.NewLiteralEnum(val)
}

func (e *DynamicEnumValues) PossibleValues() ast.EnumValueList {
	var values ast.EnumValueList
	for i, val := range e.values {
		values = append(values, &ast.EnumValueDefinition{
			Name:        val,
			Description: e.descriptions[i],
		})
	}
	return values
}

func (e *DynamicEnumValues) ToLiteral() call.Literal {
	return call.NewLiteralEnum("")
}

func (e *DynamicEnumValues) Lookup(val string) (*DynamicEnumValue, error) {
	slog.Error("DynamicEnumValues.Lookup", "val", val, "e.values", e.values)

	// Don't verify the enum if values are not loaded yet.
	if e.values == nil {
		slog.Error("DynamicEnumValues.Lookup: values not loaded yet", "val", val)
		return NewDynamicEnumValue(e, val), nil
	}

	for _, possible := range e.values {
		if val == possible {
			return NewDynamicEnumValue(e, possible), nil
		}
	}

	return nil, fmt.Errorf("invalid enum value %q", val)
}

func (e *DynamicEnumValues) Install(srv *Server) {
	slog.Error("DynamicEnumValues.Install", "e", e.TypeName(), "values", e.values)

	srv.scalars[e.Type().Name()] = e
}

// EnumValues is a list of possible values for an Enum.
type EnumValues[T EnumValue] struct {
	values       []T
	descriptions []string
}

// NewEnum creates a new EnumType with the given possible values.
func NewEnum[T EnumValue](vals ...T) *EnumValues[T] {
	return &EnumValues[T]{
		values:       vals,
		descriptions: make([]string, len(vals)),
	}
}

func (e *EnumValues[T]) Type() *ast.Type {
	var zero T
	return zero.Type()
}

func (e *EnumValues[T]) TypeName() string {
	return e.Type().Name()
}

func (e *EnumValues[T]) TypeDefinition() *ast.Definition {
	def := &ast.Definition{
		Kind:       ast.Enum,
		Name:       e.TypeName(),
		EnumValues: e.PossibleValues(),
	}
	var val T
	if isType, ok := any(val).(Descriptive); ok {
		def.Description = isType.TypeDescription()
	}
	return def
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
	for i, val := range e.values {
		values = append(values, &ast.EnumValueDefinition{
			Name:        string(val),
			Description: e.descriptions[i],
		})
	}

	return values
}

func (e *EnumValues[T]) Literal(val T) call.Literal {
	return call.NewLiteralEnum(string(val))
}

func (e *EnumValues[T]) Lookup(val string) (T, error) {
	var enum T
	for _, possible := range e.values {
		if val == string(possible) {
			return possible, nil
		}
	}
	return enum, fmt.Errorf("invalid enum value %q", val)
}

func (e *EnumValues[T]) Register(val T, desc ...string) T {
	e.values = append(e.values, val)
	e.descriptions = append(e.descriptions, FormatDescription(desc...))
	return val
}

func (e *EnumValues[T]) Install(srv *Server) {
	var zero T
	srv.scalars[zero.Type().Name()] = e
}

func MustInputSpec(val Type) InputObjectSpec {
	spec := InputObjectSpec{
		Name: val.TypeName(),
	}
	if desc, ok := val.(Descriptive); ok {
		spec.Description = desc.TypeDescription()
	}
	inputs, err := inputSpecsForType(val, true)
	if err != nil {
		panic(fmt.Errorf("input specs for %T: %w", val, err))
	}
	spec.Fields = inputs
	return spec
}

type InputObjectSpec struct {
	Name        string
	Description string
	Fields      InputSpecs
}

func (spec InputObjectSpec) Install(srv *Server) {
	srv.InstallTypeDef(spec)
}

func (spec InputObjectSpec) Type() *ast.Type {
	return &ast.Type{
		NamedType: spec.Name,
		NonNull:   true,
	}
}

func (spec InputObjectSpec) TypeName() string {
	return spec.Name
}

func (spec InputObjectSpec) TypeDefinition() *ast.Definition {
	return &ast.Definition{
		Kind:        ast.InputObject,
		Name:        spec.Name,
		Description: spec.Description,
		Fields:      spec.Fields.FieldDefinitions(),
	}
}
