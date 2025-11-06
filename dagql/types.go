package dagql

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
	"golang.org/x/exp/constraints"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/cache"
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
	New(val AnyResult) (AnyObjectResult, error)
	// ParseField parses the given field and returns a Selector and an expected
	// return type.
	ParseField(ctx context.Context, view call.View, astField *ast.Field, vars map[string]any) (Selector, *ast.Type, error)
	// Extend registers an additional field onto the type.
	//
	// Unlike natively added fields, the extended func is limited to the external
	// Object interface.
	// cacheConfigFunc is optional, if not set the default dagql ID cache key will be used.
	Extend(spec FieldSpec, fun FieldFunc)
	// FieldSpec looks up a field spec by name.
	FieldSpec(name string, view call.View) (FieldSpec, bool)
}

type IDType interface {
	Input
	IDable
	ScalarType
}

// FieldFunc is a function that implements a field on an object while limited
// to the object's external interface.
type FieldFunc func(context.Context, AnyResult, map[string]Input) (AnyResult, error)

type IDable interface {
	// ID returns the ID of the value.
	ID() *call.ID
}

// AnyResult is a Typed value wrapped with an ID constructor. The wrapped value may
// be any graphql type, including scalars, objects, arrays, etc.
// It's a Result but as an interface and without any type params, allowing it
// to be passed around without knowing the concrete type at compile-time.
type AnyResult interface {
	Typed
	Wrapper
	IDable
	PostCallable
	Setter

	// DerefValue returns an AnyResult when the wrapped value is Derefable and
	// has a value set. If the value is not derefable, it returns itself.
	DerefValue() (AnyResult, bool)

	// NthValue returns the Nth value of the wrapped value when the wrapped value
	// is an Enumerable. If the wrapped value is not Enumerable, it returns an error.
	NthValue(int) (AnyResult, error)

	// WithPostCall returns a new AnyResult with the given post-call function attached to it.
	WithPostCall(fn cache.PostCallFunc) AnyResult

	// IsSafeToPersistCache returns whether it's safe to persist this result in the cache.
	IsSafeToPersistCache() bool

	// WithSafeToPersistCache returns a new AnyResult with the given safe-to-persist-cache flag.
	WithSafeToPersistCache(safe bool) AnyResult
}

// AnyObjectResult is an AnyResult that wraps a selectable value (i.e. a graph object)
type AnyObjectResult interface {
	AnyResult

	// ObjectType returns the type of the object.
	ObjectType() ObjectType

	// Call evaluates the field selected by the given ID and returns the result.
	//
	// The returned value is the raw Typed value returned from the field; it must
	// be instantiated with a class for further selection.
	//
	// Any Nullable values are automatically unwrapped.
	Call(context.Context, *Server, *call.ID) (AnyResult, error)

	// Select evaluates the field selected by the given selector and returns the result.
	//
	// The returned value is the raw Typed value returned from the field; it must
	// be instantiated with a class for further selection.
	//
	// Any Nullable values are automatically unwrapped.
	Select(context.Context, *Server, Selector) (AnyResult, error)
}

// InterfaceValue is a value that wraps some underlying object with a interface to that object's API. This type exists to support unwrapping it and getting the underlying object.
type InterfaceValue interface {
	// UnderlyingObject returns the underlying object of the InterfaceValue
	UnderlyingObject() (Typed, error)
}

// PostCallable is a type that has a callback attached that needs to always run before returned to a caller
// whether or not the type is being returned from cache or not
type PostCallable interface {
	// Return the postcall func (or nil if not set)
	GetPostCall() cache.PostCallFunc
}

// A type that has a callback attached that needs to always run when the result is removed
// from the cache
type OnReleaser interface {
	OnRelease(context.Context) error
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

// Wrapper is an interface for types that wrap another type.
type Wrapper interface {
	Unwrap() Typed
}

// UnwrapAs attempts casting val to T, unwrapping as necessary.
//
// NOTE: the order of operations is important here - it's important to first
// check compatibility with T before unwrapping, since sometimes T also
// implements Wrapper.
func UnwrapAs[T any](val any) (T, bool) {
	t, ok := val.(T)
	if ok {
		return t, true
	}
	if wrapper, ok := val.(Wrapper); ok {
		return UnwrapAs[T](wrapper.Unwrap())
	}
	return t, false
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

func (i Int) TypeDefinition(view call.View) *ast.Definition {
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
	case float64:
		if math.IsInf(x, 0) || math.IsNaN(x) {
			return nil, fmt.Errorf("cannot create Int from %v", x)
		}
		i := int64(x)
		if float64(i) != x {
			return nil, fmt.Errorf("cannot create Int from %v", x)
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

var _ Setter = Int(0)

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

func NewFloat[T constraints.Float](val T) Float {
	return Float(val)
}

var _ ScalarType = Float(0)

func (Float) TypeName() string {
	return "Float"
}

func (f Float) TypeDefinition(view call.View) *ast.Definition {
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

func (b Boolean) TypeDefinition(view call.View) *ast.Definition {
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

func (s String) TypeDefinition(view call.View) *ast.Definition {
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

type SerializedString[T any] struct {
	Self T
}

func NewSerializedString[T any](val T) SerializedString[T] {
	return SerializedString[T]{
		Self: val,
	}
}

var _ Typed = SerializedString[any]{}

func (SerializedString[T]) Type() *ast.Type {
	return &ast.Type{
		NamedType: "String",
		NonNull:   true,
	}
}

var _ InputDecoder = SerializedString[any]{}

func (SerializedString[T]) DecodeInput(val any) (Input, error) {
	switch x := val.(type) {
	case string:
		var v T
		err := json.Unmarshal([]byte(x), &v)
		if err != nil {
			return nil, err
		}
		return NewSerializedString(v), nil
	default:
		return nil, fmt.Errorf("cannot create SerializedString from %T", x)
	}
}

var _ Input = SerializedString[any]{}

func (s SerializedString[T]) Decoder() InputDecoder {
	return s
}

func (s SerializedString[T]) ToLiteral() call.Literal {
	return call.NewLiteralString(s.String())
}

func (s SerializedString[T]) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.Self)
}

func (s *SerializedString[T]) UnmarshalJSON(p []byte) error {
	var v T
	if err := json.Unmarshal(p, &v); err != nil {
		return err
	}
	*s = SerializedString[T]{v}
	return nil
}

func (s SerializedString[T]) String() string {
	res, err := s.MarshalJSON()
	if err != nil {
		panic(err)
	}
	return string(res)
}

var _ Setter = SerializedString[any]{}

func (s SerializedString[T]) SetField(v reflect.Value) error {
	switch v.Interface().(type) {
	case SerializedString[T]:
		v.Set(reflect.ValueOf(s))
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

func (s Scalar[T]) TypeDefinition(view call.View) *ast.Definition {
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

func IDTypeNameForRawType(t string) string {
	return t + "ID"
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
func (i ID[T]) TypeDefinition(view call.View) *ast.Definition {
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
func (i ID[T]) Load(ctx context.Context, server *Server) (res ObjectResult[T], _ error) {
	val, err := server.Load(ctx, i.id)
	if err != nil {
		return res, fmt.Errorf("load %s: %w", i.id.DisplaySelf(), err)
	}
	obj, ok := val.(ObjectResult[T])
	if !ok {
		return res, fmt.Errorf("load %s: expected %T, got %T", i.id.DisplaySelf(), obj, val)
	}
	return obj, nil
}

// Enumerable is a value that has a length and allows indexing.
type Enumerable interface {
	// Element returns the element of the Enumerable.
	Element() Typed
	// Len returns the number of elements in the Enumerable.
	Len() int
	// Nth returns the Nth element of the Enumerable, with 1 representing the
	// first entry.
	Nth(int) (Typed, error)

	NthValue(i int, enumID *call.ID) (AnyResult, error)
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
	switch x := val.(type) {
	case []any:
		var zero I
		decoder := zero.Decoder()

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

func NewIntArray[T constraints.Integer](elems ...T) Array[Int] {
	return ToArray(NewInt, elems...)
}

func NewFloatArray[T constraints.Float](elems ...T) Array[Float] {
	return ToArray(NewFloat, elems...)
}

func NewBooleanArray(elems ...bool) Array[Boolean] {
	return ToArray(NewBoolean, elems...)
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

func (arr Array[T]) Element() Typed {
	var t T
	return t
}

func (arr Array[T]) Len() int {
	return len(arr)
}

func (arr Array[T]) nth(i int) (T, error) {
	if i < 1 || i > len(arr) {
		var zero T
		return zero, fmt.Errorf("index %d out of bounds", i)
	}
	return arr[i-1], nil
}

func (arr Array[T]) Nth(i int) (Typed, error) {
	return arr.nth(i)
}

func (arr Array[T]) NthValue(i int, enumID *call.ID) (AnyResult, error) {
	t, err := arr.nth(i)
	if err != nil {
		return nil, err
	}

	return Result[T]{
		constructor: enumID.SelectNth(i),
		self:        t,
	}, nil
}

type ResultArray[T Typed] []Result[T]

var _ Typed = ResultArray[Typed]{}
var _ Enumerable = ResultArray[Typed]{}

func (i ResultArray[T]) Type() *ast.Type {
	var t T
	return &ast.Type{
		Elem:    t.Type(),
		NonNull: true,
	}
}

func (arr ResultArray[T]) Element() Typed {
	var t T
	return t
}

func (arr ResultArray[T]) Len() int {
	return len(arr)
}

func (arr ResultArray[T]) nth(i int) (res Result[T], _ error) {
	if i < 1 || i > len(arr) {
		return res, fmt.Errorf("index %d out of bounds", i)
	}
	return arr[i-1], nil
}

func (arr ResultArray[T]) Nth(i int) (Typed, error) {
	inst, err := arr.nth(i)
	if err != nil {
		return nil, err
	}
	return inst.Self(), nil
}

func (arr ResultArray[T]) NthValue(i int, enumID *call.ID) (AnyResult, error) {
	inst, err := arr.nth(i)
	if err != nil {
		return nil, err
	}

	return inst, nil
}

type ObjectResultArray[T Typed] []ObjectResult[T]

var _ Typed = ObjectResultArray[Typed]{}
var _ Enumerable = ObjectResultArray[Typed]{}

func (i ObjectResultArray[T]) Type() *ast.Type {
	var t T
	return &ast.Type{
		Elem:    t.Type(),
		NonNull: true,
	}
}

func (arr ObjectResultArray[T]) Element() Typed {
	var t T
	return t
}

func (arr ObjectResultArray[T]) Len() int {
	return len(arr)
}

func (arr ObjectResultArray[T]) nth(i int) (res ObjectResult[T], _ error) {
	if i < 1 || i > len(arr) {
		return res, fmt.Errorf("index %d out of bounds", i)
	}
	return arr[i-1], nil
}

func (arr ObjectResultArray[T]) Nth(i int) (Typed, error) {
	inst, err := arr.nth(i)
	if err != nil {
		return nil, err
	}
	return inst.Self(), nil
}

func (arr ObjectResultArray[T]) NthValue(i int, enumID *call.ID) (AnyResult, error) {
	inst, err := arr.nth(i)
	if err != nil {
		return nil, err
	}

	return inst, nil
}

type enumValue interface {
	Input
	~string
}

type EnumValue[T enumValue] struct {
	Value       T
	Underlying  T
	Description string
	View        ViewFilter
}

// EnumValues is a list of possible values for an Enum.
type EnumValues[T enumValue] []EnumValue[T]

// NewEnum creates a new EnumType with the given possible values.
func NewEnum[T enumValue](vals ...T) *EnumValues[T] {
	enum := make(EnumValues[T], 0, len(vals))
	for _, val := range vals {
		enum = append(enum, EnumValue[T]{
			Value: val,
		})
	}
	return &enum
}

func (e *EnumValues[T]) Type() *ast.Type {
	var zero T
	return zero.Type()
}

func (e *EnumValues[T]) TypeName() string {
	return e.Type().Name()
}

func (e *EnumValues[T]) TypeDefinition(view call.View) *ast.Definition {
	def := &ast.Definition{
		Kind:       ast.Enum,
		Name:       e.TypeName(),
		EnumValues: e.PossibleValues(view),
	}
	var val T
	if isType, ok := any(val).(Descriptive); ok {
		def.Description = isType.TypeDescription()
	}
	return def
}

func (e *EnumValues[T]) DecodeInput(val any) (Input, error) {
	if enum, ok := val.(T); ok {
		val = string(enum)
	}

	v, err := (&EnumValueName{Enum: e.TypeName()}).DecodeInput(val)
	if err != nil {
		return nil, err
	}
	return e.Lookup(v.(*EnumValueName).Name)
}

func (e *EnumValues[T]) PossibleValues(view call.View) ast.EnumValueList {
	var values ast.EnumValueList
	for _, val := range *e {
		if val.View != nil && !val.View.Contains(view) {
			continue
		}
		def := &ast.EnumValueDefinition{
			Name:        string(val.Value),
			Description: val.Description,
		}
		if val.Value != val.Underlying {
			def.Directives = append(def.Directives, enumValueDirectives(val.Underlying)...)
		}
		values = append(values, def)
	}

	return values
}

func enumValueDirectives[T enumValue](value T) []*ast.Directive {
	var zero T
	if value == zero {
		return nil
	}

	return []*ast.Directive{
		{
			Name: "enumValue",
			Arguments: ast.ArgumentList{
				{
					Name: "value",
					Value: &ast.Value{
						Kind: ast.StringValue,
						Raw:  string(value),
					},
				},
			},
		},
	}
}

func (e *EnumValues[T]) Literal(val T) call.Literal {
	return call.NewLiteralEnum(string(val))
}

func (e *EnumValues[T]) Lookup(val string) (T, error) {
	var zero T
	for _, possible := range *e {
		if val == string(possible.Value) {
			if possible.Underlying != zero {
				return possible.Underlying, nil
			}
			return possible.Value, nil
		}
	}
	return zero, fmt.Errorf("invalid enum member %q for %T", val, zero)
}

func (e *EnumValues[T]) Register(val T, desc ...string) T {
	return e.RegisterView(val, nil, desc...)
}

func (e *EnumValues[T]) RegisterView(val T, view ViewFilter, desc ...string) T {
	*e = append(*e, EnumValue[T]{
		Value:       val,
		Description: FormatDescription(desc...),
		View:        view,
	})
	return val
}

func (e *EnumValues[T]) Alias(val T, target T) T {
	return e.AliasView(val, target, nil)
}

func (e *EnumValues[T]) AliasView(val T, target T, view ViewFilter) T {
	for _, v := range *e {
		if v.Value == target {
			*e = append(*e, EnumValue[T]{
				Value:       val,
				Underlying:  v.Value,
				Description: v.Description,
				View:        view,
			})
			return val
		}
	}
	panic(fmt.Sprintf("cannot find enum %q", target))
}

func (e *EnumValues[T]) Install(srv *Server) {
	var zero T
	srv.scalars[zero.Type().Name()] = e
}

type EnumValueName struct {
	Enum string
	Name string
}

var _ Input = &EnumValueName{}

func (e *EnumValueName) TypeName() string {
	return e.Enum
}

func (e *EnumValueName) Type() *ast.Type {
	return &ast.Type{
		NamedType: e.Enum,
		NonNull:   true,
	}
}

func (e *EnumValueName) TypeDefinition(view call.View) *ast.Definition {
	return &ast.Definition{
		Kind: ast.Enum,
		Name: e.TypeName(),
	}
}

func (e *EnumValueName) ToLiteral() call.Literal {
	return call.NewLiteralEnum(e.Name)
}

func (e *EnumValueName) Decoder() InputDecoder {
	return e
}

func (e *EnumValueName) DecodeInput(val any) (Input, error) {
	switch x := val.(type) {
	case *EnumValueName:
		return &EnumValueName{Enum: e.Enum, Name: x.Name}, nil
	case string:
		return &EnumValueName{Enum: e.Enum, Name: x}, nil
	case bool:
		return nil, fmt.Errorf("invalid enum name %t", x)
	case nil:
		return nil, fmt.Errorf("invalid enum name null")
	default:
		return nil, fmt.Errorf("cannot create enum name from %T", x)
	}
}

func (e *EnumValueName) MarshalJSON() ([]byte, error) {
	return json.Marshal(e.Name)
}

func MustInputSpec(val Type) InputObjectSpec {
	spec := InputObjectSpec{
		Name: val.TypeName(),
	}
	if desc, ok := val.(Descriptive); ok {
		spec.Description = desc.TypeDescription()
	}
	inputs, err := InputSpecsForType(val, true)
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

func (spec InputObjectSpec) TypeDefinition(view call.View) *ast.Definition {
	return &ast.Definition{
		Kind:        ast.InputObject,
		Name:        spec.Name,
		Description: spec.Description,
		Fields:      spec.Fields.FieldDefinitions(view),
	}
}
