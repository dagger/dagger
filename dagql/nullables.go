package dagql

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/dagger/dagger/dagql/idproto"
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

var _ Derefable = Optional[Input]{}

func (n Optional[I]) DecodeInput(val any) (Input, error) {
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

func (n Optional[I]) Deref() (Typed, bool) {
	return n.Value, n.Valid
}

func (i *Optional[I]) UnmarshalJSON(p []byte) error {
	if err := json.Unmarshal(p, &i.Value); err != nil {
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

func (d DynamicOptional) Type() *ast.Type {
	cp := *d.Elem.Type()
	cp.NonNull = false
	return &cp
}

func (d DynamicOptional) Decoder() InputDecoder {
	return d
}

func (d DynamicOptional) ToLiteral() *idproto.Literal {
	if !d.Valid {
		return &idproto.Literal{
			Value: &idproto.Literal_Null{
				Null: true,
			},
		}
	}
	return d.Value.ToLiteral()
}

var _ InputDecoder = DynamicOptional{}

func (n DynamicOptional) DecodeInput(val any) (Input, error) {
	if val == nil {
		return DynamicOptional{
			Elem:  n,
			Valid: false,
		}, nil
	}
	input, err := n.Elem.Decoder().DecodeInput(val)
	if err != nil {
		return nil, err
	}
	return DynamicOptional{
		Elem:  n.Elem,
		Value: input,
		Valid: true,
	}, nil
}

var _ Setter = DynamicOptional{}

func (n DynamicOptional) SetField(val reflect.Value) error {
	switch val.Kind() {
	case reflect.Ptr:
		if n.Valid {
			ptr := reflect.New(val.Type().Elem())
			if err := assign(ptr.Elem(), n.Value); err != nil {
				return fmt.Errorf("dynamic optional pointer: %w", err)
			}
			val.Set(ptr)
		}
	default:
		if n.Valid {
			if err := assign(val, n.Value); err != nil {
				return fmt.Errorf("dynamic optional: %w", err)
			}
			return nil
		}
	}
	return nil
}

var _ Derefable = DynamicOptional{}

func (n DynamicOptional) Deref() (Typed, bool) {
	return n.Value, n.Valid
}

func (i DynamicOptional) MarshalJSON() ([]byte, error) {
	if !i.Valid {
		return json.Marshal(nil)
	}
	optional, err := json.Marshal(i.Value)
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

type DynamicNullable struct {
	Elem  Typed
	Value Typed
	Valid bool
}

var _ Typed = DynamicNullable{}

func (d DynamicNullable) Type() *ast.Type {
	cp := *d.Elem.Type()
	cp.NonNull = false
	return &cp
}

var _ Derefable = DynamicNullable{}

func (n DynamicNullable) Deref() (Typed, bool) {
	return n.Value, n.Valid
}

func (i DynamicNullable) MarshalJSON() ([]byte, error) {
	if !i.Valid {
		return json.Marshal(nil)
	}
	return json.Marshal(i.Value)
}

func (i *DynamicNullable) UnmarshalJSON(p []byte) error {
	if err := json.Unmarshal(p, &i.Value); err != nil {
		return err
	}
	return nil
}
