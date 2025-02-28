package dagql

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql/call"
)

// Derefable is a type that wraps another type.
//
// In practice this is only used for Optional and Nullable. It should be used
// sparingly, since wrapping interfaces explodes very quickly.
type Derefable interface {
	Deref() (Typed, bool)
}

// Optional wraps a type and allows it to be null.
//
// This is used for optional arguments and return values.
type Optional[I Input] struct {
	Value I
	// true if the value is set
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

// GetOr returns the value of the Optional, or the given value if the Optional
// is empty.
func (n Optional[I]) GetOr(v I) I {
	if !n.Valid {
		return v
	}
	return n.Value
}

// NoOpt returns an empty Optional value.
func NoOpt[I Input]() Optional[I] {
	return Optional[I]{}
}

func (o Optional[I]) ToNullable() Nullable[I] {
	return Nullable[I](o)
}

func (o Optional[I]) Decoder() InputDecoder {
	return o
}

func (o Optional[I]) ToLiteral() call.Literal {
	if !o.Valid {
		return call.NewLiteralNull()
	}
	return o.Value.ToLiteral()
}

func (o Optional[I]) MarshalJSON() ([]byte, error) {
	if !o.Valid {
		return json.Marshal(nil)
	}
	return json.Marshal(o.Value)
}

var _ Typed = Optional[Input]{}

func (o Optional[I]) Type() *ast.Type {
	nullable := *o.Value.Type()
	nullable.NonNull = false
	return &nullable
}

var _ Derefable = Optional[Input]{}

func (o Optional[I]) DecodeInput(val any) (Input, error) {
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

func (o Optional[I]) Deref() (Typed, bool) {
	return o.Value, o.Valid
}

func (o *Optional[I]) UnmarshalJSON(p []byte) error {
	if err := json.Unmarshal(p, &o.Value); err != nil {
		return err
	}
	return nil
}

type DynamicOptional struct {
	Elem  Input
	Value Input
	Valid bool
}

var _ Input = DynamicOptional{}

func (o DynamicOptional) Type() *ast.Type {
	cp := *o.Elem.Type()
	cp.NonNull = false
	return &cp
}

func (o DynamicOptional) Decoder() InputDecoder {
	return o
}

func (o DynamicOptional) ToLiteral() call.Literal {
	if !o.Valid {
		return call.NewLiteralNull()
	}
	return o.Value.ToLiteral()
}

var _ InputDecoder = DynamicOptional{}

func (o DynamicOptional) DecodeInput(val any) (Input, error) {
	if val == nil {
		return DynamicOptional{
			Elem:  o.Elem,
			Valid: false,
		}, nil
	}
	input, err := o.Elem.Decoder().DecodeInput(val)
	if err != nil {
		return nil, err
	}
	return DynamicOptional{
		Elem:  o.Elem,
		Value: input,
		Valid: true,
	}, nil
}

var _ Setter = DynamicOptional{}

func (o DynamicOptional) SetField(val reflect.Value) error {
	switch val.Kind() {
	case reflect.Ptr:
		if o.Valid {
			ptr := reflect.New(val.Type().Elem())
			if err := assign(ptr.Elem(), o.Value); err != nil {
				return fmt.Errorf("dynamic optional pointer: %w", err)
			}
			val.Set(ptr)
		}
	default:
		if o.Valid {
			if err := assign(val, o.Value); err != nil {
				return fmt.Errorf("dynamic optional: %w", err)
			}
			return nil
		}
	}
	return nil
}

var _ Derefable = DynamicOptional{}

func (o DynamicOptional) Deref() (Typed, bool) {
	return o.Value, o.Valid
}

func (o DynamicOptional) MarshalJSON() ([]byte, error) {
	if !o.Valid {
		return json.Marshal(nil)
	}
	optional, err := json.Marshal(o.Value)
	if err != nil {
		return nil, err
	}
	return optional, nil
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

var _ Derefable = Nullable[Typed]{}

func (n Nullable[T]) Deref() (Typed, bool) {
	return n.Value, n.Valid
}

func (n Nullable[T]) MarshalJSON() ([]byte, error) {
	if !n.Valid {
		return json.Marshal(nil)
	}
	return json.Marshal(n.Value)
}

func (n *Nullable[T]) UnmarshalJSON(p []byte) error {
	if err := json.Unmarshal(p, &n.Value); err != nil {
		return err
	}
	return nil
}

type DynamicNullable struct {
	Elem  Typed
	Value Typed
	Valid bool
}

var _ Typed = DynamicNullable{}

func (n DynamicNullable) Type() *ast.Type {
	cp := *n.Elem.Type()
	cp.NonNull = false
	return &cp
}

var _ Derefable = DynamicNullable{}

func (n DynamicNullable) Deref() (Typed, bool) {
	return n.Value, n.Valid
}

func (n DynamicNullable) MarshalJSON() ([]byte, error) {
	if !n.Valid {
		return json.Marshal(nil)
	}
	return json.Marshal(n.Value)
}

func (n *DynamicNullable) UnmarshalJSON(p []byte) error {
	if err := json.Unmarshal(p, &n.Value); err != nil {
		return err
	}
	return nil
}
